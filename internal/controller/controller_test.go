package controller

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
	"github.com/eeyung1/multi-agent-orchestrator/internal/recon"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/review"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

func TestWorkerHelper(t *testing.T) {
	if os.Getenv("ORCHESTRATOR_HELPER") != "1" {
		return
	}
	var w WorkerContext
	if err := json.NewDecoder(os.Stdin).Decode(&w); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if cwd != w.Worktree || w.Session != os.Getenv("ORCHESTRATOR_SESSION_ID") || w.Token == "" || w.Generation == 0 || w.Branch == "" || w.Objective == "" || len(w.Task.AcceptanceCriteria) == 0 || len(w.Instructions) == 0 {
		t.Fatal("incorrect worker context")
	}
	if barrier := os.Getenv("ORCHESTRATOR_BARRIER"); barrier != "" {
		if err := os.WriteFile(filepath.Join(barrier, w.Task.ID), []byte("started"), 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			entries, _ := os.ReadDir(barrier)
			if len(entries) >= 2 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("independent workers did not overlap")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if os.Getenv("ORCHESTRATOR_STALE") == "1" {
		if path := os.Getenv("ORCHESTRATOR_STARTED"); path != "" {
			if err := os.WriteFile(path, []byte("started"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if w.Task.ID == "repair" && w.Feedback == "" {
		os.Exit(7)
	}
	if w.Task.ID == "bad" {
		os.Exit(7)
	}
	if err := os.MkdirAll(w.Task.Scope, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.Task.Scope, "result.txt")
	if err := os.WriteFile(path, []byte(w.Task.Description+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ORCHESTRATOR_UNOWNED") == "1" {
		if err := os.WriteFile(filepath.Join(cwd, "outside.txt"), []byte("unowned"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(w.Protocol, "Leave changes uncommitted") {
		return
	}
	helperGit(t, cwd, "add", "--", path)
	helperGit(t, cwd, "commit", "-m", "Implement "+w.Task.ID)
}

func nativeFixture(t *testing.T) (string, Config) {
	d, c := fixture(t)
	bin := t.TempDir()
	program := filepath.Join(bin, "codex")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(os.Args[0], "'", "'\"'\"'") + "' -test.run=^TestWorkerHelper$\n"
	if err := os.WriteFile(program, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c.Planner = planner.Provider{Adapter: "codex", Program: program}
	c.Workers = map[string]Worker{"codex": {Adapter: "codex", Program: program}}
	return d, c
}

func TestNativeWorkerLeavesGitToControllerAndRetainsRestartAuthority(t *testing.T) {
	d, c := nativeFixture(t)
	v := task("native")
	v.Capability = "codex"
	p := admit(t, d, c, v)
	r, err := Run(context.Background(), d, p.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "AWAITING_REVIEW" {
		t.Fatalf("%+v", r)
	}
	s, err := scheduler.Read(d)
	if err != nil {
		t.Fatal(err)
	}
	o := s.Objectives[p.ID]
	if o.PlannerProvider != "codex" || len(o.WorkerProviders) != 1 || o.WorkerProviders[0] != "codex" {
		t.Fatalf("provider selection not persisted: %+v", o)
	}
	st := s.Tasks[p.ID+"::native"]
	if helperGit(t, st.Worktree, "log", "-1", "--format=%s") != "Implement native" || st.ValidatedHead == "" {
		t.Fatalf("controller did not commit/validate: %+v", st)
	}
	if _, err := Run(context.Background(), d, p.ID, c); err != nil {
		t.Fatal(err)
	}
	s, _ = scheduler.Read(d)
	if s.Tasks[p.ID+"::native"].Attempt != 1 {
		t.Fatal("restart duplicated native assignment")
	}
	c.MaxWorkers++
	if _, err := Run(context.Background(), d, p.ID, c); err == nil {
		t.Fatal("restart weakened policy fencing")
	}
}

func TestNativeControllerRefusesUnownedEditsBeforeCommit(t *testing.T) {
	d, c := nativeFixture(t)
	t.Setenv("ORCHESTRATOR_UNOWNED", "1")
	v := task("native")
	v.Capability = "codex"
	p := admit(t, d, c, v)
	r, err := Run(context.Background(), d, p.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status == "AWAITING_REVIEW" {
		t.Fatal("unowned changes accepted")
	}
	s, _ := scheduler.Read(d)
	st := s.Tasks[p.ID+"::native"]
	if st.Status != "BLOCKED" || len(s.Results) != 0 || helperGit(t, st.Worktree, "rev-parse", "HEAD") != st.BaseSHA {
		t.Fatalf("unowned output committed/published: %+v", st)
	}
	claims, _ := coordination.Claims(d)
	if len(claims) != 1 {
		t.Fatal("blocked claim lost")
	}
}

func TestMultipleNativeProvidersUseSeparateAssignments(t *testing.T) {
	d, c := nativeFixture(t)
	bin := filepath.Dir(c.Workers["codex"].Program)
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexec '"+strings.ReplaceAll(os.Args[0], "'", "'\"'\"'")+"' -test.run=^TestWorkerHelper$\n"), 0755); err != nil {
		t.Fatal(err)
	}
	c.Workers["claude"] = Worker{Adapter: "claude", Program: filepath.Join(bin, "claude")}
	a, b := task("codex-task"), task("claude-task")
	a.Capability = "codex"
	b.Capability = "claude"
	t.Setenv("ORCHESTRATOR_BARRIER", t.TempDir())
	p := admit(t, d, c, a, b)
	r, err := Run(context.Background(), d, p.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "AWAITING_REVIEW" || len(r.Tasks) != 2 {
		t.Fatalf("%+v", r)
	}
	s, _ := scheduler.Read(d)
	if s.Tasks[p.ID+"::codex-task"].SessionID == s.Tasks[p.ID+"::claude-task"].SessionID {
		t.Fatal("providers shared ownership")
	}
}

func TestNativeBindingSkipsTargetControlledPathShadow(t *testing.T) {
	d, c := nativeFixture(t)
	bin := filepath.Join(d, "bin")
	if err := os.Mkdir(bin, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "node"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	v := task("native")
	v.Capability = "codex"
	p := admit(t, d, c, v)
	r, err := Run(context.Background(), d, p.ID, c)
	if err != nil || r.Status != "AWAITING_REVIEW" {
		t.Fatalf("trusted binding was shadowed: %+v %v", r, err)
	}
}

func fixture(t *testing.T) (string, Config) {
	t.Helper()
	t.Setenv("ORCHESTRATOR_HELPER", "1")
	d := t.TempDir()
	helperGit(t, d, "init", "-b", "main")
	helperGit(t, d, "config", "user.email", "test@example.com")
	helperGit(t, d, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(d, "AGENTS.md"), []byte("# Project instructions\nKeep implementation focused and run the configured checks.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	helperGit(t, d, "add", "AGENTS.md")
	helperGit(t, d, "commit", "-m", "Seed")
	if _, err := enrollment.Enroll(d, enrollment.Native, "test human"); err != nil {
		t.Fatal(err)
	}
	c := Config{Workers: map[string]Worker{"coding": {Program: os.Args[0], Args: []string{"-test.run=^TestWorkerHelper$"}}}, Validators: map[string]execution.Command{"check": {Program: "git", Args: []string{"diff", "--check", "HEAD~1", "HEAD"}}, "fail": {Program: "false"}}, MaxWorkers: 2, TimeoutSeconds: 20, HeartbeatSeconds: 1}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	return d, c
}
func task(id string, deps ...string) objective.Task {
	return objective.Task{ID: id, Feature: id, Description: "Implement " + id, Scope: id, DependsOn: deps, Priority: 10, AcceptanceCriteria: []string{"Committed result exists and has no whitespace errors"}, Validation: []string{"check"}, Capability: "coding"}
}
func admit(t *testing.T, d string, c Config, tasks ...objective.Task) objective.Plan {
	t.Helper()
	p := objective.Plan{ID: "objective", Objective: "Implement the requested fixture outputs", Tasks: tasks}
	r, err := recon.Inspect(d, p.Objective)
	if err != nil {
		t.Fatal(err)
	}
	if err := Admit(d, p, c, r, "test human approved exact plan"); err != nil {
		t.Fatal(err)
	}
	return p
}
func helperGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func TestConcurrentWorkersAndReviewNeverMerge(t *testing.T) {
	d, c := fixture(t)
	base := helperGit(t, d, "rev-parse", "main")
	t.Setenv("ORCHESTRATOR_BARRIER", t.TempDir())
	p := admit(t, d, c, task("first"), task("second"))
	r, err := Run(context.Background(), d, p.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "AWAITING_REVIEW" || r.ReviewStatus != "AWAITING_REVIEW" || len(r.Tasks) != 2 || len(r.NotCompleted) != 0 {
		t.Fatalf("report=%+v", r)
	}
	s, err := scheduler.Read(d)
	if err != nil {
		t.Fatal(err)
	}
	a := s.Tasks[p.ID+"::first"]
	b := s.Tasks[p.ID+"::second"]
	if a.SessionID == b.SessionID || a.Worktree == b.Worktree || a.Branch == b.Branch {
		t.Fatal("workers shared an assignment")
	}
	for _, result := range r.Tasks {
		if result.HeadSHA == "" || result.Validation == "" || len(result.Artifacts) == 0 {
			t.Fatalf("missing evidence: %+v", result)
		}
	}
	claims, err := coordination.Claims(d)
	if err != nil || len(claims) != 0 {
		t.Fatalf("completed claims=%+v err=%v", claims, err)
	}
	if err := review.Decide(d, p.ID, "APPROVED", "human reviewer"); err != nil {
		t.Fatal(err)
	}
	if helperGit(t, d, "rev-parse", "main") != base || helperGit(t, d, "branch", "--show-current") != "main" {
		t.Fatal("Planner changed integration branch")
	}
	if helperGit(t, d, "remote") != "" {
		t.Fatal("unexpected remote publication")
	}
	r2, err := Run(context.Background(), d, p.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if r2.ReviewStatus != "APPROVED" {
		t.Fatalf("review reset on restart: %+v", r2)
	}
	s2, _ := scheduler.Read(d)
	if s2.Tasks[p.ID+"::first"].Attempt != 1 {
		t.Fatal("completed task duplicated")
	}
}

func TestBlockedAndFailedValidationDoNotStopIndependentWork(t *testing.T) {
	for _, mode := range []string{"worker", "validation"} {
		t.Run(mode, func(t *testing.T) {
			d, c := fixture(t)
			bad := task("bad")
			if mode == "validation" {
				bad = task("invalid")
				bad.Validation = []string{"fail"}
			}
			p := admit(t, d, c, bad, task("good"), task("dependent", bad.ID))
			r, err := Run(context.Background(), d, p.ID, c)
			if err != nil {
				t.Fatal(err)
			}
			s, _ := scheduler.Read(d)
			if s.Tasks[p.ID+"::good"].Status != "COMPLETE" || s.Tasks[p.ID+"::"+bad.ID].Status != "BLOCKED" || s.Tasks[p.ID+"::dependent"].Status != "PENDING" {
				t.Fatalf("tasks=%+v", s.Tasks)
			}
			if r.ReviewStatus == "AWAITING_REVIEW" || len(r.Decisions) == 0 {
				t.Fatalf("missing decision: %+v", r)
			}
			claims, _ := coordination.Claims(d)
			if len(claims) != 1 {
				t.Fatalf("blocked ownership was released: %+v", claims)
			}
			if _, err := Run(context.Background(), d, p.ID, c); err != nil {
				t.Fatal(err)
			}
			s, _ = scheduler.Read(d)
			if s.Tasks[p.ID+"::"+bad.ID].Attempt != 1 {
				t.Fatal("blocked work retried without recovery authorization")
			}
		})
	}
}

func TestDependentWorkUsesCollectedHeadWithoutMerging(t *testing.T) {
	d, c := fixture(t)
	base := helperGit(t, d, "rev-parse", "main")
	p := admit(t, d, c, task("first"), task("second", "first"))
	r, err := Run(context.Background(), d, p.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "AWAITING_REVIEW" {
		t.Fatalf("%+v", r)
	}
	s, _ := scheduler.Read(d)
	first := s.Results[s.Tasks[p.ID+"::first"].ResultID]
	second := s.Tasks[p.ID+"::second"]
	if second.BaseSHA != first.HeadSHA {
		t.Fatalf("dependent base=%s prerequisite=%s", second.BaseSHA, first.HeadSHA)
	}
	if _, err := os.Stat(filepath.Join(second.Worktree, "first", "result.txt")); err != nil {
		t.Fatal("prerequisite output missing")
	}
	if helperGit(t, d, "rev-parse", "main") != base {
		t.Fatal("dependency caused integration")
	}
}

func TestDivergentDependenciesRequireHumanIntegration(t *testing.T) {
	d, c := fixture(t)
	p := admit(t, d, c, task("first"), task("second"), task("third", "first", "second"))
	r, err := Run(context.Background(), d, p.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.NotCompleted) != 1 || r.NotCompleted[0] != "third" {
		t.Fatalf("%+v", r)
	}
	found := false
	for _, decision := range r.Decisions {
		if decision.Kind == "dependency_integration" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing genuine integration decision")
	}
}

func TestRestartPreservesExistingAssignmentAndRunsIndependentWork(t *testing.T) {
	d, c := fixture(t)
	p := admit(t, d, c, task("first"), task("second"))
	a, err := scheduler.Acquire(scheduler.Request{Workspace: d, Agent: "existing", Role: "coding", Base: helperGit(t, d, "rev-parse", "main"), Plan: p, TaskID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run(context.Background(), d, p.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := scheduler.Read(d)
	if s.Tasks[p.ID+"::first"].Attempt != 1 || s.Tasks[p.ID+"::first"].SessionID != a.SessionID || s.Tasks[p.ID+"::second"].Status != "COMPLETE" {
		t.Fatalf("restart duplicated/lost work: %+v", s.Tasks)
	}
	if _, err := os.Stat(filepath.Join(a.Worktree, "first", "result.txt")); !os.IsNotExist(err) {
		t.Fatal("existing worker was relaunched")
	}
	if len(r.Decisions) == 0 {
		t.Fatal("existing writer reconciliation not reported")
	}
}

func TestStaleGenerationRejectedDuringWorkerExecution(t *testing.T) {
	d, c := fixture(t)
	t.Setenv("ORCHESTRATOR_STALE", "1")
	started := filepath.Join(t.TempDir(), "started")
	t.Setenv("ORCHESTRATOR_STARTED", started)
	p := admit(t, d, c, task("first"))
	type outcome struct {
		r   Report
		err error
	}
	done := make(chan outcome, 1)
	go func() { r, err := Run(context.Background(), d, p.ID, c); done <- outcome{r, err} }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s, err := scheduler.Read(d)
		if err != nil {
			t.Fatal(err)
		}
		v := s.Tasks[p.ID+"::first"]
		_, startedErr := os.Stat(started)
		if v.SessionID != "" && startedErr == nil {
			id, _ := repository.Identity(d)
			store, _ := state.Open(d, id)
			if err := store.Update("test administrator", "fence-old-assignment", func(s *state.Snapshot) error {
				t := s.Tasks[p.ID+"::first"]
				t.AssignmentGeneration++
				s.Tasks[p.ID+"::first"] = t
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("assignment not created")
		}
		time.Sleep(10 * time.Millisecond)
	}
	o := <-done
	if o.err != nil {
		t.Fatal(o.err)
	}
	s, _ := scheduler.Read(d)
	if len(s.Results) != 0 || len(s.Reviews) != 0 {
		t.Fatal("stale output accepted")
	}
	if len(o.r.Decisions) == 0 {
		t.Fatal("fenced result was not reported")
	}
}

func TestRepairUsesSameOwnedAssignmentAndFeedback(t *testing.T) {
	d, c := fixture(t)
	c.RepairAttempts = 1
	p := admit(t, d, c, task("repair"))
	r, err := Run(context.Background(), d, p.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "AWAITING_REVIEW" {
		t.Fatalf("%+v", r)
	}
	s, _ := scheduler.Read(d)
	if s.Tasks[p.ID+"::repair"].Attempt != 1 {
		t.Fatal("repair replaced the assignment")
	}
}

func TestManagedPlansCannotBypassEvidenceOrAdmittedDefinition(t *testing.T) {
	d, c := fixture(t)
	p := admit(t, d, c, task("first"))
	a, err := scheduler.Acquire(scheduler.Request{Workspace: d, Agent: "one", Role: "coding", Plan: p, Base: helperGit(t, d, "rev-parse", "main")})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Complete(d, p.ID, a.Task.ID, a.Token, a.SessionID, a.AssignmentGeneration); err == nil {
		t.Fatal("managed completion bypassed result evidence")
	}
	if _, err := review.Collect(review.ResultInput{Workspace: d, ObjectiveID: p.ID, TaskID: a.Task.ID, SessionID: a.SessionID, Token: a.Token, Branch: a.Branch, Worktree: a.Worktree, AssignmentGeneration: a.AssignmentGeneration, Validation: "model says passed"}); err == nil {
		t.Fatal("unverified evidence accepted")
	}
	p.Tasks = append(p.Tasks, task("unauthorized"))
	if _, err := scheduler.Acquire(scheduler.Request{Workspace: d, Agent: "two", Role: "coding", Plan: p, Base: helperGit(t, d, "rev-parse", "main")}); err == nil {
		t.Fatal("model appended an unapproved task")
	}
}

func TestRestartCollectsPublishedCompletionWithoutWorkerRelaunch(t *testing.T) {
	d, c := fixture(t)
	p := admit(t, d, c, task("first"))
	a, err := scheduler.Acquire(scheduler.Request{Workspace: d, Agent: "one", Role: "coding", Plan: p, Base: helperGit(t, d, "rev-parse", "main")})
	if err != nil {
		t.Fatal(err)
	}
	r, err := recon.Inspect(d, p.Objective)
	if err != nil {
		t.Fatal(err)
	}
	w := WorkerContext{Objective: p.Objective, Task: a.Task, Session: a.SessionID, Token: a.Token, Generation: a.AssignmentGeneration, Branch: a.Branch, Worktree: a.Worktree, Instructions: r.Instructions}
	b, _ := json.Marshal(w)
	if _, err := execution.Run(execution.Request{Primary: d, Worktree: a.Worktree, SessionID: a.SessionID, Command: execution.Command{Program: c.Workers["coding"].Program, Args: c.Workers["coding"].Args}, Input: string(b), Timeout: 20 * time.Second}); err != nil {
		t.Fatal(err)
	}
	s, _ := scheduler.Read(d)
	st := s.Tasks[p.ID+"::first"]
	head, err := review.CheckOutput(st)
	if err != nil {
		t.Fatal(err)
	}
	helperGit(t, a.Worktree, "diff", "--check", "HEAD~1", "HEAD")
	in := input(d, p.ID, st)
	in.ValidatedHead = head
	in.Validation = "diff-check: PASS"
	if err := review.RecordValidation(in); err != nil {
		t.Fatal(err)
	}
	if err := coordination.CompleteSession(d, a.SessionID, a.Token, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	// No remaining live claim: a restart must collect, never execute again.
	report, err := Run(context.Background(), d, p.ID, c)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "AWAITING_REVIEW" || report.Tasks[0].HeadSHA != head {
		t.Fatalf("%+v", report)
	}
	s, _ = scheduler.Read(d)
	if s.Tasks[p.ID+"::first"].Attempt != 1 {
		t.Fatal("assignment duplicated")
	}
}

func TestAdmissionRequiresHumanAndRejectsScopeInstructionDrift(t *testing.T) {
	d, c := fixture(t)
	p := objective.Plan{ID: "p", Objective: "work", Tasks: []objective.Task{task("first")}}
	r, err := recon.Inspect(d, p.Objective)
	if err != nil {
		t.Fatal(err)
	}
	if err := Admit(d, p, c, r, ""); err == nil {
		t.Fatal("Planner self-authorized")
	}
	c.AllowedScopes = []string{"docs"}
	if err := Admit(d, p, c, r, "human"); err == nil {
		t.Fatal("scope expansion silently allowed")
	}
	c.AllowedScopes = []string{"."}
	if err := os.WriteFile(filepath.Join(d, "AGENTS.md"), []byte("Changed instructions"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Admit(d, p, c, r, "human"); err == nil {
		t.Fatal("stale instruction recon accepted")
	}
}
