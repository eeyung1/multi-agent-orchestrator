//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
)

type notifiedOutput struct {
	once  sync.Once
	ready chan struct{}
}

func (w *notifiedOutput) Write(b []byte) (int, error) {
	w.once.Do(func() { close(w.ready) })
	return len(b), nil
}

func TestBridgeCommandAttachesIndependentlyAndDisconnectsOnCancellation(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fixture: %v %s", err, out)
	}
	if _, err := enrollment.Enroll(root, enrollment.Native, "test human authorization"); err != nil {
		t.Fatal(err)
	}
	r, err := agentregistry.Open(root, agentregistry.Policy{StaleAfter: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := agentattachment.New(r, agentattachment.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir, err := os.MkdirTemp("", "mao-bridge-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agents.sock")
	l, err := agentattachment.ListenLocal(s, path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	serverDone := make(chan error, 1)
	go func() { serverDone <- l.Serve(context.Background()) }()
	_, proof, err := s.Invite(agentattachment.InviteRequest{AgentID: "standalone", Roles: []agentprotocol.Role{agentprotocol.RoleWorker}, Capabilities: []agentprotocol.Capability{"example/heartbeat"}, TTL: time.Minute}, true)
	if err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(dir, "invitation.json")
	if err := agentattachment.WriteCredential(credential, proof); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &notifiedOutput{ready: make(chan struct{})}
	done := make(chan error, 1)
	// Run the client command independently: no controller/provider process launch.
	go func() {
		done <- run(ctx, []string{"--socket", path, "--credential-file", credential, "--agent-id", "standalone", "--heartbeat-interval", "1h"}, out)
	}()
	select {
	case <-out.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("bridge did not attach")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("bridge did not stop")
	}
	_ = l.Close()
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	all, err := r.List()
	if err != nil || len(all) != 1 || all[0].Session.Status != agentregistry.Disconnected || all[0].Session.AssignedRole != "" {
		t.Fatalf("bridge lifecycle: %v", err)
	}
}
