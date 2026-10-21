package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
)

func TestRunReleasesClaimsAddedDuringExecution(t *testing.T) {
	root := repo(t)
	binary := filepath.Join(t.TempDir(), "orchestrator")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/orchestrator")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	script := `"$1" --workspace "$2" --session "$ORCHESTRATOR_SESSION_ID" --feature extra --scope extra.txt --token extra-token
printf implemented > result.txt`
	got, err := Run(Request{Workspace: root, Agent: "Codex", Role: "backend", Feature: "multi", Scope: "result.txt", Token: "run-token", Base: "main", AgentCommand: execution.Command{Program: "sh", Args: []string{"-c", script, "orchestrator-test", binary, root}}, Validate: execution.Command{Program: "sh", Args: []string{"-c", "test \"$(cat result.txt)\" = implemented"}}, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "multi-agent-orchestrator", "agentmeeting.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if got.State != "COMPLETE" || !strings.Contains(text, "| "+got.SessionID+" | backend | COMPLETE |") || strings.Contains(text, "| run-token | ") || strings.Contains(text, "| extra-token | ") {
		t.Fatalf("got=%+v ledger=%s", got, text)
	}
}
