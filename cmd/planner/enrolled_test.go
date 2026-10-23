//go:build unix

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
)

type selectionOutput struct {
	bytes.Buffer
	events chan string
}

func (w *selectionOutput) Write(b []byte) (int, error) {
	n, err := w.Buffer.Write(b)
	for _, text := range []string{"Planner enrollment/selection:", "Select exact enrolled session"} {
		if bytes.Contains(b, []byte(text)) {
			w.events <- text
		}
	}
	return n, err
}
func TestEnrolledPlannerConsoleRequiresExactHumanConfirmation(t *testing.T) {
	for _, answer := range []string{"yes", "no", ""} {
		t.Run(fmt.Sprintf("answer-%q", answer), func(t *testing.T) {
			root := t.TempDir()
			cmd := exec.Command("git", "init", "-b", "main")
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s %v", out, err)
			}
			if _, err := enrollment.Enroll(root, enrollment.Native, "human authorized test repository"); err != nil {
				t.Fatal(err)
			}
			dir, err := os.MkdirTemp("", "mao-select-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			dir, err = filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "agents.sock")
			credential := filepath.Join(dir, "invite.json")
			input, writer := io.Pipe()
			defer input.Close()
			defer writer.Close()
			out := &selectionOutput{events: make(chan string, 8)}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			type result struct {
				p       *planner.Enrolled
				cleanup func() error
				err     error
			}
			done := make(chan result, 1)
			go func() {
				p, cleanup, err := selectEnrolled(ctx, bufio.NewReader(input), out, root, path, time.Minute, time.Second)
				done <- result{p, cleanup, err}
			}()
			wait := func() {
				t.Helper()
				select {
				case <-out.events:
				case <-ctx.Done():
					t.Fatal("console synchronization timed out")
				}
			}
			wait()
			if _, err := fmt.Fprintf(writer, "invite {\"agent_id\":\"future-agent\",\"credential_file\":%q}\n", credential); err != nil {
				t.Fatal(err)
			}
			wait()
			proof, err := agentattachment.ReadCredential(credential)
			if err != nil {
				t.Fatal(err)
			}
			id := agentprotocol.Identity{ProtocolVersion: agentprotocol.Version3, AgentID: "future-agent", SessionID: "fresh-session", Metadata: agentprotocol.Metadata{Provider: "brand-not-in-core"}, SupportedRoles: []agentprotocol.Role{agentprotocol.RolePlanner}, Capabilities: []agentprotocol.Capability{"planning"}}
			client, err := agentattachment.DialLocal(ctx, path, id, proof)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			// Enrollment bytes can arrive before the server publishes its READY
			// route. A lifecycle round trip establishes that readiness explicitly.
			if err := client.Heartbeat(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(writer, "candidates\n"); err != nil {
				t.Fatal(err)
			}
			wait()
			if _, err := io.WriteString(writer, "select 1\n"); err != nil {
				t.Fatal(err)
			}
			wait()
			if _, err := fmt.Fprintln(writer, answer); err != nil {
				t.Fatal(err)
			}
			var res result
			select {
			case res = <-done:
			case <-ctx.Done():
				t.Fatal("selection did not finish")
			}
			if answer == "yes" {
				if res.err != nil {
					t.Fatal(res.err)
				}
				if res.p.Binding.SessionID != id.SessionID || res.p.Binding.Generation != client.Binding().Generation {
					t.Fatal("wrong binding")
				}
				if err := res.cleanup(); err != nil {
					t.Fatal(err)
				}
			} else if res.err == nil {
				if err := res.cleanup(); err != nil {
					t.Fatal(err)
				}
				t.Fatal("silence/refusal authorized selection")
			}
			if !strings.Contains(out.String(), "brand-not-in-core") || !strings.Contains(out.String(), "incarnation=") {
				t.Fatal("candidate identity not displayed")
			}
			registry, err := agentregistry.Open(root, agentregistry.Policy{StaleAfter: time.Minute}, nil)
			if err != nil {
				t.Fatal(err)
			}
			views, err := registry.List()
			if err != nil || len(views) != 1 {
				t.Fatal(err)
			}
			if answer != "yes" && views[0].Session.AssignedRole != "" {
				t.Fatal("unauthorized role assigned")
			}
		})
	}
}
