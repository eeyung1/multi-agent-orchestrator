package completion

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
)

func addSessionClaims(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "multi-agent-orchestrator", "agentmeeting.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), "## Atomic Claims", "| a10 | reviewer | WORKING | old | other | branch | t1 |\n\n## Atomic Claims", 1)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 21; i++ {
		if err := coordination.ClaimScope(root, coordination.Claim{SessionID: "a1", Feature: "extra", Scope: fmt.Sprintf("extra/%d", i), Token: fmt.Sprintf("t%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	// A different session can have the same token: ownership is the session/token pair.
	if err := coordination.ClaimScope(root, coordination.Claim{SessionID: "a10", Feature: "other", Scope: "other/file", Token: "t1"}); err != nil {
		t.Fatal(err)
	}
}

func meetingText(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "multi-agent-orchestrator", "agentmeeting.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func ownedClaims(text, session string) []string {
	var rows []string
	for _, line := range strings.Split(text, "\n") {
		parts := strings.Split(line, "|")
		if len(parts) == 7 && strings.TrimSpace(parts[2]) == session {
			rows = append(rows, line)
		}
	}
	return rows
}

func TestCompleteReleasesAll21ClaimsAndPreservesOtherSession(t *testing.T) {
	root, wt := setup(t)
	addSessionClaims(t, root)
	before := meetingText(t, root)
	got, err := Complete(Request{Primary: root, Worktree: wt, SessionID: "a1", Token: "t1", Feature: "F1", Validate: execution.Command{Program: "printf", Args: []string{"ok"}}})
	if err != nil {
		t.Fatal(err)
	}
	after := meetingText(t, root)
	if got.State != "COMPLETE" || !strings.Contains(after, "| a1 | backend | COMPLETE |") {
		t.Fatalf("got=%+v ledger=%s", got, after)
	}
	if claims := ownedClaims(after, "a1"); len(claims) != 0 {
		t.Fatalf("COMPLETE session retains %d claims: %v", len(claims), claims)
	}
	if strings.Join(ownedClaims(before, "a10"), "\n") != strings.Join(ownedClaims(after, "a10"), "\n") {
		t.Fatalf("other session's claims changed: %s", after)
	}
}

func TestValidationFailureRetainsAllSessionClaims(t *testing.T) {
	root, wt := setup(t)
	addSessionClaims(t, root)
	before := meetingText(t, root)
	got, err := Complete(Request{Primary: root, Worktree: wt, SessionID: "a1", Token: "t1", Feature: "F1", Validate: execution.Command{Program: "sh", Args: []string{"-c", "echo broken; exit 1"}}})
	after := meetingText(t, root)
	if err == nil || got.State != "BLOCKED" || !strings.Contains(after, "| a1 | backend | BLOCKED |") {
		t.Fatalf("got=%+v err=%v ledger=%s", got, err, after)
	}
	for _, session := range []string{"a1", "a10"} {
		if strings.Join(ownedClaims(before, session), "\n") != strings.Join(ownedClaims(after, session), "\n") {
			t.Fatalf("claims changed for %s", session)
		}
	}
	evidence, err := os.ReadFile(filepath.Join(root, "multi-agent-orchestrator", "resolution.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(evidence), "- State: BLOCKED") || strings.Contains(string(evidence), "- State: COMPLETE") {
		t.Fatalf("evidence=%s", evidence)
	}
}

func TestCompletionPreconditionsDoNotMutateStateOrEvidence(t *testing.T) {
	for _, scenario := range []string{"unknown token", "foreign token", "invalid lifecycle", "coordination locked", "evidence failure"} {
		t.Run(scenario, func(t *testing.T) {
			root, wt := setup(t)
			addSessionClaims(t, root)
			token := "t1"
			switch scenario {
			case "unknown token":
				token = "missing"
			case "foreign token":
				if err := coordination.ClaimScope(root, coordination.Claim{SessionID: "a10", Feature: "foreign", Scope: "foreign", Token: "foreign-only"}); err != nil {
					t.Fatal(err)
				}
				token = "foreign-only"
			case "invalid lifecycle":
				path := filepath.Join(root, "multi-agent-orchestrator", "agentmeeting.md")
				if err := os.WriteFile(path, []byte(strings.Replace(meetingText(t, root), "| a1 | backend | WORKING |", "| a1 | backend | ACTIVE |", 1)), 0o644); err != nil {
					t.Fatal(err)
				}
			case "coordination locked":
				if err := os.Mkdir(filepath.Join(root, ".agent-coordination-lock"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "evidence failure":
				path := filepath.Join(root, "multi-agent-orchestrator", "resolution.md")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			before := meetingText(t, root)
			got, err := Complete(Request{Primary: root, Worktree: wt, SessionID: "a1", Token: token, Feature: "F1", Validate: execution.Command{Program: "printf", Args: []string{"ok"}}})
			if err == nil || got.State == "COMPLETE" {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			if after := meetingText(t, root); after != before {
				t.Fatalf("failed completion changed ledger:\n%s", after)
			}
			if scenario != "evidence failure" {
				evidence, err := os.ReadFile(filepath.Join(root, "multi-agent-orchestrator", "resolution.md"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(evidence), "- State: COMPLETE") {
					t.Fatalf("premature success evidence=%s", evidence)
				}
			}
		})
	}
}
