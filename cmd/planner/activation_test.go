package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/controller"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

func fakeProviders(t *testing.T, ids ...string) string {
	t.Helper()
	bin := t.TempDir()
	for _, id := range ids {
		if err := os.WriteFile(filepath.Join(bin, id), []byte("#!/bin/sh\nexit 77\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	return bin
}

func TestExplicitPlannerAndMultipleWorkers(t *testing.T) {
	bin := fakeProviders(t, "codex", "claude", "gemini")
	var c controller.Config
	var out bytes.Buffer
	if err := activate(bufio.NewReader(strings.NewReader("")), &out, t.TempDir(), &c, "claude", "codex,gemini", state.Objective{}); err != nil {
		t.Fatal(err)
	}
	if c.Planner.Adapter != "claude" || c.Planner.Program != filepath.Join(bin, "claude") || len(c.Workers) != 2 || c.Workers["gemini"].Adapter != "gemini" {
		t.Fatalf("%+v", c)
	}
	if strings.Contains(out.String(), "claim-") || strings.Contains(out.String(), "generation") {
		t.Fatal("bookkeeping exposed")
	}
}

func TestInteractivePlannerAndWorkerPool(t *testing.T) {
	fakeProviders(t, "codex", "claude", "gemini")
	var c controller.Config
	var out bytes.Buffer
	if err := activate(bufio.NewReader(strings.NewReader("2\n1,3\n")), &out, t.TempDir(), &c, "", "", state.Objective{}); err != nil {
		t.Fatal(err)
	}
	if c.Planner.Adapter != "claude" || len(c.Workers) != 2 || !strings.Contains(out.String(), "Available Planner agents") {
		t.Fatalf("%+v %s", c, out.String())
	}
}

func TestUnavailableAndUnknownProvidersFailClosed(t *testing.T) {
	fakeProviders(t, "codex")
	for _, id := range []string{"claude", "sh"} {
		var c controller.Config
		var out bytes.Buffer
		if err := activate(bufio.NewReader(strings.NewReader("")), &out, t.TempDir(), &c, id, "codex", state.Objective{}); err == nil {
			t.Fatalf("accepted %s", id)
		}
	}
	t.Setenv("PATH", t.TempDir())
	var c controller.Config
	var out bytes.Buffer
	if err := activate(bufio.NewReader(strings.NewReader("")), &out, t.TempDir(), &c, "", "", state.Objective{}); err == nil || !strings.Contains(err.Error(), "no compatible Planner") {
		t.Fatalf("%v", err)
	}
}

func TestSavedSelectionRestoresWithoutChangingAuthority(t *testing.T) {
	fakeProviders(t, "codex", "claude")
	saved := state.Objective{ID: "existing", PlannerProvider: "claude", WorkerProviders: []string{"codex"}, PolicyDigest: "unchanged", AuthorizedBy: "human"}
	var c controller.Config
	var out bytes.Buffer
	if err := activate(bufio.NewReader(strings.NewReader("")), &out, t.TempDir(), &c, "", "", saved); err != nil {
		t.Fatal(err)
	}
	if c.Planner.Adapter != "claude" || c.Workers["codex"].Adapter != "codex" || saved.PolicyDigest != "unchanged" || saved.AuthorizedBy != "human" {
		t.Fatal("saved authority/selection changed")
	}
	if err := activate(bufio.NewReader(strings.NewReader("")), &out, t.TempDir(), &c, "codex", "", saved); err == nil {
		t.Fatal("silent provider switch on resume")
	}
}

func TestBootstrapNeverDropsValidationAndSavesPolicyOnly(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "policy.json")
	var c controller.Config
	var out bytes.Buffer
	if err := bootstrap(bufio.NewReader(strings.NewReader("1\nno\n")), &out, project, path, &c); err == nil {
		t.Fatal("unapproved checks admitted")
	}
	if err := bootstrap(bufio.NewReader(strings.NewReader("1\nyes\nno\nyes\n")), &out, project, path, &c); err != nil {
		t.Fatal(err)
	}
	loaded, err := controller.ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.RequiredValidators) != 2 || len(loaded.Workers) != 0 || loaded.Planner.Program != "" || len(loaded.WorktreePreparation) != 0 {
		t.Fatalf("bootstrap saved provider ownership/config instead of policy: %+v", loaded)
	}
	if err := loaded.ValidatePolicy(); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapSavesOnlyExplicitlyAuthorizedPreparation(t *testing.T) {
	project := t.TempDir()
	bin := t.TempDir()
	program := filepath.Join(bin, "prepare-fixture")
	marker := filepath.Join(bin, "executed")
	if err := os.WriteFile(program, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := filepath.Join(t.TempDir(), "policy.json")
	var c controller.Config
	var out bytes.Buffer
	input := "custom\ngit\n[\"diff\",\"--check\"]\nyes\nyes\nprepare-fixture\n[\"--directory\",\"frontend\",\"install;literal\"]\nyes\nyes\n"
	if err := bootstrap(bufio.NewReader(strings.NewReader(input)), &out, project, path, &c); err != nil {
		t.Fatal(err)
	}
	loaded, err := controller.ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.WorktreePreparation) != 1 || loaded.WorktreePreparation[0].Program != program || strings.Join(loaded.WorktreePreparation[0].Args, "|") != "--directory|frontend|install;literal" || len(loaded.RequiredValidators) != 2 {
		t.Fatalf("authorized preparation/validation did not survive saving: %+v", loaded)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("interactive setup executed preparation")
	}
	if !strings.Contains(out.String(), "Authorize this exact worktree preparation command and arguments?") {
		t.Fatal("missing explicit preparation authorization")
	}
}

func TestPreparationSetupRequiresExactCommandAuthorization(t *testing.T) {
	for _, input := range []string{
		"yes\ngit\n[]\nno\n",
		"yes\ngit\n[]\n",
		"yes\ngit\nnull\n",
		"yes\ngit\n\"diff --check\"\n",
		"yes\ngit diff --check\n[]\nyes\n",
	} {
		var c controller.Config
		var out bytes.Buffer
		if err := preparationPolicy(bufio.NewReader(strings.NewReader(input)), &out, t.TempDir(), &c); err == nil {
			t.Fatalf("accepted unauthorized or invalid preparation: %q", input)
		}
		if len(c.WorktreePreparation) != 0 {
			t.Fatal("unauthorized preparation added to trusted policy")
		}
	}
	var c controller.Config
	var out bytes.Buffer
	if err := preparationPolicy(bufio.NewReader(strings.NewReader("no\n")), &out, t.TempDir(), &c); err != nil || len(c.WorktreePreparation) != 0 {
		t.Fatalf("optional preparation was required: %+v %v", c, err)
	}
	// A deliberately empty argv is valid for a trusted executable.
	if err := preparationPolicy(bufio.NewReader(strings.NewReader("yes\ngit\n[]\nyes\n")), &out, t.TempDir(), &c); err != nil || len(c.WorktreePreparation) != 1 {
		t.Fatalf("explicit empty arguments rejected: %+v %v", c, err)
	}
}

func TestProjectCustomConfigurationNeedsExplicitTrust(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, "policy.json")
	if err := os.WriteFile(path, []byte(`{"workers":{"coding":{"program":"project-controlled"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := customConfigTrust(bufio.NewReader(strings.NewReader("yes\n")), &out, path, project, false); err == nil {
		t.Fatal("implicit project configuration trusted")
	}
	if err := customConfigTrust(bufio.NewReader(strings.NewReader("")), &out, path, project, true); err == nil {
		t.Fatal("silence trusted executable policy")
	}
	if err := customConfigTrust(bufio.NewReader(strings.NewReader("yes\n")), &out, path, project, true); err != nil {
		t.Fatal(err)
	}
}
