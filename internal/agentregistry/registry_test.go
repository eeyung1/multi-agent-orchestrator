package agentregistry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

type testClock struct {
	mu    sync.Mutex
	value time.Time
}

func (c *testClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.value }
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = c.value.Add(d)
}

type fixture struct {
	root     string
	registry *Registry
	clock    *testClock
	policy   Policy
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	d := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "Test"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(d, "seed"), []byte("seed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "seed"}, {"commit", "-m", "seed"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, out)
		}
	}
	if _, err := enrollment.Enroll(d, enrollment.Native, "test human approved authority"); err != nil {
		t.Fatal(err)
	}
	clock := &testClock{value: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)}
	policy := Policy{StaleAfter: 10 * time.Second}
	r, err := Open(d, policy, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{root: d, registry: r, clock: clock, policy: policy}
}

func identity(agent, session string) agentprotocol.Identity {
	return agentprotocol.Identity{ProtocolVersion: agentprotocol.Version1,
		AgentID: agentprotocol.AgentID(agent), SessionID: agentprotocol.SessionID(session),
		Metadata:       agentprotocol.Metadata{Provider: "future-unknown-provider", Runtime: "test-runtime"},
		Capabilities:   []agentprotocol.Capability{"coding", "example/review:v2"},
		SupportedRoles: []agentprotocol.Role{agentprotocol.RoleWorker}}
}

func admit(t *testing.T, f fixture, id agentprotocol.Identity) View {
	t.Helper()
	v, err := f.registry.Enroll(Enrollment{Identity: id, Decision: "trusted Go admission"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func get(t *testing.T, f fixture, id agentprotocol.SessionID) View {
	t.Helper()
	v, err := f.registry.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func rawState(t *testing.T, f fixture) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, "multi-agent-orchestrator", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func snapshot(t *testing.T, f fixture) state.Snapshot {
	t.Helper()
	var s state.Snapshot
	if err := json.Unmarshal(rawState(t, f), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEnrollmentReadyUnknownProviderAndSeparateIdentities(t *testing.T) {
	f := newFixture(t)
	id := identity("logical-agent", "runtime-session")
	id.Metadata.Provider, id.Metadata.Runtime = "PLANNER", "ORCHESTRATOR"
	v := admit(t, f, id)
	if !v.Current || !v.Live || v.Session.Status != Ready || v.Session.AssignedRole != "" || v.Session.Generation != 1 {
		t.Fatalf("unexpected enrollment: %+v", v)
	}
	if v.Session.Identity.AgentID != id.AgentID || v.Session.Identity.SessionID != id.SessionID || reflect.TypeOf(id.AgentID) == reflect.TypeOf(id.SessionID) {
		t.Fatal("agent/session identities were conflated")
	}
	if !v.Session.EnrolledAt.Equal(f.clock.Now()) || !v.Session.LastSeenAt.Equal(v.Session.EnrolledAt) {
		t.Fatal("timestamps not Go-issued")
	}
	if got, err := f.registry.Eligible(agentprotocol.RoleWorker, "coding"); err != nil || len(got) != 0 {
		t.Fatalf("supported role granted assignment eligibility: %v %v", got, err)
	}
	if _, ok := reflect.TypeOf(Enrollment{}).FieldByName("Generation"); ok {
		t.Fatal("enrollment can choose its issued generation")
	}
	id.Capabilities[0] = "modified-after-call"
	if get(t, f, v.Ref().SessionID).Session.Identity.Capabilities[0] != "coding" {
		t.Fatal("registry retained caller-owned memory")
	}
}

func TestEnrollmentValidationAndNoImplicitDecisions(t *testing.T) {
	for _, mutate := range []func(*agentprotocol.Identity){
		func(id *agentprotocol.Identity) { id.AgentID = "" },
		func(id *agentprotocol.Identity) { id.SessionID = "" },
		func(id *agentprotocol.Identity) { id.ProtocolVersion = "future/v99" },
		func(id *agentprotocol.Identity) { id.Capabilities = []agentprotocol.Capability{"coding", "coding"} },
		func(id *agentprotocol.Identity) {
			id.SupportedRoles = []agentprotocol.Role{agentprotocol.RoleWorker, agentprotocol.RoleWorker}
		},
		func(id *agentprotocol.Identity) { id.SupportedRoles = []agentprotocol.Role{"ORCHESTRATOR"} },
	} {
		f := newFixture(t)
		id := identity("agent", "run")
		mutate(&id)
		before := rawState(t, f)
		if _, err := f.registry.Enroll(Enrollment{Identity: id, Decision: "Go approved"}); err == nil {
			t.Fatal("invalid identity admitted")
		}
		if !bytes.Equal(before, rawState(t, f)) {
			t.Fatal("invalid identity changed state")
		}
	}
	f := newFixture(t)
	if _, err := f.registry.Enroll(Enrollment{Identity: identity("agent", "run")}); err == nil {
		t.Fatal("silence authorized admission")
	}
}

func TestCaseSensitiveRegistryIdentitiesRemainReadable(t *testing.T) {
	f := newFixture(t)
	first := admit(t, f, identity("Agent", "Run"))
	second := admit(t, f, identity("agent", "run"))
	all, err := f.registry.List()
	if err != nil || len(all) != 2 {
		t.Fatalf("case-sensitive identities lost: %v", err)
	}
	if !get(t, f, first.Ref().SessionID).Current || !get(t, f, second.Ref().SessionID).Current {
		t.Fatal("distinct identities conflated")
	}
}

func TestNewSessionSupersedesAndFencesEveryOldMutation(t *testing.T) {
	f := newFixture(t)
	id := identity("logical-agent", "old-runtime")
	old := admit(t, f, id)
	if err := f.registry.AssignRole(old.Ref(), agentprotocol.RoleWorker, "Go assigned worker"); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Second)
	id.SessionID = "new-runtime"
	previous := old.Ref()
	if _, err := f.registry.Enroll(Enrollment{Identity: id, Decision: "Go approved"}); err == nil {
		t.Fatal("replaced a session without a previous-incarnation fence")
	}
	wrong := previous
	wrong.Generation++
	if _, err := f.registry.Enroll(Enrollment{Identity: id, Supersedes: &wrong, Decision: "Go approved"}); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale supersession: %v", err)
	}
	next, err := f.registry.Enroll(Enrollment{Identity: id, Supersedes: &previous, Decision: "explicit replacement admission"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Ref().Generation <= previous.Generation || next.Ref().AgentID != previous.AgentID || next.Ref().SessionID == previous.SessionID || next.Session.AssignedRole != "" {
		t.Fatal("replacement reused identity/role")
	}
	historical := get(t, f, previous.SessionID)
	if historical.Current || historical.Live || historical.Session.Status != Disconnected || historical.Session.SupersededBy != next.Ref().SessionID || historical.Session.AssignedRole != agentprotocol.RoleWorker {
		t.Fatal("supersession history missing")
	}
	before := rawState(t, f)
	for _, action := range []func() error{
		func() error { return f.registry.Heartbeat(previous) },
		func() error { return f.registry.AssignRole(previous, agentprotocol.RoleWorker, "stale decision") },
		func() error { return f.registry.ClearRole(previous, "stale decision") },
		func() error { return f.registry.Register(previous, identity("logical-agent", "old-runtime")) },
		func() error { return f.registry.Transition(previous, Ready, "stale transition") },
	} {
		if err := action(); !errors.Is(err, ErrFenced) {
			t.Fatalf("old session mutation was not fenced: %v", err)
		}
	}
	if !bytes.Equal(before, rawState(t, f)) {
		t.Fatal("old session mutated new incarnation")
	}
	if _, err := f.registry.Enroll(Enrollment{Identity: identity("other-agent", "old-runtime"), Decision: "Go approved"}); err == nil {
		t.Fatal("historical session ID was reused")
	}
}

func TestHeartbeatExactFenceUpdatesOnlyLastSeen(t *testing.T) {
	f := newFixture(t)
	v := admit(t, f, identity("agent", "run"))
	if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, "Go assigned role"); err != nil {
		t.Fatal(err)
	}
	before := get(t, f, v.Ref().SessionID).Session
	f.clock.Advance(3 * time.Second)
	for _, ref := range []Ref{{AgentID: "wrong", SessionID: v.Ref().SessionID, Generation: v.Ref().Generation}, {AgentID: v.Ref().AgentID, SessionID: "wrong", Generation: v.Ref().Generation}, {AgentID: v.Ref().AgentID, SessionID: v.Ref().SessionID, Generation: v.Ref().Generation + 1}, {}} {
		if err := f.registry.Heartbeat(ref); err == nil {
			t.Fatal("bad heartbeat identity accepted")
		}
	}
	if err := f.registry.Heartbeat(v.Ref()); err != nil {
		t.Fatal(err)
	}
	after := get(t, f, v.Ref().SessionID).Session
	if !after.LastSeenAt.Equal(f.clock.Now()) {
		t.Fatal("heartbeat did not update last-seen")
	}
	after.LastSeenAt = before.LastSeenAt
	if !reflect.DeepEqual(before, after) {
		t.Fatal("heartbeat changed role, capabilities, lifecycle or incarnation")
	}
}

func TestRoleDecisionsAreExplicitSupportedAndStable(t *testing.T) {
	f := newFixture(t)
	id := identity("agent", "run")
	id.SupportedRoles = []agentprotocol.Role{agentprotocol.RolePlanner, agentprotocol.RoleWorker}
	v := admit(t, f, id)
	if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, ""); err == nil {
		t.Fatal("missing role decision accepted")
	}
	if err := f.registry.AssignRole(v.Ref(), "ADMIN", "Go decision"); err == nil {
		t.Fatal("unknown role accepted")
	}
	if err := f.registry.AssignRole(v.Ref(), agentprotocol.RolePlanner, "Go explicitly assigned Planner responsibility"); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, "Go explicitly reassigned role"); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.ClearRole(v.Ref(), "Go explicitly removed role"); err != nil {
		t.Fatal(err)
	}
	if get(t, f, v.Ref().SessionID).Session.AssignedRole != "" {
		t.Fatal("role removal did not persist")
	}
	worker := admit(t, f, identity("worker", "worker-run"))
	if err := f.registry.AssignRole(worker.Ref(), agentprotocol.RolePlanner, "Go decision"); err == nil {
		t.Fatal("unsupported Planner role accepted")
	}
	// Phase 5, not this registry, will enforce objective-level Planner selection.
	for _, agent := range []string{"planner-one", "planner-two"} {
		id := identity(agent, agent+"-run")
		id.SupportedRoles = []agentprotocol.Role{agentprotocol.RolePlanner}
		candidate := admit(t, f, id)
		if err := f.registry.AssignRole(candidate.Ref(), agentprotocol.RolePlanner, "Go assigned supported role"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLifecycleTransitionsAndDisconnectedTerminalState(t *testing.T) {
	for _, path := range [][]Status{{Busy, Ready, Busy, Blocked, Ready, Disconnected}, {Busy, Disconnected}, {Busy, Blocked, Disconnected}} {
		f := newFixture(t)
		v := admit(t, f, identity("agent", "run"))
		if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, "Go worker role"); err != nil {
			t.Fatal(err)
		}
		for _, next := range path {
			if err := f.registry.Transition(v.Ref(), next, "Go lifecycle decision"); err != nil {
				t.Fatalf("next %s: %v", next, err)
			}
			if get(t, f, v.Ref().SessionID).Session.Status != next {
				t.Fatal("transition not durable")
			}
		}
		before := rawState(t, f)
		if err := f.registry.Transition(v.Ref(), Ready, "revive"); err == nil {
			t.Fatal("disconnected session became READY")
		}
		if err := f.registry.Heartbeat(v.Ref()); !errors.Is(err, ErrFenced) {
			t.Fatal("disconnected heartbeat accepted")
		}
		if !bytes.Equal(before, rawState(t, f)) {
			t.Fatal("invalid revival modified state")
		}
	}
}

func TestInvalidLifecycleAndBusyMutationsFailClosed(t *testing.T) {
	f := newFixture(t)
	v := admit(t, f, identity("agent", "run"))
	if err := f.registry.Transition(v.Ref(), Busy, "Go start"); err == nil {
		t.Fatal("unassigned session became BUSY")
	}
	for _, next := range []Status{Ready, Blocked, "COMPLETE", ""} {
		if err := f.registry.Transition(v.Ref(), next, "Go decision"); err == nil {
			t.Fatalf("invalid READY -> %s accepted", next)
		}
	}
	if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, "Go role"); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Transition(v.Ref(), Busy, "Go start"); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.ClearRole(v.Ref(), "Go remove"); err == nil {
		t.Fatal("BUSY role changed")
	}
	if err := f.registry.Register(v.Ref(), identity("agent", "run")); err == nil {
		t.Fatal("BUSY advertisement replaced")
	}
	if err := f.registry.Transition(v.Ref(), Busy, "Go duplicate"); err == nil {
		t.Fatal("BUSY self-transition accepted")
	}
}

func TestAdvertisementCannotAssignOrWithdrawRoleOrRefreshLiveness(t *testing.T) {
	f := newFixture(t)
	v := admit(t, f, identity("agent", "run"))
	f.clock.Advance(time.Second)
	id := identity("agent", "run")
	id.Metadata.Provider = "PLANNER"
	id.SupportedRoles = []agentprotocol.Role{agentprotocol.RoleWorker, agentprotocol.RolePlanner}
	id.Capabilities = []agentprotocol.Capability{"new.example/capability:v9"}
	if err := f.registry.Register(v.Ref(), id); err != nil {
		t.Fatal(err)
	}
	current := get(t, f, v.Ref().SessionID)
	if current.Session.AssignedRole != "" || !current.Session.LastSeenAt.Equal(v.Session.LastSeenAt) {
		t.Fatal("advertisement granted role or refreshed liveness")
	}
	if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, "Go decision"); err != nil {
		t.Fatal(err)
	}
	id.SupportedRoles = []agentprotocol.Role{agentprotocol.RolePlanner}
	if err := f.registry.Register(v.Ref(), id); err == nil {
		t.Fatal("advertisement withdrew assigned role")
	}
	id.SessionID = "another-session"
	if err := f.registry.Register(v.Ref(), id); !errors.Is(err, ErrFenced) {
		t.Fatal("advertisement changed identity")
	}
}

func TestLivenessBoundaryExpiryLateHeartbeatAndFreshAdmission(t *testing.T) {
	for _, status := range []Status{Ready, Busy, Blocked} {
		t.Run(string(status), func(t *testing.T) {
			f := newFixture(t)
			v := admit(t, f, identity("agent", "run"))
			if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, "Go worker"); err != nil {
				t.Fatal(err)
			}
			if status != Ready {
				if err := f.registry.Transition(v.Ref(), Busy, "Go started"); err != nil {
					t.Fatal(err)
				}
			}
			if status == Blocked {
				if err := f.registry.Transition(v.Ref(), Blocked, "Go blocker"); err != nil {
					t.Fatal(err)
				}
			}
			f.clock.Advance(f.policy.StaleAfter)
			if !get(t, f, v.Ref().SessionID).Live {
				t.Fatal("exact threshold expired early")
			}
			f.clock.Advance(time.Nanosecond)
			// Even without a preceding query, a late heartbeat cannot resurrect.
			if err := f.registry.Heartbeat(v.Ref()); !errors.Is(err, ErrFenced) {
				t.Fatalf("late heartbeat: %v", err)
			}
			stopped := get(t, f, v.Ref().SessionID)
			if stopped.Live || stopped.Session.Status != Disconnected || stopped.Session.StateReason != "heartbeat_timeout" || !stopped.Session.LastSeenAt.Equal(v.Session.LastSeenAt) {
				t.Fatal("expiry did not preserve last heartbeat and terminal state")
			}
			candidates, err := f.registry.Eligible(agentprotocol.RoleWorker, "coding")
			if err != nil || len(candidates) != 0 {
				t.Fatal("expired session eligible")
			}
			ref := v.Ref()
			id := identity("agent", "fresh-run")
			fresh, err := f.registry.Enroll(Enrollment{Identity: id, Supersedes: &ref, Decision: "Go authorized fresh runtime"})
			if err != nil || !fresh.Live || fresh.Session.AssignedRole != "" {
				t.Fatalf("fresh admission: %v", err)
			}
		})
	}
}

func TestEligibilityRequiresCurrentLiveReadyAssignedRoleAndCapability(t *testing.T) {
	f := newFixture(t)
	v := admit(t, f, identity("agent", "run"))
	if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, "Go worker"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		role  agentprotocol.Role
		cap   agentprotocol.Capability
		count int
	}{{agentprotocol.RoleWorker, "coding", 1}, {agentprotocol.RoleWorker, "missing", 0}, {agentprotocol.RolePlanner, "coding", 0}} {
		got, err := f.registry.Eligible(tc.role, tc.cap)
		if err != nil || len(got) != tc.count {
			t.Fatalf("eligibility %v: %v", tc, err)
		}
	}
	if err := f.registry.Transition(v.Ref(), Busy, "Go start"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.registry.Eligible(agentprotocol.RoleWorker, "coding"); err != nil || len(got) != 0 {
		t.Fatal("BUSY session eligible")
	}
	if err := f.registry.Transition(v.Ref(), Blocked, "Go block"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.registry.Eligible(agentprotocol.RoleWorker, "coding"); err != nil || len(got) != 0 {
		t.Fatal("BLOCKED session eligible")
	}
	if err := f.registry.Transition(v.Ref(), Ready, "Go repair authorized"); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(f.policy.StaleAfter + time.Second)
	if got, err := f.registry.Eligible(agentprotocol.RoleWorker, "coding"); err != nil || len(got) != 0 {
		t.Fatal("stale READY session eligible")
	}
	if get(t, f, v.Ref().SessionID).Session.Status != Disconnected {
		t.Fatal("eligibility did not durably expire stale session")
	}
}

func TestPersistencePolicyClockAndReturnedValueIsolation(t *testing.T) {
	f := newFixture(t)
	v := admit(t, f, identity("agent", "run"))
	if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, "Go worker"); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Second)
	if err := f.registry.Heartbeat(v.Ref()); err != nil {
		t.Fatal(err)
	}
	before := get(t, f, v.Ref().SessionID)
	reopened, err := Open(f.root, f.policy, f.clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	after, err := reopened.Get(v.Ref().SessionID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("reopen lost state: %v", err)
	}
	after.Session.Identity.Capabilities[0] = "mutated-return"
	if get(t, f, v.Ref().SessionID).Session.Identity.Capabilities[0] != "coding" {
		t.Fatal("returned view mutated registry")
	}
	if _, err := Open(f.root, Policy{}, f.clock.Now); err == nil {
		t.Fatal("zero liveness policy accepted")
	}
	if _, err := Open(f.root, Policy{StaleAfter: time.Hour}, f.clock.Now); err == nil {
		t.Fatal("reopen silently replaced policy")
	}
	f.clock.Advance(-2 * time.Second)
	if _, err := reopened.List(); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("clock rollback accepted: %v", err)
	}
	if err := reopened.Heartbeat(v.Ref()); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("heartbeat rolled time backwards: %v", err)
	}
}

func TestRegistryCannotChangeSchedulerOwnershipOrUseManagedSession(t *testing.T) {
	f := newFixture(t)
	p := objective.Plan{ID: "objective", Objective: "fixture", Tasks: []objective.Task{{ID: "task", Feature: "fixture", Scope: "fixture"}}}
	a, err := scheduler.Acquire(scheduler.Request{Workspace: f.root, Agent: "managed-worker", Role: "coding", Base: "main", Plan: p})
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, f)
	if _, err := f.registry.Enroll(Enrollment{Identity: identity("agent", a.SessionID), Decision: "Go decision"}); err == nil {
		t.Fatal("registry duplicated managed session authority")
	}
	v := admit(t, f, identity("agent", "universal-session"))
	if v.Session.Generation != 1 || uint64(v.Session.Generation) == a.AssignmentGeneration || reflect.TypeOf(v.Session.Generation) == reflect.TypeOf(a.AssignmentGeneration) {
		t.Fatal("registry incarnation confused with assignment generation")
	}
	if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, "Go worker"); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Second)
	if err := f.registry.Heartbeat(v.Ref()); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(f.policy.StaleAfter + time.Nanosecond)
	if _, err := f.registry.Eligible(agentprotocol.RoleWorker, "coding"); err != nil {
		t.Fatal(err)
	}
	if get(t, f, v.Ref().SessionID).Session.Status != Disconnected {
		t.Fatal("registry session did not expire")
	}
	after := snapshot(t, f)
	if !reflect.DeepEqual(before.Tasks, after.Tasks) || !reflect.DeepEqual(before.Claims, after.Claims) || !reflect.DeepEqual(before.Sessions, after.Sessions) {
		t.Fatal("registry changed task/claim/managed-session ownership")
	}
	if err := scheduler.Verify(f.root, p.ID, a); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agentmeeting.md", "resolution.md", "coordination.md"} {
		data, err := os.ReadFile(filepath.Join(f.root, "multi-agent-orchestrator", name))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("universal-session")) {
			t.Fatal("registry secrets/state leaked into Markdown")
		}
	}
}

func TestMalformedRegistryFailsClosedWithoutOverwrite(t *testing.T) {
	changes := map[string]func(*state.Snapshot){
		"foreign repository":  func(s *state.Snapshot) { s.Authority.RepositoryID = "another-repository" },
		"missing registry":    func(s *state.Snapshot) { s.AgentRegistry = nil },
		"missing session map": func(s *state.Snapshot) { s.AgentRegistry.Sessions = nil },
		"lost history":        func(s *state.Snapshot) { s.AgentRegistry.Sessions = map[agentprotocol.SessionID]state.AgentSession{} },
		"invalid policy":      func(s *state.Snapshot) { s.AgentRegistry.StaleAfterNS = 0 },
		"future schema":       func(s *state.Snapshot) { s.Authority.Schema++ },
		"zero counter":        func(s *state.Snapshot) { s.AgentRegistry.LastGeneration = 0 },
		"wrong map key": func(s *state.Snapshot) {
			v := s.AgentRegistry.Sessions["run"]
			delete(s.AgentRegistry.Sessions, "run")
			s.AgentRegistry.Sessions["wrong"] = v
		},
		"zero generation": func(s *state.Snapshot) {
			v := s.AgentRegistry.Sessions["run"]
			v.Generation = 0
			s.AgentRegistry.Sessions["run"] = v
		},
		"implicit role": func(s *state.Snapshot) {
			v := s.AgentRegistry.Sessions["run"]
			v.AssignedRole = agentprotocol.RolePlanner
			s.AgentRegistry.Sessions["run"] = v
		},
		"future heartbeat": func(s *state.Snapshot) {
			v := s.AgentRegistry.Sessions["run"]
			v.LastSeenAt = s.AgentRegistry.ObservedAt.Add(time.Hour)
			s.AgentRegistry.Sessions["run"] = v
		},
		"unknown status": func(s *state.Snapshot) {
			v := s.AgentRegistry.Sessions["run"]
			v.Status = "COMPLETE"
			s.AgentRegistry.Sessions["run"] = v
		},
		"bad terminal state": func(s *state.Snapshot) {
			v := s.AgentRegistry.Sessions["run"]
			v.Status = Disconnected
			s.AgentRegistry.Sessions["run"] = v
		},
		"bad supersession": func(s *state.Snapshot) {
			v := s.AgentRegistry.Sessions["run"]
			v.SupersededBy = "unknown"
			s.AgentRegistry.Sessions["run"] = v
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			v := admit(t, f, identity("agent", "run"))
			s := snapshot(t, f)
			change(&s)
			data, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.root, "multi-agent-orchestrator", "state.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := f.registry.Heartbeat(v.Ref()); err == nil {
				t.Fatal("corrupt state accepted")
			}
			if _, err := Open(f.root, f.policy, f.clock.Now); err == nil {
				t.Fatal("corrupt state reopened")
			}
			if !bytes.Equal(data, rawState(t, f)) {
				t.Fatal("corrupt state was overwritten")
			}
		})
	}
	for _, corrupt := range []func(string) string{
		func(s string) string { return s[:len(s)/2] },
		func(s string) string { return s + " {}" },
		func(s string) string {
			return strings.Replace(s, `"agent_registry":`, `"unknown_authority":true,"agent_registry":`, 1)
		},
		func(s string) string {
			return strings.Replace(s, `"session_generation": 1`, `"session_generation":1,"session_generation":2`, 1)
		},
	} {
		f := newFixture(t)
		admit(t, f, identity("agent", "run"))
		data := []byte(corrupt(string(rawState(t, f))))
		if err := os.WriteFile(filepath.Join(f.root, "multi-agent-orchestrator", "state.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := f.registry.List(); err == nil {
			t.Fatal("malformed/ambiguous JSON accepted")
		}
		if !bytes.Equal(data, rawState(t, f)) {
			t.Fatal("malformed JSON overwritten")
		}
	}
}

func TestConcurrentEnrollHeartbeatsReadsAndSessionReplacement(t *testing.T) {
	f := newFixture(t)
	other, err := Open(f.root, f.policy, f.clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r := f.registry
			if i%2 == 0 {
				r = other
			}
			v, err := r.Enroll(Enrollment{Identity: identity(fmt.Sprintf("agent-%d", i), fmt.Sprintf("session-%d", i)), Decision: "concurrent Go admission"})
			if err == nil {
				err = r.Heartbeat(v.Ref())
			}
			errs <- err
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, err := other.List(); errs <- err }()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	all, err := f.registry.List()
	if err != nil || len(all) != 8 {
		t.Fatalf("lost concurrent enrollments: %v", err)
	}
	for i, v := range all {
		if uint64(v.Session.Generation) != uint64(i+1) {
			t.Fatal("duplicate/lost generation")
		}
	}
	old := all[0]
	ref := old.Ref()
	id := old.Session.Identity
	id.SessionID = "replacement"
	start = make(chan struct{})
	errs = make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := other.Enroll(Enrollment{Identity: id, Supersedes: &ref, Decision: "Go replaced runtime"})
		errs <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		err := f.registry.Heartbeat(ref)
		if errors.Is(err, ErrFenced) {
			err = nil
		}
		errs <- err
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := f.registry.Heartbeat(ref); !errors.Is(err, ErrFenced) {
		t.Fatal("old heartbeat remained authoritative after replacement")
	}
	latest, err := other.Get("replacement")
	if err != nil || !latest.Current || !latest.Live {
		t.Fatalf("replacement lost race: %v", err)
	}
}

func TestConcurrentSupersessionHasOneWinner(t *testing.T) {
	f := newFixture(t)
	old := admit(t, f, identity("agent", "old"))
	ref := old.Ref()
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, name := range []string{"next-one", "next-two"} {
		go func(name string) {
			<-start
			_, err := f.registry.Enroll(Enrollment{Identity: identity("agent", name), Supersedes: &ref, Decision: "Go replacement"})
			results <- err
		}(name)
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if !errors.Is(err, ErrFenced) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("replacement CAS admitted multiple winners")
	}
	all, err := f.registry.List()
	if err != nil || len(all) != 2 {
		t.Fatal("replacement history corrupted")
	}
}

func TestConcurrentHeartbeatAndAdvertisementDoNotLoseFields(t *testing.T) {
	f := newFixture(t)
	v := admit(t, f, identity("agent", "run"))
	if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, "Go assigned worker"); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Second)
	advertisement := v.Session.Identity
	advertisement.Metadata.RuntimeVersion = "new-version"
	advertisement.Capabilities = []agentprotocol.Capability{"coding", "new/capability"}
	start := make(chan struct{})
	errs := make(chan error, 3)
	go func() { <-start; errs <- f.registry.Heartbeat(v.Ref()) }()
	go func() { <-start; errs <- f.registry.Register(v.Ref(), advertisement) }()
	go func() { <-start; _, err := f.registry.List(); errs <- err }()
	close(start)
	for i := 0; i < 3; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	after := get(t, f, v.Ref().SessionID)
	if !after.Session.LastSeenAt.Equal(f.clock.Now()) || after.Session.Identity.Metadata.RuntimeVersion != "new-version" || after.Session.AssignedRole != agentprotocol.RoleWorker || after.Ref() != v.Ref() {
		t.Fatal("concurrent field updates lost data or changed authority")
	}
}

func TestCorruptIncarnationHistoryCannotRestoreOldAuthority(t *testing.T) {
	for _, mutate := range []func(*state.AgentRegistryState){
		func(data *state.AgentRegistryState) {
			old := data.Sessions["old"]
			old.SupersededBy = "old"
			data.Sessions["old"] = old
		},
		func(data *state.AgentRegistryState) {
			old := data.Sessions["old"]
			old.SupersededBy = ""
			data.Sessions["old"] = old
		},
		func(data *state.AgentRegistryState) {
			next := data.Sessions["new"]
			next.Generation = 1
			data.Sessions["new"] = next
		},
	} {
		f := newFixture(t)
		old := admit(t, f, identity("agent", "old"))
		ref := old.Ref()
		if _, err := f.registry.Enroll(Enrollment{Identity: identity("agent", "new"), Supersedes: &ref, Decision: "Go replacement"}); err != nil {
			t.Fatal(err)
		}
		s := snapshot(t, f)
		mutate(s.AgentRegistry)
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.root, "multi-agent-orchestrator", "state.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := f.registry.Heartbeat(ref); err == nil {
			t.Fatal("corrupt history restored old authority")
		}
		if _, err := f.registry.List(); err == nil {
			t.Fatal("corrupt history accepted")
		}
	}
}

func TestAuthorityRevocationAndManagedWriterCollisionFailClosed(t *testing.T) {
	f := newFixture(t)
	v := admit(t, f, identity("agent", "universal-session"))
	repoID, err := repository.Identity(f.root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(f.root, repoID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update("test", "attempt-managed-id-collision", func(s *state.Snapshot) error {
		s.Sessions[string(v.Ref().SessionID)] = state.Session{ID: string(v.Ref().SessionID), State: "ACTIVE"}
		return nil
	}); err == nil {
		t.Fatal("managed writer created competing session authority")
	}
	if err := store.Update("test", "revoke", func(s *state.Snapshot) error { s.Authority.Enrollment = "BLOCKED"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Heartbeat(v.Ref()); err == nil {
		t.Fatal("revoked repository accepted heartbeat")
	}
	if _, err := f.registry.Eligible(agentprotocol.RoleWorker, "coding"); err == nil {
		t.Fatal("revoked authority exposed eligible work")
	}
}
