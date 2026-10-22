package agentattachment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agenttransport"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) Advance(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.now = c.now.Add(d) }

type fixture struct {
	root  string
	s     *Server
	r     *agentregistry.Registry
	clock *testClock
}

func setup(t *testing.T, change func(*Config)) fixture {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fixture: %v %s", err, out)
	}
	if _, err := enrollment.Enroll(root, enrollment.Native, "test explicit human authority"); err != nil {
		t.Fatal(err)
	}
	c := &testClock{now: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	r, err := agentregistry.Open(root, agentregistry.Policy{StaleAfter: time.Minute}, c.Now)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Clock = c.Now
	if change != nil {
		change(&config)
	}
	s, err := New(r, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return fixture{root, s, r, c}
}
func identity(agent, session string) agentprotocol.Identity {
	return agentprotocol.Identity{ProtocolVersion: agentprotocol.Version2, AgentID: agentprotocol.AgentID(agent), SessionID: agentprotocol.SessionID(session),
		Metadata: agentprotocol.Metadata{Provider: "unknown-future-provider", Runtime: "PLANNER"}, Capabilities: []agentprotocol.Capability{"coding"}, SupportedRoles: []agentprotocol.Role{agentprotocol.RoleWorker}}
}
func invite(t *testing.T, f fixture, agent string, replace bool) (Pending, agentprotocol.InvitationProof) {
	t.Helper()
	p, proof, err := f.s.Invite(InviteRequest{AgentID: agentprotocol.AgentID(agent), Roles: []agentprotocol.Role{agentprotocol.RoleWorker}, Capabilities: []agentprotocol.Capability{"coding"}, TTL: 30 * time.Second, Replace: replace}, true)
	if err != nil {
		t.Fatal(err)
	}
	return p, proof
}
func await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("test synchronization timed out")
		var zero T
		return zero
	}
}
func connection(t *testing.T, f fixture) (agenttransport.Transport, <-chan error) {
	t.Helper()
	a, o, err := agenttransport.NewMemoryPair(2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.s.Handle(ctx, o) }()
	t.Cleanup(func() { cancel(); _ = a.Close() })
	return a, done
}
func attach(t *testing.T, f fixture, id agentprotocol.Identity, proof agentprotocol.InvitationProof) (*Client, <-chan error) {
	t.Helper()
	a, done := connection(t, f)
	c, err := Attach(context.Background(), a, id, proof)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, done
}
func readState(t *testing.T, f fixture) state.Snapshot {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, "multi-agent-orchestrator", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s state.Snapshot
	if json.Unmarshal(b, &s) != nil {
		t.Fatal("state JSON")
	}
	return s
}
func record(t *testing.T, f fixture, id agentprotocol.SessionID) agentregistry.View {
	t.Helper()
	v, err := f.r.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestAdmissionBindingHeartbeatAndDisconnect(t *testing.T) {
	f := setup(t, nil)
	_, proof := invite(t, f, "agent", false)
	c, done := attach(t, f, identity("agent", "session-one"), proof)
	v := record(t, f, "session-one")
	if c.Binding() != v.Ref() || v.Session.Generation != 1 || v.Session.Status != agentregistry.Ready || v.Session.AssignedRole != "" || !v.Live {
		t.Fatal("admission identity/role/generation")
	}
	if v.Session.Identity.Metadata.Provider != "unknown-future-provider" {
		t.Fatal("unknown provider changed")
	}
	f.clock.Advance(time.Second)
	if err := c.Heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !record(t, f, "session-one").Session.LastSeenAt.Equal(f.clock.Now()) {
		t.Fatal("heartbeat not applied")
	}
	if err := c.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
	if record(t, f, "session-one").Session.Status != agentregistry.Disconnected {
		t.Fatal("disconnect not applied")
	}
	s := readState(t, f)
	if len(s.Claims) != 0 || len(s.Tasks) != 0 || len(s.Sessions) != 0 {
		t.Fatal("attachment minted managed task ownership")
	}
}

func TestInvalidExpiredRevokedAndReplayInvitations(t *testing.T) {
	for _, mode := range []string{"invalid", "expired", "revoked", "wrong-agent", "roles", "capability", "missing-authorization", "replay"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t, nil)
			p, proof := invite(t, f, "agent", false)
			id := identity("agent", "run")
			switch mode {
			case "invalid":
				proof.Secret = strings.Repeat("0", 64)
			case "expired":
				f.clock.Advance(30 * time.Second)
			case "revoked":
				if err := f.s.Revoke(p.ID); err != nil {
					t.Fatal(err)
				}
			case "wrong-agent":
				id.AgentID = "impostor"
			case "roles":
				id.SupportedRoles = []agentprotocol.Role{agentprotocol.RolePlanner}
			case "capability":
				id.Capabilities = []agentprotocol.Capability{"uninvited"}
			case "missing-authorization":
				if _, _, err := f.s.Invite(InviteRequest{}, false); err == nil {
					t.Fatal("silence authorized invite")
				}
				if err := f.s.Revoke(p.ID); err != nil {
					t.Fatal(err)
				}
			case "replay":
				c, done := attach(t, f, id, proof)
				if err := c.Disconnect(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := await(t, done); err != nil {
					t.Fatal(err)
				}
				id.SessionID = "replayed"
			}
			before := readState(t, f).AgentRegistry.LastGeneration
			a, done := connection(t, f)
			_, err := Attach(context.Background(), a, id, proof)
			if err == nil || strings.Contains(err.Error(), proof.Secret) {
				t.Fatal("invalid invitation admitted/leaked")
			}
			if err := await(t, done); err == nil {
				t.Fatal("server did not reject")
			}
			after := readState(t, f)
			if after.AgentRegistry.LastGeneration != before {
				t.Fatal("failed admission created a session")
			}
		})
	}
}

func TestBoundIdentitySwitchDuplicateAndUnsupportedMessages(t *testing.T) {
	for _, mode := range []string{"agent", "session", "duplicate", "result", "assignment-reference", "capabilities", "role"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t, nil)
			_, proof := invite(t, f, "agent", false)
			c, done := attach(t, f, identity("agent", "run"), proof)
			e := agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version2, Type: agentprotocol.Heartbeat, AgentID: "agent", SessionID: "run"}
			switch mode {
			case "agent":
				e.AgentID = "other"
			case "session":
				e.SessionID = "other"
			case "duplicate":
				e.Type = agentprotocol.EnrollmentRequest
				id := identity("agent", "run")
				e.Identity = &id
				e.Invitation = &proof
			case "capabilities":
				e.Type = agentprotocol.CapabilityRegistration
				id := identity("agent", "run")
				e.Identity = &id
			case "result", "assignment-reference":
				e.Assignment = &agentprotocol.AssignmentRef{ObjectiveID: "objective", TaskID: "task", AssignmentGeneration: 42, Token: "private-fixture-claim", SessionID: e.SessionID}
				if mode == "result" {
					e.Type = agentprotocol.Result
					e.Detail = "unadmitted result"
				}
			case "role":
				e.Role = agentprotocol.RolePlanner
			}
			err := c.transport.Send(context.Background(), e)
			if mode == "role" {
				if err == nil {
					t.Fatal("heartbeat assigned role")
				}
				_ = c.Close()
			} else {
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := await(t, done); err == nil {
				t.Fatal("unsupported/identity-switched message accepted")
			}
			if len(readState(t, f).Tasks) != 0 || record(t, f, "run").Session.AssignedRole != "" {
				t.Fatal("session message gained authority")
			}
		})
	}
}

func TestFreshInvitationSupersessionFencesOldHeartbeatAndClose(t *testing.T) {
	for _, heartbeat := range []bool{false, true} {
		t.Run(map[bool]string{false: "late-close", true: "late-heartbeat"}[heartbeat], func(t *testing.T) {
			f := setup(t, nil)
			_, proof := invite(t, f, "agent", false)
			old, oldDone := attach(t, f, identity("agent", "old"), proof)
			_, nextProof := invite(t, f, "agent", true)
			fresh, freshDone := attach(t, f, identity("agent", "new"), nextProof)
			if fresh.Binding().Generation <= old.Binding().Generation {
				t.Fatal("incarnation did not advance")
			}
			before := record(t, f, "new")
			if heartbeat {
				if err := old.Heartbeat(context.Background()); err == nil {
					t.Fatal("stale heartbeat accepted")
				}
			} else {
				_ = old.Close()
			}
			_ = await(t, oldDone)
			after := record(t, f, "new")
			if !after.Live || after.Session.Status != agentregistry.Ready || !after.Session.LastSeenAt.Equal(before.Session.LastSeenAt) {
				t.Fatal("old connection altered replacement")
			}
			if err := fresh.Disconnect(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := await(t, freshDone); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInvitationLimitListingAndRestart(t *testing.T) {
	f := setup(t, func(c *Config) { c.MaxInvitations = 1 })
	p, proof := invite(t, f, "agent", false)
	if _, _, err := f.s.Invite(InviteRequest{AgentID: "other", Roles: p.Roles, Capabilities: p.Capabilities, TTL: time.Second}, true); err == nil {
		t.Fatal("unbounded invitations")
	}
	listed, err := f.s.Invitations()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(listed)
	if bytes.Contains(b, []byte(proof.Secret)) || bytes.Contains(b, []byte("hash")) {
		t.Fatal("listing exposes verification material")
	}
	listed[0].Roles[0] = agentprotocol.RolePlanner
	if f.s.pending[p.ID].Roles[0] != agentprotocol.RoleWorker {
		t.Fatal("listing mutates constraints")
	}
	config := f.s.c
	f.s.Close()
	other, err := New(f.r, config)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	f.s = other
	a, done := connection(t, f)
	if _, err := Attach(context.Background(), a, identity("agent", "run"), proof); err == nil {
		t.Fatal("restart preserved ephemeral invitation")
	}
	_ = await(t, done)
}

func TestConcurrentCredentialConsumptionHasOneWinner(t *testing.T) {
	f := setup(t, nil)
	_, proof := invite(t, f, "agent", false)
	results := make(chan *Client, 2)
	errs := make(chan error, 2)
	dones := make([]<-chan error, 0, 2)
	for _, name := range []string{"one", "two"} {
		a, done := connection(t, f)
		dones = append(dones, done)
		go func(name string) {
			c, err := Attach(context.Background(), a, identity("agent", name), proof)
			results <- c
			errs <- err
		}(name)
	}
	winners := 0
	for i := 0; i < 2; i++ {
		c := await(t, results)
		if c != nil {
			winners++
			_ = c.Close()
		}
		_ = await(t, errs)
	}
	for _, done := range dones {
		_ = await(t, done)
	}
	if winners != 1 || readState(t, f).AgentRegistry.LastGeneration != 1 {
		t.Fatal("one-use credential race failed")
	}
}

func TestStaleReplacementInvitationCannotReplaceNewerSession(t *testing.T) {
	f := setup(t, nil)
	_, proof := invite(t, f, "agent", false)
	old, oldDone := attach(t, f, identity("agent", "old"), proof)
	_, first := invite(t, f, "agent", true)
	_, stale := invite(t, f, "agent", true)
	fresh, freshDone := attach(t, f, identity("agent", "new"), first)
	a, done := connection(t, f)
	if _, err := Attach(context.Background(), a, identity("agent", "stale-replacement"), stale); err == nil {
		t.Fatal("stale invitation superseded newer session")
	}
	_ = await(t, done)
	if !record(t, f, "new").Live {
		t.Fatal("replacement lost authority")
	}
	_ = old.Close()
	_ = await(t, oldDone)
	_ = fresh.Close()
	_ = await(t, freshDone)
}

func TestRegistryDisconnectIdempotenceAndPolicyBounds(t *testing.T) {
	f := setup(t, nil)
	_, proof := invite(t, f, "agent", false)
	c, done := attach(t, f, identity("agent", "run"), proof)
	_ = c.Close()
	_ = await(t, done)
	if err := f.r.Disconnect(c.Binding(), "repeat exact cleanup"); err != nil {
		t.Fatal(err)
	}
	if err := f.r.Check(c.Binding()); !errors.Is(err, agentregistry.ErrFenced) {
		t.Fatal("disconnected ref passed check")
	}
	config := f.s.c
	config.IdleTimeout = f.r.LivenessThreshold() + time.Second
	if _, err := New(f.r, config); err == nil {
		t.Fatal("idle timeout outlives registry policy")
	}
}
