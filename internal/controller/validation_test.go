package controller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/review"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

func TestValidatorHelper(t *testing.T) {
	if os.Getenv("ORCHESTRATOR_HELPER") != "1" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil || cwd != os.Getenv("ORCHESTRATOR_WORKTREE") {
		t.Fatal("validator ran outside its assigned worktree")
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "check-intermediate" {
		b, err := os.ReadFile("TRIAL_REPORT.md")
		if err != nil || string(b) != "generated measurements\n" {
			t.Fatal("validation intermediates were removed before the next check")
		}
		return
	}
	if dirty := helperGit(t, cwd, "status", "--porcelain", "--untracked-files=all"); dirty != "" {
		t.Fatalf("validator started without a clean worker boundary: %s", dirty)
	}
	if err := os.WriteFile(os.Getenv("ORCHESTRATOR_VALIDATION_HEAD"), []byte(helperGit(t, cwd, "rev-parse", "HEAD")), 0600); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	switch mode {
	case "normal":
	case "tracked", "intermediate":
		write("TRIAL_REPORT.md", "generated measurements\n")
	case "wait-for-fence":
		write("TRIAL_REPORT.md", "generated measurements\n")
		write(os.Getenv("ORCHESTRATOR_VALIDATOR_STARTED"), "started")
		time.Sleep(300 * time.Millisecond)
	case "ignore-rules":
		write(".gitignore", ".cache/\ngenerated/\n")
		write("generated/newly-ignored.txt", "generated\n")
	case "symlink":
		if err := os.Symlink(os.Getenv("ORCHESTRATOR_EXTERNAL_OUTPUT"), "generated-link"); err != nil {
			t.Fatal(err)
		}
	case "untracked":
		write("generated/nested/:(glob)* report.txt", "generated\n")
		write(".cache/new-output.txt", "new ignored output\n")
		write("-validator-file", "option-like filename\n")
		if err := os.Mkdir("empty-output", 0755); err != nil {
			t.Fatal(err)
		}
	case "index", "index-cancel", "failure":
		write("TRIAL_REPORT.md", "generated measurements\n")
		helperGit(t, cwd, "add", "TRIAL_REPORT.md")
		if mode == "index-cancel" {
			write("TRIAL_REPORT.md", "original report\n")
		}
		write("generated/staged\nreport.txt", "new staged artifact\n")
		helperGit(t, cwd, "add", "--", "generated/staged\nreport.txt")
		if mode == "failure" {
			os.Exit(7)
		}
	case "stage-existing-ignored":
		helperGit(t, cwd, "add", "-f", "--", ".cache/keep")
	case "rename":
		if err := os.Mkdir("generated", 0755); err != nil {
			t.Fatal(err)
		}
		helperGit(t, cwd, "mv", "--", "TRIAL_REPORT.md", "generated/renamed.txt")
	case "commit":
		write("TRIAL_REPORT.md", "validator committed measurements\n")
		helperGit(t, cwd, "add", "TRIAL_REPORT.md")
		helperGit(t, cwd, "commit", "-m", "Validator changed history")
	case "detach":
		helperGit(t, cwd, "checkout", "--detach", "HEAD")
	case "switch-branch":
		helperGit(t, cwd, "switch", "-c", "validator-branch")
	case "nested-repository":
		helperGit(t, cwd, "init", "nested-output")
	case "nested-tracked-replacement":
		if err := os.Remove("TRIAL_REPORT.md"); err != nil {
			t.Fatal(err)
		}
		helperGit(t, cwd, "init", "TRIAL_REPORT.md")
	case "cleanup-failure":
		write("TRIAL_REPORT.md", "generated measurements\n")
		write(helperGit(t, cwd, "rev-parse", "--git-path", "index.lock"), "retain lock for inspection\n")
	default:
		t.Fatalf("unknown validator mode: %s", mode)
	}
}

func validatorCommand(mode string) execution.Command {
	return execution.Command{Program: os.Args[0], Args: []string{"-test.run=^TestValidatorHelper$", "--", mode}}
}

func TestTrustedValidationFilesystemBoundary(t *testing.T) {
	for _, tc := range []struct {
		mode, blocker string
		clean         bool
	}{
		{mode: "normal", clean: true},
		{mode: "tracked", clean: true},
		{mode: "intermediate", clean: true},
		{mode: "untracked", clean: true},
		{mode: "ignore-rules", clean: true},
		{mode: "symlink", clean: true},
		{mode: "index", clean: true},
		{mode: "index-cancel", clean: true},
		{mode: "stage-existing-ignored", clean: true},
		{mode: "rename", clean: true},
		{mode: "failure", clean: true, blocker: "exit status 7"},
		{mode: "commit", blocker: "validator changed HEAD"},
		{mode: "detach", blocker: "validator changed the assigned branch"},
		{mode: "switch-branch", blocker: "validator changed the assigned branch"},
		{mode: "nested-repository", blocker: "nested Git repository"},
		{mode: "nested-tracked-replacement", blocker: "nested Git repository"},
		{mode: "cleanup-failure", blocker: "validation boundary rejected"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			d, c := nativeFixture(t)
			if err := os.WriteFile(filepath.Join(d, "TRIAL_REPORT.md"), []byte("original report\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d, ".gitignore"), []byte(".cache/\n"), 0644); err != nil {
				t.Fatal(err)
			}
			helperGit(t, d, "add", "TRIAL_REPORT.md", ".gitignore")
			helperGit(t, d, "commit", "-m", "Seed generated report and ignored cache")
			// Preparation simulates an ignored local dependency already present
			// when the clean validation boundary is captured.
			c.WorktreePreparation = []execution.Command{{Program: "mkdir", Args: []string{".cache"}}, {Program: "touch", Args: []string{".cache/keep"}}}
			headFile := filepath.Join(t.TempDir(), "worker-head")
			t.Setenv("ORCHESTRATOR_VALIDATION_HEAD", headFile)
			external := t.TempDir()
			t.Setenv("ORCHESTRATOR_EXTERNAL_OUTPUT", external)
			if err := os.WriteFile(filepath.Join(external, "preserve.txt"), []byte("external data"), 0644); err != nil {
				t.Fatal(err)
			}
			c.Validators["mutating"] = validatorCommand(tc.mode)
			v := task("first")
			v.Capability = "codex"
			v.Validation = []string{"mutating", "check"}
			if tc.mode == "intermediate" {
				c.Validators["intermediate"] = validatorCommand("check-intermediate")
				v.Validation = []string{"mutating", "intermediate", "check"}
			}
			if tc.blocker != "" && tc.mode != "failure" {
				c.RepairAttempts = 2 // Integrity failures must never trigger repair.
			}
			p := admit(t, d, c, v)
			r, err := Run(context.Background(), d, p.ID, c)
			if err != nil {
				t.Fatal(err)
			}
			s, err := scheduler.Read(d)
			if err != nil {
				t.Fatal(err)
			}
			st := s.Tasks[p.ID+"::first"]
			captured, err := os.ReadFile(headFile)
			if err != nil {
				t.Fatalf("validator never reached the clean worker boundary: %v; report: %+v", err, r)
			}
			head := helperGit(t, st.Worktree, "rev-parse", "HEAD")
			if tc.mode == "commit" {
				if head == string(captured) || helperGit(t, st.Worktree, "rev-parse", "HEAD~1") != string(captured) {
					t.Fatal("validator history was reset or automatically repaired")
				}
			} else if head != string(captured) {
				t.Fatal("captured worker commit changed")
			}
			if helperGit(t, st.Worktree, "diff-tree", "--no-commit-id", "--name-only", "-r", string(captured)) != "first/result.txt" {
				t.Fatal("validator artifacts entered the worker commit")
			}
			if tc.clean {
				checked, err := review.CheckOutput(st)
				if err != nil || checked != string(captured) {
					t.Fatalf("cleanup did not preserve strict output review: %s %v", checked, err)
				}
				b, err := os.ReadFile(filepath.Join(st.Worktree, "TRIAL_REPORT.md"))
				if err != nil || string(b) != "original report\n" {
					t.Fatalf("tracked artifact was not restored: %q %v", b, err)
				}
				for _, name := range []string{"generated", "generated-link", ".cache/new-output.txt", "-validator-file", "empty-output"} {
					if _, err := os.Lstat(filepath.Join(st.Worktree, name)); !os.IsNotExist(err) {
						t.Fatalf("validator-created path was not removed: %s: %v", name, err)
					}
				}
			}
			if _, err := os.Stat(filepath.Join(st.Worktree, ".cache/keep")); err != nil {
				t.Fatal("preexisting ignored dependency was removed")
			}
			if b, err := os.ReadFile(filepath.Join(external, "preserve.txt")); err != nil || string(b) != "external data" {
				t.Fatal("cleanup followed a symlink outside the worktree")
			}
			if tc.blocker != "" {
				if st.Status != "BLOCKED" || !strings.Contains(st.Blocker, tc.blocker) || len(s.Results) != 0 || len(s.Reviews) != 0 {
					t.Fatalf("validator failure was accepted or lost: %+v; %+v", st, r)
				}
				if tc.mode == "failure" && !strings.Contains(st.Blocker, "side effects restored") {
					t.Fatal("failure blocker omitted successful cleanup evidence")
				}
				claims, err := coordination.Claims(d)
				if err != nil || len(claims) != 1 {
					t.Fatalf("failed assignment lost ownership: %+v %v", claims, err)
				}
				if tc.mode == "nested-repository" || tc.mode == "nested-tracked-replacement" {
					nestedPath := "nested-output"
					if tc.mode == "nested-tracked-replacement" {
						nestedPath = "TRIAL_REPORT.md"
					}
					if _, err := os.Stat(filepath.Join(st.Worktree, nestedPath, ".git")); err != nil {
						t.Fatal("nested repository was destructively cleaned")
					}
				}
				if tc.mode == "cleanup-failure" && helperGit(t, st.Worktree, "status", "--porcelain") == "" {
					t.Fatal("cleanup failure concealed the preserved side effects")
				}
			} else {
				result := s.Results[st.ResultID]
				if r.Status != "AWAITING_REVIEW" || st.Status != "COMPLETE" || st.ValidatedHead != string(captured) || result.HeadSHA != string(captured) {
					t.Fatalf("cleaned result did not reach exact-head review: %+v; %+v", st, r)
				}
				if tc.mode != "normal" && !strings.Contains(result.Validation, "side effects restored") {
					t.Fatal("result omitted cleanup evidence")
				}
			}
		})
	}
}

func TestStaleGenerationRejectedDuringValidation(t *testing.T) {
	d, c := nativeFixture(t)
	started := filepath.Join(t.TempDir(), "validator-started")
	t.Setenv("ORCHESTRATOR_VALIDATOR_STARTED", started)
	t.Setenv("ORCHESTRATOR_VALIDATION_HEAD", filepath.Join(t.TempDir(), "head"))
	c.Validators["waiting"] = validatorCommand("wait-for-fence")
	v := task("first")
	v.Capability = "codex"
	v.Validation = []string{"waiting"}
	p := admit(t, d, c, v)
	type outcome struct {
		report Report
		err    error
	}
	done := make(chan outcome, 1)
	go func() { r, err := Run(context.Background(), d, p.ID, c); done <- outcome{r, err} }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("validator did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	id, err := repository.Identity(d)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(d, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update("test administrator", "fence-validation-assignment", func(s *state.Snapshot) error {
		task := s.Tasks[p.ID+"::first"]
		task.AssignmentGeneration++
		s.Tasks[p.ID+"::first"] = task
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	s, err := scheduler.Read(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Results) != 0 || len(s.Reviews) != 0 || len(result.report.Decisions) == 0 {
		t.Fatal("stale validation was accepted")
	}
	worktree := s.Tasks[p.ID+"::first"].Worktree
	if b, err := os.ReadFile(filepath.Join(worktree, "TRIAL_REPORT.md")); err != nil || string(b) != "generated measurements\n" {
		t.Fatal("stale generation was allowed to clean another assignment's worktree")
	}
}
