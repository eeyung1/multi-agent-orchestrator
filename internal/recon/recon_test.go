package recon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
)

func TestReconIncludesInstructionsTestsRoadmapAndExcludesSecrets(t *testing.T) {
	d := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = d
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, b)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	for path, content := range map[string]string{"AGENTS.md": "Run tests and follow conventions", "roadmap.md": "- [ ] Verify privacy", "privacy_test.go": "package privacy\n", ".env": "SECRET_VALUE_SHOULD_NOT_BE_SENT"} {
		if err := os.WriteFile(filepath.Join(d, path), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "AGENTS.md", "roadmap.md", "privacy_test.go")
	git("commit", "-m", "Seed")
	if _, err := enrollment.Enroll(d, enrollment.Native, "test"); err != nil {
		t.Fatal(err)
	}
	r, err := Inspect(d, "Verify privacy")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if len(r.Instructions) != 1 || !strings.Contains(string(b), "roadmap.md") || !strings.Contains(string(b), "privacy_test.go") || strings.Contains(string(b), "SECRET_VALUE") {
		t.Fatalf("%s", b)
	}
	if err := os.WriteFile(filepath.Join(d, "AGENTS.md"), []byte("Changed project convention"), 0644); err != nil {
		t.Fatal(err)
	}
	r2, err := Inspect(d, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.InstructionDigest == r2.InstructionDigest {
		t.Fatal("instruction drift not detected")
	}
}
