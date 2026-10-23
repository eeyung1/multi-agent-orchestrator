package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
)

func TestWorkerFixtureRespectsBoundedScope(t *testing.T) {
	root := t.TempDir()
	w := agentprotocol.WorkerMessage{Assignment: &agentprotocol.WorkerAssignmentData{Worktree: root, Task: planning.Task{Scope: "owned"}}}
	if err := writeFixtureFiles(context.Background(), w, workerFixture{Files: map[string]string{"{scope}/result.txt": "bounded"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "owned", "result.txt"))
	if err != nil || string(b) != "bounded" {
		t.Fatal("fixture did not apply assigned scope")
	}
	for _, path := range []string{"outside.txt", "../outside.txt", "owned/../../outside.txt", filepath.Join(root, "absolute")} {
		if writeFixtureFiles(context.Background(), w, workerFixture{Files: map[string]string{path: "forbidden"}}) == nil {
			t.Fatalf("fixture escaped scope: %s", path)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "owned", "link")); err != nil {
		t.Fatal(err)
	}
	if writeFixtureFiles(context.Background(), w, workerFixture{Files: map[string]string{"{scope}/link/escape": "forbidden"}}) == nil {
		t.Fatal("fixture followed symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "escape")); !os.IsNotExist(err) {
		t.Fatal("unowned path written")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if writeFixtureFiles(ctx, w, workerFixture{Files: map[string]string{"{scope}/cancelled": "no"}}) == nil {
		t.Fatal("cancelled writer continued")
	}
}
