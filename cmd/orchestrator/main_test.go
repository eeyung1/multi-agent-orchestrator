package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLICompletionAndIndividualRelease(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "orchestrator")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	for _, operation := range []string{"release", "complete"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			coord := filepath.Join(root, "multi-agent-orchestrator")
			if err := os.MkdirAll(coord, 0o755); err != nil {
				t.Fatal(err)
			}
			meeting := "# Agent Meeting\n\n## Active Agents\n\n| Agent/session ID | Role | Status | Heartbeat UTC | Feature | Branch/worktree | Claim reference |\n| --- | --- | --- | --- | --- | --- | --- |\n| a1 | backend | WORKING | old | F | branch | one |\n| a10 | reviewer | WORKING | old | G | branch | one |\n\n## Atomic Claims\n\n| Token | Agent/session ID | Feature | Scope | Claimed UTC |\n| --- | --- | --- | --- | --- |\n| one | a1 | F | one | old |\n| two | a1 | F | two | old |\n| one | a10 | G | other | old |\n"
			path := filepath.Join(coord, "agentmeeting.md")
			if err := os.WriteFile(path, []byte(meeting), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(coord, "resolution.md"), []byte("# Resolution\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"--" + operation, "--workspace", root, "--session", "a1", "--token", "one"}
			if operation == "complete" {
				args = append(args, "--worktree", root, "--feature", "F", "--validate-program", "printf", "--validate-arg", "ok")
			}
			cmd := exec.Command(binary, args...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v: %s", operation, err, out)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if strings.Contains(text, "| one | a1 |") || !strings.Contains(text, "| one | a10 | G | other | old |") {
				t.Fatalf("ledger=%s", text)
			}
			if operation == "release" {
				if !strings.Contains(text, "| two | a1 |") || !strings.Contains(text, "| a1 | backend | WORKING |") {
					t.Fatalf("release changed other ownership/state: %s", text)
				}
			} else if strings.Contains(text, "| two | a1 |") || !strings.Contains(text, "| a1 | backend | COMPLETE |") {
				t.Fatalf("completion left claims: %s", text)
			}
		})
	}
}
