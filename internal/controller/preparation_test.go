package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
)

func TestPreparationHelper(t *testing.T) {
	if os.Getenv("ORCHESTRATOR_HELPER") != "1" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil || cwd != os.Getenv("ORCHESTRATOR_WORKTREE") || os.Getenv("ORCHESTRATOR_SESSION_ID") == "" {
		t.Fatal("preparation did not receive the exact assigned worktree/session")
	}
	if localBin := os.Getenv("ORCHESTRATOR_UNTRUSTED_BIN"); localBin != "" {
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			if dir == localBin {
				t.Fatal("preparation inherited a project-controlled interpreter PATH")
			}
		}
	}
	switch os.Args[len(os.Args)-1] {
	case "first; literal argument":
		if _, err := os.Stat(".worktree-preparation"); !os.IsNotExist(err) {
			t.Fatal("preparation ran more than once or copied primary local dependencies")
		}
		if err := os.WriteFile(".worktree-preparation", []byte("first\n"), 0600); err != nil {
			t.Fatal(err)
		}
	case "second":
		b, err := os.ReadFile(".worktree-preparation")
		if err != nil || string(b) != "first\n" {
			t.Fatal("preparation commands ran out of order")
		}
		if err := os.WriteFile(".worktree-preparation", []byte("first\nsecond\n"), 0600); err != nil {
			t.Fatal(err)
		}
	case "fail":
		fmt.Println("fixture dependency installation failed")
		os.Exit(7)
	case "change-instructions":
		if err := os.WriteFile("AGENTS.md", []byte("Changed during preparation"), 0644); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unexpected preparation arguments")
	}
}

func preparationCommand(mode string) execution.Command {
	return execution.Command{Program: os.Args[0], Args: []string{"-test.run=^TestPreparationHelper$", "--", mode}}
}

func TestWorktreePreparationBeforeWorker(t *testing.T) {
	for _, withPreparation := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured=%t", withPreparation), func(t *testing.T) {
			d, c := fixture(t)
			if err := os.WriteFile(filepath.Join(d, ".gitignore"), []byte(".worktree-preparation\n"), 0644); err != nil {
				t.Fatal(err)
			}
			helperGit(t, d, "add", ".gitignore")
			helperGit(t, d, "commit", "-m", "Ignore local preparation output")
			if err := os.WriteFile(filepath.Join(d, ".worktree-preparation"), []byte("primary local dependencies"), 0600); err != nil {
				t.Fatal(err)
			}
			started := filepath.Join(t.TempDir(), "worker-started")
			t.Setenv("ORCHESTRATOR_WORKER_STARTED", started)
			t.Setenv("ORCHESTRATOR_EXPECT_PREPARATION", "none")
			if withPreparation {
				c.WorktreePreparation = []execution.Command{preparationCommand("first; literal argument"), preparationCommand("second")}
				// Bare executable names must skip primary-checkout PATH shadows,
				// even though execution happens in a nested linked worktree.
				localBin := filepath.Join(d, "bin")
				if err := os.Mkdir(localBin, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(localBin, "prepare-fixture"), []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
					t.Fatal(err)
				}
				trustedBin := t.TempDir()
				if err := os.Symlink(os.Args[0], filepath.Join(trustedBin, "prepare-fixture")); err != nil {
					t.Fatal(err)
				}
				c.WorktreePreparation[0].Program = "prepare-fixture"
				t.Setenv("PATH", localBin+string(os.PathListSeparator)+trustedBin+string(os.PathListSeparator)+os.Getenv("PATH"))
				t.Setenv("ORCHESTRATOR_UNTRUSTED_BIN", localBin)
				t.Setenv("ORCHESTRATOR_EXPECT_PREPARATION", "first\nsecond\n")
			}
			// A bounded worker repair must not rerun successful preparation.
			c.RepairAttempts = 1
			p := admit(t, d, c, task("repair"))
			r, err := Run(context.Background(), d, p.ID, c)
			if err != nil || r.Status != "AWAITING_REVIEW" {
				t.Fatalf("%+v %v", r, err)
			}
			s, err := scheduler.Read(d)
			if err != nil {
				t.Fatal(err)
			}
			worktree := s.Tasks[p.ID+"::repair"].Worktree
			b, err := os.ReadFile(started)
			if err != nil || string(b) != worktree || worktree == d {
				t.Fatalf("worker did not run in the isolated worktree: %q %v", b, err)
			}
			b, err = os.ReadFile(filepath.Join(d, ".worktree-preparation"))
			if err != nil || string(b) != "primary local dependencies" {
				t.Fatal("preparation changed primary checkout dependencies")
			}
			log, err := os.ReadFile(filepath.Join(d, "multi-agent-orchestrator", "resolution.md"))
			if err != nil || strings.Contains(string(log), "TestPreparationHelper") != withPreparation {
				t.Fatalf("incorrect supervised preparation evidence: %s %v", log, err)
			}
		})
	}
}

func TestPreparationFailureBlocksWorkerAndRetainsOwnership(t *testing.T) {
	for _, mode := range []string{"fail", "change-instructions"} {
		t.Run(mode, func(t *testing.T) {
			d, c := fixture(t)
			started := filepath.Join(t.TempDir(), "worker-started")
			t.Setenv("ORCHESTRATOR_WORKER_STARTED", started)
			c.RepairAttempts = 3
			c.WorktreePreparation = []execution.Command{preparationCommand(mode), preparationCommand("first; literal argument")}
			p := admit(t, d, c, task("first"))
			r, err := Run(context.Background(), d, p.ID, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Tasks) != 1 || r.Tasks[0].Status != "BLOCKED" || !strings.Contains(r.Tasks[0].Blocker, "worktree preparation command 1") {
				t.Fatalf("missing preparation blocker: %+v", r)
			}
			if mode == "fail" && !strings.Contains(r.Tasks[0].Blocker, "fixture dependency installation failed") {
				t.Fatalf("missing failure output: %+v", r)
			}
			if mode == "change-instructions" && !strings.Contains(r.Tasks[0].Blocker, "assigned worktree instructions differ") {
				t.Fatalf("preparation bypassed recon guard: %+v", r)
			}
			if _, err := os.Stat(started); !os.IsNotExist(err) {
				t.Fatal("worker launched after preparation failure")
			}
			s, err := scheduler.Read(d)
			if err != nil {
				t.Fatal(err)
			}
			st := s.Tasks[p.ID+"::first"]
			if len(s.Results) != 0 || st.Attempt != 1 {
				t.Fatal("failed preparation produced a result or retried assignment")
			}
			if _, err := os.Stat(filepath.Join(st.Worktree, ".worktree-preparation")); !os.IsNotExist(err) {
				t.Fatal("later preparation command ran after failure")
			}
			claims, err := coordination.Claims(d)
			if err != nil || len(claims) != 1 {
				t.Fatalf("failed preparation lost ownership: %+v %v", claims, err)
			}
		})
	}
}

func TestPreparationPolicyChangesRequireHumanAuthorization(t *testing.T) {
	d, c := fixture(t)
	p := admit(t, d, c, task("first"))
	c.WorktreePreparation = []execution.Command{preparationCommand("fail")}
	if _, err := Run(context.Background(), d, p.ID, c); err == nil || !strings.Contains(err.Error(), "matching human authorization") {
		t.Fatalf("preparation bypassed authorized policy digest: %v", err)
	}
}

func TestPreparationExecutablePolicyRejectsUntrustedPrograms(t *testing.T) {
	d, c := fixture(t)
	local := filepath.Join(d, "prepare")
	if err := os.WriteFile(local, []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(t.TempDir(), "prepare")
	if err := os.Symlink(local, symlink); err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"", "missing-preparation-executable", "./prepare", local, symlink} {
		t.Run(program, func(t *testing.T) {
			c.WorktreePreparation = []execution.Command{{Program: program}}
			if err := c.ValidatePolicy(); err == nil {
				if err := c.ValidateAdapters(d); err == nil {
					t.Fatal("untrusted preparation executable accepted")
				}
			}
		})
	}
}

func TestExplicitPreparationPolicyReauthorizationPreservesObjectiveState(t *testing.T) {
	d, c := fixture(t)
	p := admit(t, d, c, task("first"))
	before, err := scheduler.Read(d)
	if err != nil {
		t.Fatal(err)
	}
	beforeTask := before.Tasks[p.ID+"::first"]
	oldDigest := before.Objectives[p.ID].PolicyDigest

	c.WorktreePreparation = []execution.Command{preparationCommand("first; literal argument")}
	if _, err := Run(context.Background(), d, p.ID, c); err == nil || !strings.Contains(err.Error(), "matching human authorization") {
		t.Fatalf("changed policy ran without reauthorization: %v", err)
	}
	if err := ReauthorizePolicy(d, p.ID, c, "test human explicitly approved changed preparation policy"); err != nil {
		t.Fatal(err)
	}
	after, err := scheduler.Read(d)
	if err != nil {
		t.Fatal(err)
	}
	afterTask := after.Tasks[p.ID+"::first"]
	if after.Objectives[p.ID].PolicyDigest == oldDigest || after.Objectives[p.ID].PolicyDigest != PolicyDigest(c) {
		t.Fatal("policy digest was not reauthorized")
	}
	if beforeTask.Status != afterTask.Status || beforeTask.Attempt != afterTask.Attempt ||
		beforeTask.AssignmentGeneration != afterTask.AssignmentGeneration || beforeTask.Token != afterTask.Token ||
		beforeTask.SessionID != afterTask.SessionID || beforeTask.ResultID != afterTask.ResultID {
		t.Fatalf("policy reauthorization changed task state: before=%+v after=%+v", beforeTask, afterTask)
	}
	found := false
	for _, event := range after.Events {
		if event.Operation == "reauthorize-objective-policy" {
			found = true
		}
	}
	if !found {
		t.Fatal("policy reauthorization authority event missing")
	}
}

func TestPreparationPolicyReauthorizationRejectsInstructionDrift(t *testing.T) {
	d, c := fixture(t)
	p := admit(t, d, c, task("first"))
	c.WorktreePreparation = []execution.Command{preparationCommand("first; literal argument")}
	if err := os.WriteFile(filepath.Join(d, "AGENTS.md"), []byte("Changed instructions\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ReauthorizePolicy(d, p.ID, c, "test human"); err == nil || !strings.Contains(err.Error(), "instructions changed") {
		t.Fatalf("instruction drift was reauthorized: %v", err)
	}
}
