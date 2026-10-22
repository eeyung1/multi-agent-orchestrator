package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"fmt"
	"github.com/eeyung1/multi-agent-orchestrator/internal/controller"
	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
)

func TestSilenceNeverAuthorizes(t *testing.T) {
	for _, answer := range []string{"", "\n", "no\n", "maybe\n"} {
		var out bytes.Buffer
		yes, _ := confirm(bufio.NewReader(strings.NewReader(answer)), &out, "Proceed? ")
		if yes {
			t.Fatalf("authorized %q", answer)
		}
	}
}

func TestBuiltInCLIHelper(t *testing.T) {
	if os.Getenv("ORCHESTRATOR_NATIVE_UI_HELPER") != "1" {
		return
	}
	answer := ""
	for i, arg := range os.Args {
		if arg == "--output-last-message" && i+1 < len(os.Args) {
			answer = os.Args[i+1]
		}
	}
	role := "worker"
	if answer != "" {
		role = "planner"
	}
	if path := os.Getenv("ORCHESTRATOR_CALLS_FILE"); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(role + "\n")
		if closeErr := f.Close(); err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
	if answer != "" {
		var r struct {
			Request planner.Request `json:"request"`
		}
		if err := json.NewDecoder(os.Stdin).Decode(&r); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(os.Args, " "), "--sandbox read-only") {
			t.Fatal("planner sandbox missing")
		}
		proposal := fmt.Sprintf(`{"plan":{"id":"proposed","objective":%q,"tasks":[{"id":"docs","feature":"docs","description":"Write fixture document","scope":"docs","acceptance_criteria":["Document exists"],"validation":["exists","diff-check"],"capability":"codex"}]}}`, r.Request.Objective)
		if err := os.WriteFile(answer, []byte(proposal), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !strings.Contains(strings.Join(os.Args, " "), "--sandbox workspace-write") {
		t.Fatal("worker sandbox missing")
	}
	var w controller.WorkerContext
	if err := json.NewDecoder(os.Stdin).Decode(&w); err != nil {
		t.Fatal(err)
	}
	if w.Task.Scope != "docs" || w.Session == "" || w.Generation == 0 || w.Token == "" {
		t.Fatal("assignment context missing")
	}
	if err := os.MkdirAll("docs", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("docs/readme.txt", []byte("Fixture document\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicCLISelectionAndRestartWithoutJSONProviderEditing(t *testing.T) {
	d := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = d
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("%v %s", e, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(d, "README.md"), []byte("# Fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-m", "Seed")
	base := git("rev-parse", "main")
	bin := t.TempDir()
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(os.Args[0], "'", "'\"'\"'") + "' -test.run=^TestBuiltInCLIHelper$ -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ORCHESTRATOR_NATIVE_UI_HELPER", "1")
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("ORCHESTRATOR_CALLS_FILE", calls)
	policy := controller.Config{Validators: map[string]execution.Command{"exists": {Program: "test", Args: []string{"-s", "docs/readme.txt"}}, "diff-check": {Program: "git", Args: []string{"diff", "--check", "HEAD~1", "HEAD"}}}, RequiredValidators: []string{"exists", "diff-check"}, MaxWorkers: 2}
	b, _ := json.Marshal(policy)
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--workspace", d, "--config", path, "--planner", "codex", "--workers", "codex"}, strings.NewReader("yes\nWrite a fixture document\nyes\nyes\nleave\n"), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Planner activated: Codex") || !strings.Contains(out.String(), "AWAITING_REVIEW") {
		t.Fatalf("%s", out.String())
	}
	s, err := scheduler.Read(d)
	if err != nil {
		t.Fatal(err)
	}
	objectiveID := ""
	for id, o := range s.Objectives {
		objectiveID = id
		if o.PlannerProvider != "codex" || len(o.WorkerProviders) != 1 {
			t.Fatalf("%+v", o)
		}
	}
	if err := run(context.Background(), []string{"--workspace", d, "--config", path, "--resume", objectiveID}, strings.NewReader("leave\n"), &out); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(calls)
	if err != nil || string(b) != "planner\nworker\n" {
		t.Fatalf("restart invoked agents again: %q %v", b, err)
	}
	if git("rev-parse", "main") != base {
		t.Fatal("dynamic activation integrated work")
	}
}

func TestInteractiveObjectiveThroughHumanReview(t *testing.T) {
	d := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = d
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("%v %s", e, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	if e := os.WriteFile(filepath.Join(d, "README.md"), []byte("# Fixture\n"), 0644); e != nil {
		t.Fatal(e)
	}
	git("add", "README.md")
	git("commit", "-m", "Seed")
	base := git("rev-parse", "HEAD")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := `{"plan":{"id":"proposed","objective":"Write a fixture document","tasks":[{"id":"docs","feature":"docs","description":"Write fixture document","scope":"docs","acceptance_criteria":["Document exists"],"validation":["exists"],"capability":"coding"}]}}`
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": p}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	c := controller.Config{Planner: planner.Provider{Endpoint: server.URL, Model: "test"}, Workers: map[string]controller.Worker{"coding": {Program: "sh", Args: []string{"-c", "mkdir docs && printf 'Fixture document\\n' > docs/readme.txt && git add docs/readme.txt && git commit -m 'Document fixture'"}}}, Validators: map[string]execution.Command{"exists": {Program: "test", Args: []string{"-s", "docs/readme.txt"}}}, MaxWorkers: 2}
	b, _ := json.Marshal(c)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--workspace", d, "--config", path}, strings.NewReader("yes\nWrite a fixture document\nyes\nyes\napprove\n"), &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "AWAITING_REVIEW") || !strings.Contains(out.String(), "Review recorded: APPROVED") || strings.Contains(out.String(), "claim-") {
		t.Fatalf("output=%s", out.String())
	}
	if git("rev-parse", "main") != base {
		t.Fatal("Planner merged")
	}
	s, err := scheduler.Read(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range s.Objectives {
		if o.Status != "APPROVED" {
			t.Fatalf("%+v", o)
		}
	}
}

func TestBlockedRecoveryRequiresExplicitHumanAuthorization(t *testing.T) {
	for _, authorize := range []bool{false, true} {
		t.Run(fmt.Sprintf("authorize=%v", authorize), func(t *testing.T) {
			d := t.TempDir()
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = d
				b, err := cmd.CombinedOutput()
				if err != nil { t.Fatalf("%v %s", err, b) }
				return strings.TrimSpace(string(b))
			}
			git("init", "-b", "main")
			git("config", "user.email", "test@example.com")
			git("config", "user.name", "Test")
			if err := os.WriteFile(filepath.Join(d, "README.md"), []byte("# Fixture\n"), 0644); err != nil { t.Fatal(err) }
			git("add", "README.md")
			git("commit", "-m", "Seed")
			if _, err := enrollment.Enroll(d, enrollment.Native, "test"); err != nil { t.Fatal(err) }
			p := objective.Plan{ID: "blocked-objective", Objective: "recover work", Tasks: []objective.Task{{ID: "blocked", Feature: "blocked", Scope: "docs"}}}
			first, err := scheduler.Acquire(scheduler.Request{Workspace: d, Agent: "one", Role: "coding-agent", Base: "main", Plan: p})
			if err != nil { t.Fatal(err) }
			if err := coordination.Transition(d, first.SessionID, "BLOCKED"); err != nil { t.Fatal(err) }
			if err := scheduler.Block(d, p.ID, first, "validation failed"); err != nil { t.Fatal(err) }

			var out bytes.Buffer
			answer := "\n"
			if authorize { answer = "yes\n" }
			if err := authorizeBlockedRecovery(bufio.NewReader(strings.NewReader(answer)), &out, d, p.ID); err != nil { t.Fatal(err) }
			s, err := scheduler.Read(d)
			if err != nil { t.Fatal(err) }
			task := s.Tasks[p.ID+"::blocked"]
			if authorize {
				if task.Status != "PENDING" || len(task.AssignmentHistory) != 1 {
					t.Fatalf("authorized recovery not persisted: %+v", task)
				}
				if !strings.Contains(out.String(), "Recovery authorized") { t.Fatalf("output=%s", out.String()) }
			} else {
				if task.Status != "BLOCKED" || task.Attempt != 1 || task.Token != first.Token {
					t.Fatalf("silence changed blocked ownership: %+v", task)
				}
			}
		})
	}
}
