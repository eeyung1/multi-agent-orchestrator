package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/review"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

func TestCrossResourceRecoveryConditions(t *testing.T) {
	for _, mode := range []string{"reservation", "operational-blocked", "validated-complete", "retired-recovery"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			p, as := f.acquire(t)
			a := as[0]
			s, err := f.store.Read()
			if err != nil {
				t.Fatal(err)
			}
			task := s.Tasks[p.ID+"::"+a.Task.ID]
			want := ""
			switch mode {
			case "reservation":
				if err := f.store.Update("test human", "reservation-fixture", func(s *state.Snapshot) error {
					task.SessionID, task.Branch, task.Worktree = "", "", ""
					s.Tasks[p.ID+"::"+task.ID] = task
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				want = "CROSS_RESOURCE_RECONCILIATION"
			case "operational-blocked":
				if err := coordination.BlockOwnedSession(f.root, task); err != nil {
					t.Fatal(err)
				}
				want = "BLOCK_PUBLICATION_PENDING"
			case "validated-complete":
				if err := os.MkdirAll(filepath.Join(a.Worktree, a.Task.Scope), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(a.Worktree, a.Task.Scope, "done"), []byte("done\n"), 0644); err != nil {
					t.Fatal(err)
				}
				git(t, a.Worktree, "add", "--", a.Task.Scope)
				git(t, a.Worktree, "commit", "-m", "Checkpoint")
				in := review.ResultInput{Workspace: f.root, ObjectiveID: p.ID, TaskID: task.ID, SessionID: a.SessionID, Token: a.Token, Branch: a.Branch, Worktree: a.Worktree, AssignmentGeneration: a.AssignmentGeneration, ValidatedHead: git(t, a.Worktree, "rev-parse", "HEAD"), Validation: "trusted checkpoint PASS"}
				if err := review.RecordValidation(in); err != nil {
					t.Fatal(err)
				}
				if err := coordination.CompleteSession(f.root, a.SessionID, a.Token, func() error { return nil }); err != nil {
					t.Fatal(err)
				}
				want = "FINALIZATION_REQUIRED"
			case "retired-recovery":
				if err := scheduler.Block(f.root, p.ID, a, "controlled failure"); err != nil {
					t.Fatal(err)
				}
				s, err = f.store.Read()
				if err != nil {
					t.Fatal(err)
				}
				task = s.Tasks[p.ID+"::"+task.ID]
				if err := coordination.ReconcileBlockedAssignment(f.root, task, "human reviewed old writer"); err != nil {
					t.Fatal(err)
				}
				want = "TASK_BLOCKED"
			}
			before := data(t, f.path)
			v := capture(t, f.root, Options{})
			if !code(v, want) || !v.Tasks[0].HumanAction {
				t.Fatalf("%s recovery invisible: %+v", mode, v)
			}
			if mode == "validated-complete" && v.Tasks[0].Recovery != "FINALIZATION_REQUIRED" {
				t.Fatal("validated checkpoint misclassified as lost claim")
			}
			if mode == "retired-recovery" && v.Tasks[0].Recovery != "EXPLICIT_RECOVERY_AVAILABLE" {
				t.Fatal("idempotent retirement retry unavailable")
			}
			if !bytes.Equal(before, data(t, f.path)) {
				t.Fatal("observation reconciled authority")
			}
		})
	}
}

func TestDisplayConfigurationAndLargeGenerationRemainPresentationOnly(t *testing.T) {
	f := setup(t)
	p, as := f.acquire(t)
	const generation = uint64(9007199254740993)
	if err := f.store.Update("test human", "large-generation-fixture", func(s *state.Snapshot) error {
		task := s.Tasks[p.ID+"::"+as[0].Task.ID]
		task.AssignmentGeneration = generation
		s.Tasks[p.ID+"::"+task.ID] = task
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(config, []byte(`{"max_workers":2,"planner":{"Authorization":"Bearer configuration-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPERATIONS_TEST_SECRET", "environment-secret-not-for-display")
	before := data(t, f.path)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"status", "--workspace", f.root, "--config", config, "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var v Snapshot
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.MaxWorkers == nil || *v.MaxWorkers != 2 || v.ConfigurationFrom != "provided_config_not_authority_verified" || v.Tasks[0].Assignment.Generation != generation {
		t.Fatal("display policy or generation was altered")
	}
	for _, secret := range []string{"configuration-secret", "environment-secret-not-for-display"} {
		if bytes.Contains(out.Bytes(), []byte(secret)) {
			t.Fatal("configuration/environment leaked")
		}
	}
	if !bytes.Equal(before, data(t, f.path)) {
		t.Fatal("display configuration changed policy authority")
	}
}

func TestCorruptLedgerAndUnavailableClockFailSafely(t *testing.T) {
	f := setup(t)
	f.agent(t, "clock-worker", "clock-session", agentprotocol.RoleWorker)
	f.acquire(t)
	path := filepath.Join(f.root, "multi-agent-orchestrator", "agentmeeting.md")
	original := data(t, path)
	if err := os.WriteFile(path, append(original, []byte("\n| malformed | ambiguous | row |\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	r, err := Open(f.root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Capture(context.Background()); !errors.Is(err, ErrAuthority) {
		t.Fatal("corrupt ledger presented as trustworthy", err)
	}
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}
	before := data(t, f.path)
	v := capture(t, f.root, Options{Now: func() time.Time { return time.Now().Add(-time.Hour) }})
	if !code(v, "CLOCK_REGRESSION") || v.Agents[0].Live || v.Agents[0].Status != state.AgentReady || !bytes.Equal(before, data(t, f.path)) {
		t.Fatal("clock regression invented eligibility or changed lifecycle")
	}
}
