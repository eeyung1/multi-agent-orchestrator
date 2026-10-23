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
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
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
	for _, mode := range []string{"liveness", "planning", "worker"} {
		t.Run(mode, func(t *testing.T) {
			planningMode := mode == "planning"
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
			role := agentprotocol.RoleWorker
			capability := agentprotocol.Capability("example/heartbeat")
			if planningMode {
				role = agentprotocol.RolePlanner
				capability = agentprotocol.PlanningCapability
			}
			_, proof, err := s.Invite(agentattachment.InviteRequest{AgentID: "standalone", Roles: []agentprotocol.Role{role}, Capabilities: []agentprotocol.Capability{capability}, TTL: time.Minute}, true)
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
			args := []string{"--socket", path, "--credential-file", credential, "--agent-id", "standalone", "--heartbeat-interval", "1h"}
			if planningMode {
				fixture := filepath.Join(dir, "proposal.json")
				if err := os.WriteFile(fixture, []byte(`{"questions":["Which acceptance outcome?"]}`), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--proposal-file", fixture)
			}
			if mode == "worker" {
				fixture := filepath.Join(dir, "worker.json")
				if err := os.WriteFile(fixture, []byte(`{"files":{"{scope}/result.txt":"bounded bridge output\n"}}`), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--worker-file", fixture)
			}
			go func() { done <- run(ctx, args, out) }()
			select {
			case <-out.ready:
			case <-time.After(10 * time.Second):
				t.Fatal("bridge did not attach")
			}
			if planningMode {
				views, err := r.List()
				if err != nil || len(views) != 1 {
					t.Fatal(err)
				}
				b, err := r.SelectPlanner("run", views[0].Ref(), "", true, "human chose bridge Planner")
				if err != nil {
					t.Fatal(err)
				}
				correlation, err := r.BeginPlannerRequest(b.RunID, b.BindingID)
				if err != nil {
					t.Fatal(err)
				}
				request := agentprotocol.PlanningRequest{Correlation: correlation, Request: planning.Request{Objective: "test", Recon: planning.Report{Workspace: root, Head: "abc"}, Capabilities: []string{"coding"}, Validators: []string{"check"}, AllowedScopes: []string{"src"}}}
				call, stop := context.WithTimeout(ctx, 10*time.Second)
				defer stop()
				proposal, err := s.RequestPlanning(call, views[0].Ref(), request)
				if err != nil || len(proposal.Questions) != 1 {
					t.Fatalf("planning fixture exchange: %+v %v", proposal, err)
				}
			}
			if mode == "worker" {
				views, err := r.List()
				if err != nil || len(views) != 1 || views[0].Session.AssignedRole != "" {
					t.Fatal("worker fixture implicitly acquired a role", err)
				}
				if err := r.AssignRole(views[0].Ref(), agentprotocol.RoleWorker, "test human authorized fixture session"); err != nil {
					t.Fatal(err)
				}
				worktree := t.TempDir()
				w := agentprotocol.WorkerMessage{Correlation: agentprotocol.WorkerCorrelation{ObjectiveID: "objective", TaskID: "bounded", Generation: 1, DispatchID: "go-dispatch"}, Assignment: &agentprotocol.WorkerAssignmentData{Objective: "exercise generic bridge", Task: planning.Task{ID: "bounded", Scope: "owned", Description: "write bounded fixture", Capability: string(capability), Validation: []string{"check"}}, Worktree: worktree, Branch: "agent/fixture", Rules: "Leave uncommitted; all writers stop before reporting"}}
				var reports []agentprotocol.MessageType
				call, stop := context.WithTimeout(ctx, 10*time.Second)
				defer stop()
				terminal, err := s.Work(call, views[0].Ref(), w, 5*time.Second, func(_ context.Context, e agentprotocol.Envelope) error {
					reports = append(reports, e.Type)
					return nil // Routing fixture only; controller tests exercise authority.
				})
				if err != nil || terminal.Type != agentprotocol.WorkerResult || len(reports) != 3 || reports[0] != agentprotocol.WorkerAcceptance || reports[1] != agentprotocol.WorkerProgress || reports[2] != agentprotocol.WorkerResult {
					t.Fatalf("worker fixture exchange: %v %v", reports, err)
				}
				b, err := os.ReadFile(filepath.Join(worktree, "owned", "result.txt"))
				if err != nil || string(b) != "bounded bridge output\n" {
					t.Fatalf("worker fixture output: %q %v", b, err)
				}
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
			if err != nil || len(all) != 1 || all[0].Session.Status != agentregistry.Disconnected || (mode == "liveness" && all[0].Session.AssignedRole != "") {
				t.Fatalf("bridge lifecycle: %v", err)
			}
		})
	}
}
