package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func executable(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRegistryDetectionNeverExecutesProviders(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "probed")
	executable(t, bin, "codex", "printf probed > '"+marker+"'")
	executable(t, bin, "gemini", "exit 99")
	t.Setenv("PATH", bin)
	registry := Registry()
	if len(registry) != 3 {
		t.Fatal(registry)
	}
	registry[0].ID = "mutated"
	if Registry()[0].ID != "codex" {
		t.Fatal("registry was mutable")
	}
	detected := Discover(t.TempDir())
	if len(detected) != 3 || detected[0].Path == "" || detected[1].Path != "" || detected[2].Path == "" {
		t.Fatalf("%+v", detected)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("detection executed a CLI")
	}
	if _, err := Resolve("claude", ""); err == nil {
		t.Fatal("unavailable provider accepted")
	}
	if _, err := Resolve("arbitrary-shell", ""); err == nil {
		t.Fatal("unregistered executable accepted")
	}
}

func TestProjectExecutableAndInterpreterInjectionRejected(t *testing.T) {
	project := t.TempDir()
	local := filepath.Join(project, "bin")
	trusted := t.TempDir()
	executable(t, local, "codex", "exit 1")
	executable(t, local, "node", "exit 1")
	good := executable(t, trusted, "codex", "exit 0")
	t.Setenv("PATH", local+string(os.PathListSeparator)+"."+string(os.PathListSeparator)+trusted)
	d, err := Resolve("codex", project)
	if err != nil || d.Path != good {
		t.Fatalf("%+v %v", d, err)
	}
	if err := ValidateBinding("codex", filepath.Join(local, "codex"), project); err == nil {
		t.Fatal("project executable binding accepted")
	}
	for _, entry := range Environment(project) {
		if strings.HasPrefix(entry, "PATH=") && (strings.Contains(entry, local) || strings.Contains(entry, "=.")) {
			t.Fatalf("unsafe interpreter PATH: %s", entry)
		}
	}
	if err := os.Remove(good); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(local, "codex"), good); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve("codex", project); err == nil {
		t.Fatal("project executable symlink accepted")
	}
	t.Setenv("PATH", ".:relative-bin")
	if _, err := Resolve("codex", project); err == nil {
		t.Fatal("relative PATH was trusted")
	}
}

func TestFixedArgumentVectorsAndOutputEnvelopes(t *testing.T) {
	for _, a := range Registry() {
		plan, err := PlannerArgs(a.ID, "/tmp/answer")
		if err != nil {
			t.Fatal(err)
		}
		worker, err := WorkerArgs(a.ID)
		if err != nil {
			t.Fatal(err)
		}
		all := strings.Join(append(plan, worker...), " ")
		for _, forbidden := range []string{"dangerously-bypass", "dangerously-skip", "--yolo", "bypassPermissions", "danger-full-access", "{prompt}"} {
			if strings.Contains(all, forbidden) {
				t.Fatalf("unsafe adapter arguments %s", all)
			}
		}
		if a.ID == "codex" && (!strings.Contains(strings.Join(plan, " "), "--sandbox read-only") || !strings.Contains(strings.Join(worker, " "), "--sandbox workspace-write")) {
			t.Fatal("Codex sandbox policy changed")
		}
	}
	if _, err := WorkerArgs("bash"); err == nil {
		t.Fatal("model executable accepted")
	}
	for id, envelope := range map[string]string{"claude": `{"result":"{\"questions\":[\"Which behavior?\"]}","is_error":false}`, "gemini": `{"response":"{\"questions\":[\"Which behavior?\"]}"}`, "codex": `{"questions":["Which behavior?"]}`} {
		out, err := ProposalText(id, []byte(envelope))
		if err != nil || string(out) != `{"questions":["Which behavior?"]}` {
			t.Fatalf("%s: %s %v", id, out, err)
		}
	}
	if _, err := ProposalText("claude", []byte(`{"is_error":true,"result":"denied"}`)); err == nil {
		t.Fatal("provider failure accepted")
	}
}

func TestSeparateLinkedCheckoutCannotShadowProvider(t *testing.T) {
	project := t.TempDir()
	linked := t.TempDir()
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: /some/primary/.git/worktrees/linked\n"), 0644); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(linked, "bin")
	executable(t, local, "codex", "exit 99")
	trusted := t.TempDir()
	good := executable(t, trusted, "codex", "exit 0")
	t.Setenv("PATH", local+string(os.PathListSeparator)+trusted)
	d, err := Resolve("codex", project)
	if err != nil || d.Path != good {
		t.Fatalf("linked project executable was trusted: %+v %v", d, err)
	}
}

func TestGitManagedUserInstallationRemainsAvailable(t *testing.T) {
	installation := t.TempDir()
	if err := os.Mkdir(filepath.Join(installation, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installation, ".git", "HEAD"), []byte("ref: refs/heads/install\n"), 0644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(installation, "versions", "node", "bin")
	program := executable(t, bin, "codex", "exit 0")
	t.Setenv("PATH", bin)
	d, err := Resolve("codex", t.TempDir())
	if err != nil || d.Path != program {
		t.Fatalf("trusted Git-managed installation was hidden: %+v %v", d, err)
	}
}
