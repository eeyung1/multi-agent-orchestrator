package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/controller"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
)

func TestConfigurePreparationPolicyRequiresExactHumanAuthorizationAndPreservesPolicy(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(t.TempDir(), "planner.json")
	base := controller.Config{
		Validators: map[string]execution.Command{"check": {Program: "git", Args: []string{"diff", "--check"}}},
		RequiredValidators: []string{"check"},
		AllowedScopes: []string{"."},
		MaxWorkers: 2,
		TimeoutSeconds: 60,
		HeartbeatSeconds: 10,
	}
	if err := savePolicy(path, base, false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	input := "yes\ngit\n[\"--version\"]\nyes\nyes\n"
	if err := configurePreparationPolicy(bufio.NewReader(strings.NewReader(input)), &out, workspace, path, &base); err != nil {
		t.Fatal(err)
	}
	got, err := controller.ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.WorktreePreparation) != 1 || filepath.Base(got.WorktreePreparation[0].Program) != "git" ||
		len(got.WorktreePreparation[0].Args) != 1 || got.WorktreePreparation[0].Args[0] != "--version" {
		t.Fatalf("preparation policy not saved: %+v", got.WorktreePreparation)
	}
	if len(got.Validators) != 1 || got.MaxWorkers != 2 || got.TimeoutSeconds != 60 || got.HeartbeatSeconds != 10 {
		t.Fatalf("existing trusted policy changed unexpectedly: %+v", got)
	}
}

func TestConfigurePreparationPolicyRefusalLeavesConfigUnchanged(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(t.TempDir(), "planner.json")
	base := controller.Config{
		Validators: map[string]execution.Command{"check": {Program: "git", Args: []string{"diff", "--check"}}},
		RequiredValidators: []string{"check"},
		MaxWorkers: 1,
	}
	if err := savePolicy(path, base, false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// Authorize the exact command, then refuse replacement of the trusted file.
	input := "yes\ngit\n[\"--version\"]\nyes\nno\n"
	if err := configurePreparationPolicy(bufio.NewReader(strings.NewReader(input)), &out, workspace, path, &base); err == nil {
		t.Fatal("policy replacement succeeded without final authorization")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refused policy update changed the trusted config")
	}
}
