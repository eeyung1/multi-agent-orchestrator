//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
)

func TestTrustedConsoleCreatesListsAndRevokesWithoutSecrets(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	// TempDir's numbered child follows the process umask; control ancestors
	// must explicitly be private rather than inheriting group write access.
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fixture: %v %s", err, out)
	}
	if _, err := enrollment.Enroll(root, enrollment.Native, "test human enrollment"); err != nil {
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
	dir := filepath.Join(root, "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "credential.json")
	command, _ := json.Marshal(map[string]any{"agent_id": "compatible-agent", "roles": []string{"WORKER"}, "capabilities": []string{"example/heartbeat"}, "ttl_seconds": 60, "credential_file": path})
	var out bytes.Buffer
	if err := console(s, r, "invite "+string(command), &out); err != nil {
		t.Fatal(err)
	}
	proof, err := agentattachment.ReadCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := console(s, r, "invitations", &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), proof.Secret) || strings.Contains(out.String(), `"secret"`) {
		t.Fatal("console leaked credential")
	}
	if err := console(s, r, "revoke "+proof.ID, &out); err != nil {
		t.Fatal(err)
	}
	p, err := s.Invitations()
	if err != nil || len(p) != 0 {
		t.Fatal("revoke failed")
	}
	if err := console(s, r, "invite "+string(command), &out); err == nil {
		t.Fatal("existing credential overwritten")
	}
	p, err = s.Invitations()
	if err != nil || len(p) != 0 {
		t.Fatal("credential delivery failure left usable invitation")
	}
}
