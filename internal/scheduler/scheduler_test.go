package scheduler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
)

func initRepo(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = d
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(d, "seed"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "seed")
	if _, err := enrollment.Enroll(d, enrollment.Native, "test"); err != nil {
		t.Fatal(err)
	}
	return d
}
func TestAcquirePersistsAndPreventsDuplicateTask(t *testing.T) {
	d := initRepo(t)
	p := objective.Plan{ID: "obj", Objective: "ship", Tasks: []objective.Task{{ID: "a", Feature: "a", Scope: "backend", Priority: 1}}}
	first, err := Acquire(Request{Workspace: d, Agent: "one", Role: "coding-agent", Base: "main", Plan: p})
	if err != nil {
		t.Fatal(err)
	}
	if first.Attempt != 1 || first.AssignmentGeneration == 0 || first.Token == "" {
		t.Fatalf("bad assignment: %+v", first)
	}
	if _, err := Acquire(Request{Workspace: d, Agent: "two", Role: "coding-agent", Base: "main", Plan: p}); err == nil {
		t.Fatal("expected duplicate acquisition rejection")
	}
}
func TestCompleteRejectsStaleGeneration(t *testing.T) {
	d := initRepo(t)
	p := objective.Plan{ID: "obj", Objective: "ship", Tasks: []objective.Task{{ID: "a", Feature: "a", Scope: "backend"}}}
	r, err := Acquire(Request{Workspace: d, Agent: "one", Role: "coding-agent", Base: "main", Plan: p})
	if err != nil {
		t.Fatal(err)
	}
	if err := Complete(d, "obj", "a", r.Token, r.SessionID, r.AssignmentGeneration+1); err == nil {
		t.Fatal("expected stale generation rejection")
	}
	if err := Complete(d, "obj", "a", r.Token, r.SessionID, r.AssignmentGeneration); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeRecoveryFencesBlockedAssignmentAndReacquires(t *testing.T) {
	d := initRepo(t)
	p := objective.Plan{ID: "obj", Objective: "ship", Tasks: []objective.Task{{ID: "a", Feature: "a", Scope: "backend"}}}
	first, err := Acquire(Request{Workspace: d, Agent: "one", Role: "coding-agent", Base: "main", Plan: p})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordination.Transition(d, first.SessionID, "BLOCKED"); err != nil {
		t.Fatal(err)
	}
	if err := Block(d, "obj", first, "validation failed"); err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeRecovery(d, "obj", "a", first.AssignmentGeneration+1, "test human"); err == nil {
		t.Fatal("expected stale recovery authorization rejection")
	}
	if err := AuthorizeRecovery(d, "obj", "a", first.AssignmentGeneration, "test human"); err != nil {
		t.Fatal(err)
	}
	snap, err := Read(d)
	if err != nil {
		t.Fatal(err)
	}
	task := snap.Tasks[taskKey("obj", "a")]
	if task.Status != "PENDING" || task.Token != "" || task.SessionID != "" || len(task.AssignmentHistory) != 1 {
		t.Fatalf("bad recovered task: %+v", task)
	}
	history := task.AssignmentHistory[0]
	if history.AssignmentGeneration != first.AssignmentGeneration || history.Token != first.Token ||
		history.SessionID != first.SessionID || history.Worktree != first.Worktree || history.Blocker != "validation failed" {
		t.Fatalf("recovery history lost assignment evidence: %+v", history)
	}
	if _, err := os.Stat(first.Worktree); err != nil {
		t.Fatalf("old worktree was not preserved: %v", err)
	}
	if err := Complete(d, "obj", "a", first.Token, first.SessionID, first.AssignmentGeneration); err == nil {
		t.Fatal("stale completion from recovered assignment was accepted")
	}
	second, err := Acquire(Request{Workspace: d, Agent: "two", Role: "coding-agent", Base: "main", Plan: p})
	if err != nil {
		t.Fatal(err)
	}
	if second.Attempt != 2 || second.AssignmentGeneration <= first.AssignmentGeneration ||
		second.Token == first.Token || second.SessionID == first.SessionID || second.Worktree == first.Worktree {
		t.Fatalf("recovery did not create a fresh fenced assignment: first=%+v second=%+v", first, second)
	}
}

func TestAuthorizeRecoveryRejectsNonBlockedTask(t *testing.T) {
	d := initRepo(t)
	p := objective.Plan{ID: "obj", Objective: "ship", Tasks: []objective.Task{{ID: "a", Feature: "a", Scope: "backend"}}}
	r, err := Acquire(Request{Workspace: d, Agent: "one", Role: "coding-agent", Base: "main", Plan: p})
	if err != nil {
		t.Fatal(err)
	}
	if err := AuthorizeRecovery(d, "obj", "a", r.AssignmentGeneration, "test human"); err == nil {
		t.Fatal("expected ASSIGNED task recovery rejection")
	}
}

func TestEncodeDefinitionPreservesCallerTaskOrder(t *testing.T) {
	tasks := []objective.Task{
		{ID: "task-z", Feature: "z", Scope: "backend/z"},
		{ID: "task-m", Feature: "m", Scope: "backend/m"},
		{ID: "task-a", Feature: "a", Scope: "backend/a"},
	}
	p := objective.Plan{
		ID:        "test-order-preservation",
		Objective: "Demonstrate non-mutating plan encoding",
		Tasks:     tasks,
	}

	initialIDs := make([]string, len(p.Tasks))
	for i, task := range p.Tasks {
		initialIDs[i] = task.ID
	}

	encoded := EncodeDefinition(p)
	if len(encoded) == 0 {
		t.Fatal("expected non-empty encoded definition")
	}

	// Verify caller's slice was not mutated in place
	for i, task := range p.Tasks {
		if task.ID != initialIDs[i] {
			t.Fatalf("task %d was mutated in caller slice: expected %s, got %s", i, initialIDs[i], task.ID)
		}
	}

	// Verify determinism: plan with tasks in different order produces identical digest
	pReverse := objective.Plan{
		ID:        "test-order-preservation",
		Objective: "Demonstrate non-mutating plan encoding",
		Tasks: []objective.Task{
			{ID: "task-a", Feature: "a", Scope: "backend/a"},
			{ID: "task-m", Feature: "m", Scope: "backend/m"},
			{ID: "task-z", Feature: "z", Scope: "backend/z"},
		},
	}
	digest1 := PlanDigest(p)
	digest2 := PlanDigest(pReverse)
	if digest1 != digest2 {
		t.Fatalf("PlanDigest must be deterministic regardless of input order: %s != %s", digest1, digest2)
	}
}
