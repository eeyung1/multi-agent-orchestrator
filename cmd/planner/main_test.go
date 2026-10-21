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

	"github.com/eeyung1/multi-agent-orchestrator/internal/controller"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
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
