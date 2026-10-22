package planner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/recon"
)

func TestNaturalObjectiveProducesProposalOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "test-model" || len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "Improve privacy") || !strings.Contains(body.Messages[1].Content, "AGENTS.md") || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("objective/recon/credentials not delivered")
		}
		content := `{"plan":{"id":"privacy","objective":"Improve privacy","tasks":[{"id":"audit","feature":"audit","description":"Verify privacy handling","scope":"tests/privacy","depends_on":[],"priority":3,"acceptance_criteria":["Privacy checks pass"],"validation":["privacy"],"capability":"coding"}]},"questions":[]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	t.Setenv("TEST_PLANNING_KEY", "test-key")
	p, err := (Provider{Endpoint: server.URL, Model: "test-model", APIKeyEnv: "TEST_PLANNING_KEY"}).Propose(context.Background(), Request{Objective: "Improve privacy", Recon: recon.Report{Instructions: []recon.File{{Path: "AGENTS.md", Content: "Run project checks"}}}, Capabilities: []string{"coding"}, Validators: []string{"privacy"}, AllowedScopes: []string{"tests"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Plan.Tasks) != 1 || p.Plan.Tasks[0].ID != "audit" || len(p.Plan.Tasks[0].AcceptanceCriteria) != 1 {
		t.Fatalf("%+v", p)
	}
}
func TestDecisionsAndAuthorityFields(t *testing.T) {
	p, err := Decode([]byte(`{"questions":["May the new check use an external paid service?"]}`))
	if err != nil || len(p.Questions) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	for _, data := range []string{`{"approved":true}`, `{"plan":{"id":"p","ownership":"mine"}}`, `{"plan":{"id":"p","tasks":[{"id":"a","program":"/bin/sh"}]}}`, `{"questions":[]} {"questions":[]}`, `{"questions":[""]}`} {
		if _, err := Decode([]byte(data)); err == nil {
			t.Fatalf("accepted authority/invalid response %s", data)
		}
	}
	for _, data := range []string{
		`{"worktree_preparation":[{"Program":"install","Args":[]}]}`,
		`{"plan":{"worktree_preparation":[{"Program":"install","Args":[]}]}}`,
		`{"plan":{"tasks":[{"id":"a","worktree_preparation":[{"Program":"install","Args":[]}]}]}}`,
	} {
		if _, err := Decode([]byte(data)); err == nil {
			t.Fatalf("model defined executable preparation policy: %s", data)
		}
	}
}

func TestNativeCodexProposalUsesFinalFileAndReadOnlyPolicy(t *testing.T) {
	bin := t.TempDir()
	program := filepath.Join(bin, "codex")
	script := `#!/bin/sh
answer=''
seen_readonly=0
while [ "$#" -gt 0 ]; do
 case "$1" in
 --output-last-message) shift; answer="$1" ;;
 read-only) seen_readonly=1 ;;
 esac
 shift
done
[ "$seen_readonly" = 1 ] || exit 3
input=$(/bin/cat)
case "$input" in *"never a coordination authority"*) ;; *) exit 4 ;; esac
printf '%s' '{"questions":["Which acceptance outcome?"]}' > "$answer"
printf '%s' 'diagnostic output is not the proposal'
`
	if err := os.WriteFile(program, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	p, err := (Provider{Adapter: "codex", Program: program}).Propose(context.Background(), Request{Objective: "work", Recon: recon.Report{Workspace: t.TempDir()}})
	if err != nil || len(p.Questions) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := (Provider{Adapter: "codex", Program: program, Args: []string{"--unsafe-override"}}).Propose(context.Background(), Request{}); err == nil {
		t.Fatal("built-in argument injection accepted")
	}
}
func TestProviderErrorsAreBoundedAndDoNotExposeResponseSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = io.WriteString(w, "private-key-in-server-response")
	}))
	defer server.Close()
	_, err := (Provider{Endpoint: server.URL, Model: "test"}).Propose(context.Background(), Request{Objective: "work"})
	if err == nil || strings.Contains(err.Error(), "private-key") {
		t.Fatalf("%v", err)
	}
}

func TestProviderRespectsCallerCancellation(t *testing.T) {
	bin := t.TempDir()
	program := filepath.Join(bin, "slow-planner")
	script := `#!/bin/sh
sleep 10
printf '%s' '{"questions":["finished"]}'
`
	if err := os.WriteFile(program, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := (Provider{
		Program: program,
	}).Propose(ctx, Request{Objective: "work"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected caller cancellation")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("expected caller deadline exceeded, got %v", ctx.Err())
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected provider error to preserve caller deadline, got %v", err)
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected cancellation diagnostic, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("provider did not stop promptly after caller cancellation: %s", elapsed)
	}
}
