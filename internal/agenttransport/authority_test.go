package agenttransport

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
)

func TestMessagesDoNotApplyAuthoritativeState(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v %s", err, out)
		}
	}
	if _, err := enrollment.Enroll(root, enrollment.Native, "test human authorization"); err != nil {
		t.Fatal(err)
	}
	registry, err := agentregistry.Open(root, agentregistry.Policy{StaleAfter: time.Hour}, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := registry.Enroll(agentregistry.Enrollment{Identity: *advertisement().Identity, Decision: "trusted test admission"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "multi-agent-orchestrator", "state.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a, o := memoryPair(t, 2)
	ctx := context.Background()
	// Receiving advertisements/heartbeats is not enrollment or registry heartbeat.
	for _, e := range []agentprotocol.Envelope{advertisement(), heartbeat()} {
		if err := a.Send(ctx, e); err != nil {
			t.Fatal(err)
		}
		if _, err := o.Receive(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// Even Go-origin role/assignment messages are only messages, not registry
	// decisions or scheduler grants. An unbound endpoint may carry any syntactically
	// valid session reference; Phase 4 must authorize and bind it before handling.
	role := heartbeat()
	role.Type, role.Role = agentprotocol.RoleAssignment, agentprotocol.RolePlanner
	for _, e := range []agentprotocol.Envelope{role, assignment()} {
		if err := o.Send(ctx, e); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Receive(ctx); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("transport changed authoritative role, incarnation, generation, ownership or liveness")
	}
	current, err := registry.Get(v.Ref().SessionID)
	if err != nil || current.Ref() != v.Ref() || current.Session.AssignedRole != "" {
		t.Fatalf("transport changed registry identity/role: %v", err)
	}
}
