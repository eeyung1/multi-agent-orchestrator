package review

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

func validationTask(t *testing.T) state.Task {
	t.Helper()
	d := repo(t)
	head, err := git(d, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	plan := objective.Plan{ID: "validation", Objective: "validate", Tasks: []objective.Task{{ID: "owned", Feature: "owned", Scope: "owned"}}}
	a, err := scheduler.Acquire(scheduler.Request{Workspace: d, Agent: "test-worker", Role: "coding", Base: head, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	return state.Task{Worktree: a.Worktree, Branch: a.Branch, BaseSHA: head, Scope: "owned"}
}

func TestBeginValidationPreservesStrictWorkerGate(t *testing.T) {
	for _, mode := range []string{"working-tree", "index", "unowned-commit"} {
		t.Run(mode, func(t *testing.T) {
			task := validationTask(t)
			if err := os.WriteFile(filepath.Join(task.Worktree, "seed"), []byte("unowned worker edit"), 0644); err != nil {
				t.Fatal(err)
			}
			if mode != "working-tree" {
				if err := execGit(task.Worktree, "add", "seed"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "unowned-commit" {
				if err := execGit(task.Worktree, "commit", "-m", "Unowned worker output"); err != nil {
					t.Fatal(err)
				}
			}
			boundary, err := BeginValidation(task, func() error { return nil })
			if err == nil || boundary != nil {
				t.Fatal("dirty or unowned worker output acquired cleanup authorization")
			}
		})
	}
}

func TestValidationCleanupStopsOnGuardLoss(t *testing.T) {
	task := validationTask(t)
	boundary, err := BeginValidation(task, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task.Worktree, "seed"), []byte("validator output"), 0644); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = boundary.Restore(func() error { return errors.New("assignment generation changed") }, func(...string) error { called = true; return nil })
	if !errors.Is(err, ErrValidationBoundary) || called {
		t.Fatalf("cleanup mutated an assignment after guard loss: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(task.Worktree, "seed"))
	if err != nil || string(b) != "validator output" {
		t.Fatal("guard failure did not preserve the checkout for inspection")
	}
}

func TestValidationCleanupPathSafety(t *testing.T) {
	d := t.TempDir()
	for _, name := range []string{"", ".", "..", "../outside", "/absolute", "./file", "dir/../../outside", ".git/config", "nested/.git/config", "dir\\file"} {
		if err := validationPath(d, name); err == nil {
			t.Fatalf("unsafe cleanup path accepted: %q", name)
		}
	}
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(d, "link")); err != nil {
		t.Fatal(err)
	}
	if err := validationPath(d, "link/file"); err == nil {
		t.Fatal("cleanup was allowed to traverse an external symlink")
	}
	if err := validationPath(d, "link"); err != nil {
		t.Fatal("removing a new leaf symlink should not follow its target")
	}
}

func TestValidationBoundaryRejectsDifferentWorktreeIdentity(t *testing.T) {
	task := validationTask(t)
	boundary, err := BeginValidation(task, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	// Replacing the checkout with another repository at a different Git directory
	// must not authorize cleanup, even if a caller supplies the same path.
	other := repo(t)
	boundary.task.Worktree = other
	if err := boundary.Verify(func() error { return nil }); !errors.Is(err, ErrValidationBoundary) || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("different Git worktree accepted: %v", err)
	}
}

func TestValidationCleanupFailureIsNotAccepted(t *testing.T) {
	task := validationTask(t)
	boundary, err := BeginValidation(task, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task.Worktree, "seed"), []byte("validator output"), 0644); err != nil {
		t.Fatal(err)
	}
	// Simulate a Git operation that appears successful but leaves output dirty.
	_, err = boundary.Restore(func() error { return nil }, func(args ...string) error {
		if len(args) < 2 || args[0] != "--literal-pathspecs" || args[1] != "restore" {
			t.Fatalf("unexpected cleanup operation: %v", args)
		}
		return nil
	})
	if !errors.Is(err, ErrValidationBoundary) {
		t.Fatalf("incomplete cleanup passed final output review: %v", err)
	}
	// No reset operation may hide a history change.
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = task.Worktree
	if b, err := cmd.Output(); err != nil || strings.TrimSpace(string(b)) != boundary.Head {
		t.Fatal("cleanup changed the captured HEAD")
	}
}
