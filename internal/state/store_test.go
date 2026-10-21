package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAndTransactionalRollback(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root, "repo:test")
	if err != nil { t.Fatal(err) }
	before, err := s.Read()
	if err != nil { t.Fatal(err) }
	err = s.Update("test", "rollback", func(snap *Snapshot) error {
		snap.Sessions["x"] = Session{ID: "x"}
		return os.ErrPermission
	})
	if err == nil { t.Fatal("expected rollback error") }
	after, err := s.Read()
	if err != nil { t.Fatal(err) }
	if _, ok := after.Sessions["x"]; ok { t.Fatal("failed transaction mutated durable state") }
	if after.Authority.Generation != before.Authority.Generation { t.Fatal("failed transaction advanced generation") }
}

func TestOpenRejectsDifferentRepository(t *testing.T) {
	root := t.TempDir()
	if _, err := Open(root, "repo:a"); err != nil { t.Fatal(err) }
	if _, err := Open(root, "repo:b"); err == nil { t.Fatal("expected repository mismatch") }
}

func TestCanonicalScope(t *testing.T) {
	root := t.TempDir()
	for _, bad := range []string{"", "../x", filepath.Join("..", "x"), filepath.Join(root, "x")} {
		if _, err := CanonicalScope(root, bad); err == nil { t.Fatalf("accepted unsafe scope %q", bad) }
	}
	got, err := CanonicalScope(root, "backend/../frontend/src")
	if err != nil { t.Fatal(err) }
	if got != "frontend/src" { t.Fatalf("got %q", got) }
}

func TestConcurrentInitializationFailsClosedWithoutCorruption(t *testing.T) {
	root := t.TempDir()
	if _, err := Open(root, "repo:test"); err != nil { t.Fatal(err) }
	lock := filepath.Join(root, lockDir)
	if err := os.Mkdir(lock, 0o700); err != nil { t.Fatal(err) }
	defer os.Remove(lock)
	s := &Store{primary: root, dir: filepath.Join(root, "multi-agent-orchestrator"), path: filepath.Join(root, "multi-agent-orchestrator", stateFile)}
	if err := s.Update("test", "busy", func(*Snapshot) error { return nil }); err == nil {
		t.Fatal("expected locked update to fail closed")
	}
	if _, err := s.Read(); err != nil { t.Fatalf("state corrupted by contention: %v", err) }
}

func TestOpenRefusesAmbiguousLegacyOperationalState(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "multi-agent-orchestrator")
	if err := os.MkdirAll(dir, 0o755); err != nil { t.Fatal(err) }
	legacy := "# Agent Meeting\n\n| old-session | coding-agent | WORKING | now | feature | branch | token |\n"
	if err := os.WriteFile(filepath.Join(dir, "agentmeeting.md"), []byte(legacy), 0o644); err != nil { t.Fatal(err) }
	if _, err := Open(root, "repo:test"); err == nil {
		t.Fatal("expected ambiguous legacy state to fail closed")
	}
}
