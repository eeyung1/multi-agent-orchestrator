package coordination

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompleteTransitionRejectsOutstandingClaims(t *testing.T) {
	root := setup(t)
	if err := ClaimScope(root, Claim{SessionID: "agent-1", Feature: "F", Scope: "one", Token: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := Transition(root, "agent-1", "WORKING"); err != nil {
		t.Fatal(err)
	}
	if err := Transition(root, "agent-1", "COMPLETE"); err == nil {
		t.Fatal("COMPLETE must reject outstanding claims")
	}
	data, err := os.ReadFile(filepath.Join(root, "multi-agent-orchestrator", "agentmeeting.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "| agent-1 | backend | WORKING |") || !strings.Contains(string(data), "| one | agent-1 |") {
		t.Fatalf("ledger=%s", data)
	}
}

func TestCompletedSessionCannotAcquireClaims(t *testing.T) {
	for _, handoff := range []bool{false, true} {
		t.Run(fmt.Sprintf("handoff=%v", handoff), func(t *testing.T) {
			root := setup(t)
			if err := Transition(root, "agent-2", "WORKING"); err != nil {
				t.Fatal(err)
			}
			if err := Transition(root, "agent-2", "COMPLETE"); err != nil {
				t.Fatal(err)
			}
			var err error
			if handoff {
				if err := ClaimScope(root, Claim{SessionID: "agent-1", Feature: "F", Scope: "one", Token: "one"}); err != nil {
					t.Fatal(err)
				}
				err = Handoff(root, "agent-1", "one", "agent-2")
			} else {
				err = ClaimScope(root, Claim{SessionID: "agent-2", Feature: "F", Scope: "one", Token: "one"})
			}
			if err == nil {
				t.Fatal("completed session acquired a claim")
			}
		})
	}
}

func TestReleaseClaimRemainsTokenAndSessionSpecific(t *testing.T) {
	root := setup(t)
	for _, claim := range []Claim{
		{SessionID: "agent-1", Feature: "F", Scope: "one", Token: "one"},
		{SessionID: "agent-1", Feature: "F", Scope: "two", Token: "two"},
		{SessionID: "agent-2", Feature: "G", Scope: "other", Token: "one"},
	} {
		if err := ClaimScope(root, claim); err != nil {
			t.Fatal(err)
		}
	}
	if err := ReleaseClaim(root, "agent-1", "one"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "multi-agent-orchestrator", "agentmeeting.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "| one | agent-1 |") || !strings.Contains(string(data), "| two | agent-1 |") || !strings.Contains(string(data), "| one | agent-2 |") {
		t.Fatalf("ledger=%s", data)
	}
}

func TestCompleteSessionRecordsBeforeAtomicLedgerPublication(t *testing.T) {
	root := setup(t)
	if err := Transition(root, "agent-1", "WORKING"); err != nil {
		t.Fatal(err)
	}
	for _, claim := range []Claim{
		{SessionID: "agent-1", Feature: "F", Scope: "one", Token: "one"},
		{SessionID: "agent-1", Feature: "F", Scope: "two", Token: "two"},
		{SessionID: "agent-2", Feature: "G", Scope: "other", Token: "one"},
	} {
		if err := ClaimScope(root, claim); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(root, "multi-agent-orchestrator", "agentmeeting.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	recorded := false
	err = CompleteSession(root, "agent-1", "one", func() error {
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(current) != string(before) {
			t.Fatal("state/claims changed before evidence")
		}
		recorded = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(after)
	if !recorded || !strings.Contains(text, "| agent-1 | backend | COMPLETE |") || strings.Contains(text, "| one | agent-1 |") || strings.Contains(text, "| two | agent-1 |") || !strings.Contains(text, "| one | agent-2 |") {
		t.Fatalf("ledger=%s", text)
	}
}

func TestCompleteSessionPublicationFailureRetainsPreviousLedger(t *testing.T) {
	root := setup(t)
	if err := Transition(root, "agent-1", "WORKING"); err != nil {
		t.Fatal(err)
	}
	for _, claim := range []Claim{
		{SessionID: "agent-1", Feature: "F", Scope: "one", Token: "one"},
		{SessionID: "agent-1", Feature: "F", Scope: "two", Token: "two"},
		{SessionID: "agent-2", Feature: "G", Scope: "other", Token: "other"},
	} {
		if err := ClaimScope(root, claim); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(root, "multi-agent-orchestrator")
	before, err := os.ReadFile(filepath.Join(dir, "agentmeeting.md"))
	if err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(root, "retained-coordination")
	err = CompleteSession(root, "agent-1", "one", func() error {
		if err := os.WriteFile(filepath.Join(dir, "resolution.md"), []byte("validated completion evidence\n"), 0o644); err != nil {
			return err
		}
		// Simulate storage becoming unavailable after evidence was recorded.
		// The old ledger remains readable in saved; the publication path is blocked.
		if err := os.Rename(dir, saved); err != nil {
			return err
		}
		return os.WriteFile(dir, []byte("not a directory"), 0o644)
	})
	if err == nil || !strings.Contains(err.Error(), "publish completion and release session claims") {
		t.Fatalf("err=%v", err)
	}
	after, readErr := os.ReadFile(filepath.Join(saved, "agentmeeting.md"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(before) != string(after) {
		t.Fatalf("failed publication changed ledger=%s", after)
	}
	evidence, readErr := os.ReadFile(filepath.Join(saved, "resolution.md"))
	if readErr != nil || len(evidence) == 0 {
		t.Fatalf("evidence=%s err=%v", evidence, readErr)
	}
}

func TestCompletionClaimParsingFailsClosedAndPreservesOtherSessions(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprintf("malformed=%v", malformed), func(t *testing.T) {
			root := setup(t)
			if err := Transition(root, "agent-1", "WORKING"); err != nil {
				t.Fatal(err)
			}
			if err := ClaimScope(root, Claim{SessionID: "agent-1", Feature: "F", Scope: "one", Token: "one"}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "multi-agent-orchestrator", "agentmeeting.md")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if malformed {
				data = append(data, []byte("| broken | agent-1 |\n")...)
			}
			history := "\n## History\n\n| historical | agent-2 | F | old-scope | old |\n"
			data = append(data, []byte(history)...)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			recorded := false
			err = CompleteSession(root, "agent-1", "one", func() error { recorded = true; return nil })
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if malformed {
				if err == nil || recorded || string(after) != string(data) {
					t.Fatalf("err=%v recorded=%v ledger=%s", err, recorded, after)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !recorded || !strings.Contains(string(after), history) || strings.Contains(string(after), "| one | agent-1 |") {
					t.Fatalf("ledger=%s", after)
				}
			}
		})
	}
}

func TestCompletionPreservesRegistrationsAppendedAfterClaims(t *testing.T) {
	root := setup(t)
	if err := ClaimScope(root, Claim{SessionID: "agent-1", Feature: "F", Scope: "one", Token: "one"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "multi-agent-orchestrator", "agentmeeting.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Match Activate's append-only registration in an existing claimed ledger.
	data = append(data, []byte("| agent-3 | backend | ACTIVE | old | activation | - | bootstrap |\n")...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ClaimScope(root, Claim{SessionID: "agent-3", Feature: "G", Scope: "old", Token: "three"}); err != nil {
		t.Fatal(err)
	}
	if err := Transition(root, "agent-3", "WORKING"); err != nil {
		t.Fatal(err)
	}
	if err := CompleteSession(root, "agent-3", "three", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "| agent-3 | backend | COMPLETE |") || strings.Contains(string(data), "| three | agent-3 |") || !strings.Contains(string(data), "| one | agent-1 |") {
		t.Fatalf("ledger=%s", data)
	}
}

func TestCompletionIncludesClaimsAppendedAfterLaterHeadings(t *testing.T) {
	root := setup(t)
	if err := Transition(root, "agent-1", "WORKING"); err != nil {
		t.Fatal(err)
	}
	if err := ClaimScope(root, Claim{SessionID: "agent-1", Feature: "F", Scope: "one", Token: "one"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "multi-agent-orchestrator", "agentmeeting.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n## Notes\n\nPreserve these notes.\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ClaimScope(root, Claim{SessionID: "agent-1", Feature: "extra", Scope: "two", Token: "two"}); err != nil {
		t.Fatal(err)
	}
	if err := CompleteSession(root, "agent-1", "one", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "| two | agent-1 |") {
		t.Fatalf("COMPLETE session retains an appended claim: %s", data)
	}
	if !strings.Contains(string(data), "Preserve these notes.") {
		t.Fatalf("notes removed: %s", data)
	}
}
