package activation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActivateBootstrapsAndRegisters(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Activate(root, "Codex", "backend")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "ACTIVE" {
		t.Fatalf("state = %q, want ACTIVE", result.State)
	}
	if !strings.HasPrefix(result.SessionID, "Codex-") {
		t.Fatalf("session id = %q", result.SessionID)
	}

	meeting, err := os.ReadFile(filepath.Join(root, coordinationDir, "agentmeeting.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(meeting), result.SessionID) || !strings.Contains(string(meeting), "| backend | ACTIVE |") {
		t.Fatalf("agent registration missing: %s", meeting)
	}
	for _, name := range []string{"coordination.md", "resolution.md"} {
		if _, err := os.Stat(filepath.Join(root, coordinationDir, name)); err != nil {
			t.Fatalf("%s not created: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, lockDir)); !os.IsNotExist(err) {
		t.Fatalf("bootstrap lock was not released")
	}
}

func TestActivatePreservesExistingLogs(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	coord := filepath.Join(root, coordinationDir)
	if err := os.MkdirAll(coord, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(coord, "resolution.md")
	const history = "# Resolution\n\nexisting history\n"
	if err := os.WriteFile(path, []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Activate(root, "Claude", "reviewer"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != history {
		t.Fatalf("existing resolution log changed: %q", got)
	}
}

func TestActivateRefusesExistingBootstrapLock(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, lockDir), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := Activate(root, "Codex", "backend"); err == nil {
		t.Fatal("expected activation to fail while bootstrap lock exists")
	}
}
