//go:build unix

package agentattachment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
)

func socketPath(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "mao-local-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	canonical, err := filepath.EvalSymlinks(d)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(canonical, "agents.sock")
}
func listener(t *testing.T, f fixture) (string, *LocalListener, <-chan error) {
	t.Helper()
	path := socketPath(t)
	l, err := ListenLocal(f.s, path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- l.Serve(context.Background()) }()
	t.Cleanup(func() { _ = l.Close() })
	return path, l, done
}

func TestLocalIPCAttachmentAcceptance(t *testing.T) {
	f := setup(t, nil)
	path, l, done := listener(t, f)
	_, proof := invite(t, f, "independent-agent", false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := DialLocal(ctx, path, identity("independent-agent", "independent-process-session"), proof)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	v := record(t, f, c.Binding().SessionID)
	if v.Ref() != c.Binding() || v.Session.Status != agentregistry.Ready || v.Session.AssignedRole != "" {
		t.Fatal("binding not authoritative/unassigned")
	}
	f.clock.Advance(time.Second)
	if err := c.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	if !record(t, f, c.Binding().SessionID).Session.LastSeenAt.Equal(f.clock.Now()) {
		t.Fatal("IPC heartbeat not applied")
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if record(t, f, c.Binding().SessionID).Session.Status != agentregistry.Disconnected {
		t.Fatal("IPC disconnect not applied before acknowledgment")
	}
	s := readState(t, f)
	if len(s.Tasks) != 0 || len(s.Claims) != 0 || len(s.Sessions) != 0 {
		t.Fatal("local attachment created scheduler authority")
	}
	_ = l.Close()
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("owned socket not cleaned")
	}
}

func TestLocalConnectionLimitAndShutdown(t *testing.T) {
	f := setup(t, func(c *Config) { c.MaxConnections = 1 })
	path, l, done := listener(t, f)
	_, proof := invite(t, f, "one", false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := DialLocal(ctx, path, identity("one", "one-run"), proof)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, other := invite(t, f, "two", false)
	if c2, err := DialLocal(ctx, path, identity("two", "two-run"), other); err == nil {
		_ = c2.Close()
		t.Fatal("connection limit bypassed")
	}
	if readState(t, f).AgentRegistry.LastGeneration != 1 {
		t.Fatal("excess connection enrolled")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
	if record(t, f, "one-run").Session.Status != agentregistry.Disconnected {
		t.Fatal("shutdown left bound session ready")
	}
	if err := c.Heartbeat(ctx); err == nil {
		t.Fatal("shutdown did not wake/close client")
	}
}

func TestLocalConcurrentIndependentAttachments(t *testing.T) {
	f := setup(t, nil)
	path, l, done := listener(t, f)
	const count = 8
	errCh := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("agent-%d", i)
		_, proof := invite(t, f, name, false)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c, err := DialLocal(ctx, path, identity(name, name+"-run"), proof)
			if err == nil {
				err = c.Heartbeat(ctx)
				if err == nil {
					err = c.Disconnect(ctx)
				}
				_ = c.Close()
			}
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	_ = l.Close()
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
	s := readState(t, f)
	if len(s.AgentRegistry.Sessions) != count {
		t.Fatal("lost independent enrollments")
	}
	for _, session := range s.AgentRegistry.Sessions {
		if session.Status != agentregistry.Disconnected {
			t.Fatal("lost disconnect")
		}
	}
}

func TestMalformedWireAndUnsupportedVersionNeverEnroll(t *testing.T) {
	for _, raw := range []string{"{\n", `{"protocol_version":"future/v99","type":"heartbeat","agent_id":"agent","session_id":"run"}` + "\n", `{"protocol_version":"orchestrator-agent/v2","type":"heartbeat","agent_id":"agent","session_id":"run","secret-material":true}` + "\n"} {
		f := setup(t, nil)
		path, l, done := listener(t, f)
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		var b [512]byte
		n, _ := conn.Read(b[:])
		_ = conn.Close()
		if strings.Contains(string(b[:n]), "secret-material") {
			t.Fatal("wire error echoed sensitive field")
		}
		_ = l.Close()
		if err := await(t, done); err != nil {
			t.Fatal(err)
		}
		if readState(t, f).AgentRegistry.LastGeneration != 0 {
			t.Fatal("malformed input enrolled")
		}
	}
}

func TestSocketPermissionsAndUnsafePaths(t *testing.T) {
	f := setup(t, nil)
	path := socketPath(t)
	l, err := ListenLocal(f.s, path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	i, err := os.Lstat(path)
	if err != nil || i.Mode().Perm() != 0600 {
		t.Fatal("socket permissions")
	}
	i, err = os.Lstat(filepath.Dir(path))
	if err != nil || i.Mode().Perm() != 0700 {
		t.Fatal("parent permissions")
	}
	if _, err := ListenLocal(f.s, path); err == nil {
		t.Fatal("existing socket replaced")
	}
	unsafe := filepath.Join(filepath.Dir(path), "unsafe")
	if err := os.Mkdir(unsafe, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenLocal(f.s, filepath.Join(unsafe, "socket")); err == nil {
		t.Fatal("nonprivate parent accepted")
	}
	link := filepath.Join(filepath.Dir(path), "link")
	if err := os.Symlink(filepath.Dir(path), link); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenLocal(f.s, filepath.Join(link, "socket")); err == nil {
		t.Fatal("symlink parent accepted")
	}
	leaf := filepath.Join(filepath.Dir(path), "leaf")
	if err := os.Symlink(path, leaf); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenLocal(f.s, leaf); err == nil {
		t.Fatal("symlink socket accepted")
	}
	if _, err := ListenLocal(f.s, "relative.sock"); err == nil {
		t.Fatal("relative path accepted")
	}
}

func TestSocketCleanupPreservesReplacementAndRefusesStaleSocket(t *testing.T) {
	f := setup(t, nil)
	path := socketPath(t)
	l, err := ListenLocal(f.s, path)
	if err != nil {
		t.Fatal(err)
	}
	moved := path + ".old"
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); !errors.Is(err, ErrSocket) {
		t.Fatal("foreign socket replacement cleanup not reported")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "replacement" {
		t.Fatal("cleanup deleted replacement")
	}
	if _, err := ListenLocal(f.s, moved); err == nil {
		t.Fatal("stale socket automatically stolen")
	}
}

func TestCredentialFilesArePrivateExclusiveAndStrict(t *testing.T) {
	f := setup(t, nil)
	_, proof := invite(t, f, "agent", false)
	path := filepath.Join(filepath.Dir(socketPath(t)), "invitation.json")
	if err := WriteCredential(path, proof); err != nil {
		t.Fatal(err)
	}
	if err := WriteCredential(path, proof); err == nil {
		t.Fatal("credential overwritten")
	}
	got, err := ReadCredential(path)
	if err != nil || got != proof {
		t.Fatal("credential round trip")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCredential(path); err == nil {
		t.Fatal("public credential accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCredential(link); err == nil {
		t.Fatal("credential symlink accepted")
	}
	b, _ := json.Marshal(proof)
	b = append(b[:len(b)-1], []byte(`,"unknown":true}`)...)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCredential(path); err == nil || strings.Contains(err.Error(), proof.Secret) {
		t.Fatal("malformed credential accepted/leaked")
	}
}

func TestEnrollmentPublicationFailureBurnsCredentialWithoutBinding(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires permission enforcement")
	}
	f := setup(t, nil)
	_, proof := invite(t, f, "agent", false)
	dir := filepath.Join(f.root, "multi-agent-orchestrator")
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0755)
	a, done := connection(t, f)
	if _, err := Attach(context.Background(), a, identity("agent", "run"), proof); err == nil {
		t.Fatal("failed persistence bound connection")
	}
	if err := await(t, done); !errors.Is(err, ErrRegistry) {
		t.Fatal(err)
	}
	if readState(t, f).AgentRegistry.LastGeneration != 0 {
		t.Fatal("failed publish left session")
	}
	if len(f.s.pending) != 0 {
		t.Fatal("uncertain persistence retained reusable credential")
	}
}
