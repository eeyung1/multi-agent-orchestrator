package scheduler

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

func recoveryFixture(t *testing.T) (string, objective.Plan, Result, Result, *state.Store) {
	t.Helper()
	d := initRepo(t)
	p := objective.Plan{ID: "obj", Objective: "ship", Tasks: []objective.Task{
		{ID: "a", Feature: "a", Scope: "backend"},
		{ID: "b", Feature: "b", Scope: "frontend"},
	}}
	a, err := Acquire(Request{Workspace: d, Agent: "one", Role: "coding", Base: "main", Plan: p, TaskID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Acquire(Request{Workspace: d, Agent: "two", Role: "coding", Base: "main", Plan: p, TaskID: "b"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := repository.Identity(d)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(d, id)
	if err != nil {
		t.Fatal(err)
	}
	return d, p, a, b, store
}

// Only temporary test repositories synthesize the historical split. Runtime
// callers must go through Block, which now updates both state layers.
func historicalBlock(t *testing.T, store *state.Store) state.Task {
	t.Helper()
	if err := store.Update("test fixture", "historical-split", func(s *state.Snapshot) error {
		task := s.Tasks["obj::a"]
		task.Status = "BLOCKED"
		task.Blocker = "post-validation review failed"
		s.Tasks["obj::a"] = task
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	return s.Tasks["obj::a"]
}

func TestAuthorizedRecoveryReconcilesExactBlockedOwnership(t *testing.T) {
	for _, operational := range []string{"BLOCKED", "WORKING"} {
		t.Run(operational, func(t *testing.T) {
			d, p, a, unrelated, store := recoveryFixture(t)
			if err := Block(d, p.ID, a, "first failure"); err != nil {
				t.Fatal(err)
			}
			if operational == "WORKING" {
				// Exercise the reported attempt-2 historical shape exactly.
				if err := AuthorizeRecovery(d, p.ID, "a", a.AssignmentGeneration, "test human"); err != nil {
					t.Fatal(err)
				}
				var err error
				a, err = Acquire(Request{Workspace: d, Agent: "retry", Role: "coding", Base: "main", Plan: p, TaskID: "a"})
				if err != nil || a.Attempt != 2 {
					t.Fatalf("attempt-2 fixture failed: %+v %v", a, err)
				}
				historicalBlock(t, store)
				if err := coordination.RetireBlockedSession(d, a.SessionID, a.Token); err == nil {
					t.Fatal("ordinary retirement accepted a WORKING session")
				}
			}
			if err := coordination.ClaimScope(d, coordination.Claim{SessionID: a.SessionID, Token: "extra", Feature: "extra", Scope: "extra"}); err != nil {
				t.Fatal(err)
			}
			before, _ := store.Read()
			old := before.Tasks["obj::a"]
			if err := AuthorizeRecovery(d, p.ID, "a", a.AssignmentGeneration, "explicit human recovery"); err != nil {
				t.Fatal(err)
			}
			after, err := store.Read()
			if err != nil {
				t.Fatal(err)
			}
			current := after.Tasks["obj::a"]
			if current.Status != "PENDING" || current.Token != "" || current.SessionID != "" || len(current.AssignmentHistory) != len(old.AssignmentHistory)+1 {
				t.Fatalf("recovery did not preserve history and reset ownership: %+v", current)
			}
			want := state.AssignmentHistory{Attempt: old.Attempt, AssignmentGeneration: old.AssignmentGeneration, Token: old.Token, SessionID: old.SessionID, Branch: old.Branch, Worktree: old.Worktree, BaseSHA: old.BaseSHA, Blocker: old.Blocker}
			if got := current.AssignmentHistory[len(current.AssignmentHistory)-1]; !reflect.DeepEqual(got, want) {
				t.Fatalf("historical assignment changed: %+v; expected %+v", got, want)
			}
			if status, err := coordination.SessionStatus(d, a.SessionID); err != nil || status != "HANDOFF" {
				t.Fatalf("session not retired: %s %v", status, err)
			}
			claims, err := coordination.Claims(d)
			if err != nil || len(claims) != 1 || claims[0].SessionID != unrelated.SessionID || claims[0].Token != unrelated.Token {
				t.Fatalf("retirement did not remove exactly all session claims: %+v %v", claims, err)
			}
			if status, err := coordination.SessionStatus(d, unrelated.SessionID); err != nil || status != "WORKING" {
				t.Fatal("unrelated working session was retired")
			}
			if _, err := os.Stat(a.Worktree); err != nil {
				t.Fatal("historical worktree was removed")
			}
			last := after.Events[len(after.Events)-1]
			if last.Actor != "explicit human recovery" || last.Operation != "authorize-recovery" {
				t.Fatalf("human authorization was not audited: %+v", last)
			}
			if err := AuthorizeRecovery(d, p.ID, "a", a.AssignmentGeneration, "retry human"); err == nil {
				t.Fatal("completed recovery was applied twice")
			}
			if err := Complete(d, p.ID, "a", a.Token, a.SessionID, a.AssignmentGeneration); err == nil {
				t.Fatal("old completion bypassed the recovery fence")
			}
			fresh, err := Acquire(Request{Workspace: d, Agent: "fresh", Role: "coding", Base: "main", Plan: p, TaskID: "a"})
			if err != nil {
				t.Fatal(err)
			}
			if fresh.AssignmentGeneration <= a.AssignmentGeneration || fresh.Token == a.Token || fresh.Worktree == a.Worktree || fresh.SessionID == a.SessionID {
				t.Fatal("new assignment reused old ownership")
			}
			if err := AuthorizeRecovery(d, p.ID, "a", a.AssignmentGeneration, "stale human"); err == nil {
				t.Fatal("old authorization retired the fresh active assignment")
			}
			if err := Complete(d, p.ID, "a", a.Token, a.SessionID, a.AssignmentGeneration); err == nil {
				t.Fatal("stale completion affected the fresh assignment")
			}
		})
	}
}

func TestRecoveryReconciliationRejectsMismatchedOwnership(t *testing.T) {
	for _, mode := range []string{"token", "session", "branch", "worktree", "scope", "task-identity", "objective-identity", "generation", "active", "newer-active", "newer-blocked", "other-active-owner", "transferred-token", "no-human", "malformed-claims"} {
		t.Run(mode, func(t *testing.T) {
			d, _, a, other, store := recoveryFixture(t)
			expected := historicalBlock(t, store)
			if err := store.Update("test fixture", "mismatch", func(s *state.Snapshot) error {
				task := s.Tasks["obj::a"]
				switch mode {
				case "token":
					task.Token = "wrong-token"
				case "session":
					task.SessionID = other.SessionID
				case "branch":
					task.Branch = "agent/other"
				case "worktree":
					task.Worktree = other.Worktree
				case "scope":
					task.Scope = "wrong-scope"
				case "task-identity":
					task.ID = "different"
				case "objective-identity":
					task.ObjectiveID = "different"
				case "active":
					task.Status = "ASSIGNED"
				case "newer-active", "newer-blocked":
					task.AssignmentGeneration++
					if mode == "newer-active" {
						task.Status = "ASSIGNED"
					}
				case "other-active-owner":
					peer := s.Tasks["obj::b"]
					peer.Worktree = task.Worktree
					s.Tasks["obj::b"] = peer
				}
				s.Tasks["obj::a"] = task
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if mode == "transferred-token" {
				if err := coordination.Handoff(d, a.SessionID, a.Token, other.SessionID); err != nil {
					t.Fatal(err)
				}
			}
			ledgerPath := filepath.Join(d, "multi-agent-orchestrator", "agentmeeting.md")
			if mode == "malformed-claims" {
				f, err := os.OpenFile(ledgerPath, os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString("| malformed | claim |\n")
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(ledgerPath)
			snapshot, _ := store.Read()
			generation, human := a.AssignmentGeneration, "explicit human"
			if mode == "generation" {
				generation++
			}
			if mode == "no-human" {
				human = ""
			}
			if err := AuthorizeRecovery(d, "obj", "a", generation, human); err == nil {
				t.Fatal("mismatched recovery was accepted")
			}
			// Also fence callers holding a stale structured snapshot, even when
			// the operational session is still WORKING with its original token.
			if mode == "newer-active" || mode == "newer-blocked" || mode == "active" || mode == "no-human" {
				if err := coordination.ReconcileBlockedAssignment(d, expected, human); err == nil {
					t.Fatal("direct reconciliation bypassed the structured fence")
				}
			}
			after, _ := os.ReadFile(ledgerPath)
			current, _ := store.Read()
			if string(before) != string(after) || !reflect.DeepEqual(snapshot, current) {
				t.Fatal("rejected reconciliation modified ownership or history")
			}
		})
	}
}

func TestRecoveryRetryAfterOperationalRetirement(t *testing.T) {
	d, _, a, _, store := recoveryFixture(t)
	expected := historicalBlock(t, store)
	if err := coordination.ReconcileBlockedAssignment(d, expected, "first human authorization"); err != nil {
		t.Fatal(err)
	}
	// Simulate interruption before the scheduler's PENDING write. The structured
	// assignment still identifies the retired session, and retries must be safe.
	if err := coordination.ReconcileBlockedAssignment(d, expected, "retry human authorization"); err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeRecovery(d, "obj", "a", a.AssignmentGeneration, "retry human authorization"); err != nil {
		t.Fatal(err)
	}
	s, _ := store.Read()
	if len(s.Tasks["obj::a"].AssignmentHistory) != 1 {
		t.Fatal("retry duplicated or lost the historical assignment")
	}
	if err := coordination.ClaimScope(d, coordination.Claim{SessionID: a.SessionID, Token: "reappeared", Feature: "extra", Scope: "extra"}); err != nil {
		t.Fatal(err)
	}
	if err := coordination.RetireBlockedSession(d, a.SessionID, a.Token); err == nil {
		t.Fatal("HANDOFF with newly appearing claims was accepted as retired")
	}
}

func TestBlockCoordinatesExactOperationalOwnership(t *testing.T) {
	for _, mode := range []string{"working", "already-blocked", "generation", "token", "session", "branch", "worktree", "transferred-token", "locked-ledger"} {
		t.Run(mode, func(t *testing.T) {
			d, _, assignment, other, store := recoveryFixture(t)
			a := assignment
			switch mode {
			case "already-blocked":
				if err := coordination.Transition(d, a.SessionID, "BLOCKED"); err != nil {
					t.Fatal(err)
				}
			case "generation":
				a.AssignmentGeneration++
			case "token":
				a.Token = "wrong-token"
			case "session":
				a.SessionID = other.SessionID
			case "branch":
				a.Branch = other.Branch
			case "worktree":
				a.Worktree = other.Worktree
			case "transferred-token":
				if err := coordination.Handoff(d, a.SessionID, a.Token, other.SessionID); err != nil {
					t.Fatal(err)
				}
			case "locked-ledger":
				lock := filepath.Join(d, ".agent-coordination-lock")
				if err := os.Mkdir(lock, 0700); err != nil {
					t.Fatal(err)
				}
				defer os.Remove(lock)
			}
			path := filepath.Join(d, "multi-agent-orchestrator", "agentmeeting.md")
			before, _ := os.ReadFile(path)
			snapshot, _ := store.Read()
			err := Block(d, "obj", a, "post-worker review failed")
			current, _ := store.Read()
			if mode == "working" || mode == "already-blocked" {
				if err != nil || current.Tasks["obj::a"].Status != "BLOCKED" {
					t.Fatalf("blocking failed: %+v %v", current.Tasks["obj::a"], err)
				}
				status, err := coordination.SessionStatus(d, a.SessionID)
				if err != nil || status != "BLOCKED" {
					t.Fatalf("structured BLOCKED retained executable WORKING ownership: %s %v", status, err)
				}
				claims, err := coordination.Claims(d)
				if err != nil || len(claims) != 2 {
					t.Fatalf("blocking released ownership: %+v %v", claims, err)
				}
			} else {
				after, _ := os.ReadFile(path)
				if err == nil || !reflect.DeepEqual(snapshot, current) || string(before) != string(after) {
					t.Fatal("failed/stale blocking altered assignment or operational ownership")
				}
			}
		})
	}
}
