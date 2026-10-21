package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIdentityMatchesSubdirectory(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...); cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil { t.Fatalf("git %v: %s: %v", args, out, err) }
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("x"), 0o644); err != nil { t.Fatal(err) }
	run("add", "x"); run("commit", "-m", "init")
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil { t.Fatal(err) }
	a, err := Identity(root); if err != nil { t.Fatal(err) }
	b, err := Identity(sub); if err != nil { t.Fatal(err) }
	if a != b { t.Fatalf("identity mismatch: %q != %q", a, b) }
}
