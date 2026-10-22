//go:build unix

package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
	"github.com/eeyung1/multi-agent-orchestrator/internal/recon"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
)

func TestEnrolledPlannerLocalAcceptanceAndHumanAdoption(t *testing.T) {
	for _, mode := range []string{"human adoption", "disconnect during review", "cancel pending request", "wrong objective", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			disconnectBeforeApproval := mode == "disconnect during review"
			root, config := fixture(t)
			registry, err := agentregistry.Open(root, agentregistry.Policy{StaleAfter: time.Minute}, nil)
			if err != nil {
				t.Fatal(err)
			}
			server, err := agentattachment.New(registry, agentattachment.DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			dir, err := os.MkdirTemp("", "mao-planner-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			dir, err = filepath.EvalSymlinks(dir)
			if err != nil {
				t.Fatal(err)
			}
			l, err := agentattachment.ListenLocal(server, filepath.Join(dir, "agent.sock"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- l.Serve(ctx) }()
			defer func() {
				_ = l.Close()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			connect := func(agent string) *agentattachment.Client {
				t.Helper()
				_, proof, err := server.Invite(agentattachment.InviteRequest{AgentID: agentprotocol.AgentID(agent), Roles: []agentprotocol.Role{agentprotocol.RolePlanner, agentprotocol.RoleWorker}, Capabilities: []agentprotocol.Capability{agentprotocol.PlanningCapability, "coding"}, TTL: time.Minute}, true)
				if err != nil {
					t.Fatal(err)
				}
				id := agentprotocol.Identity{ProtocolVersion: agentprotocol.Version3, AgentID: agentprotocol.AgentID(agent), SessionID: agentprotocol.SessionID(agent + "-session"), Metadata: agentprotocol.Metadata{Provider: "unknown-future-provider"}, Capabilities: []agentprotocol.Capability{agentprotocol.PlanningCapability, "coding"}, SupportedRoles: []agentprotocol.Role{agentprotocol.RolePlanner, agentprotocol.RoleWorker}}
				client, err := agentattachment.DialLocal(ctx, filepath.Join(dir, "agent.sock"), id, proof)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })
				return client
			}
			selected, other := connect("chosen"), connect("unselected")
			candidates, err := registry.PlannerCandidates()
			if err != nil || len(candidates) != 2 {
				t.Fatalf("candidates=%d %v", len(candidates), err)
			}
			binding, err := registry.SelectPlanner("run", selected.Binding(), "", true, "human explicitly chose exact session")
			if err != nil {
				t.Fatal(err)
			}
			provider := &planner.Enrolled{Registry: registry, Server: server, Binding: binding, Timeout: time.Second * 5}
			if mode == "deadline" {
				provider.Timeout = time.Nanosecond
			}
			report, err := recon.Inspect(root, "test objective")
			if err != nil {
				t.Fatal(err)
			}
			request := planner.Request{Objective: "test objective", Recon: report, Capabilities: config.Capabilities(), Validators: config.Checks(), AllowedScopes: config.AllowedScopes}
			responses := make(chan planner.Proposal, 1)
			failures := make(chan error, 1)
			planningCtx, cancelPlanning := context.WithCancel(ctx)
			defer cancelPlanning()
			go func() { p, err := provider.Propose(planningCtx, request); responses <- p; failures <- err }()
			if mode == "deadline" {
				<-responses
				if err := <-failures; !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("deadline did not fail closed", err)
				}
				snapshot, err := scheduler.Read(root)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.AgentRegistry.Planners[binding.RunID].Active {
					t.Fatal("timed-out request retained selection")
				}
				return
			}
			delivered, err := selected.ReceivePlanning(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if delivered.Request.Objective != request.Objective || delivered.Request.Recon.Head != report.Head {
				t.Fatal("lost bounded context")
			}
			if err := other.Heartbeat(ctx); err != nil {
				t.Fatal("unselected connection unavailable", err)
			}
			if err := registry.CheckPlanner(other.Binding(), delivered.Correlation, false); err == nil {
				t.Fatal("unselected agent acquired Planner binding")
			}
			if mode == "cancel pending request" {
				cancelPlanning()
				<-responses
				if err := <-failures; !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation lost", err)
				}
				snapshot, err := scheduler.Read(root)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.AgentRegistry.Planners[binding.RunID].Active || len(snapshot.Tasks) > 0 {
					t.Fatal("cancelled request retained authority")
				}
				return
			}
			proposed := planner.Proposal{Plan: objectivePlanForEnrollment()}
			if mode == "wrong objective" {
				proposed.Plan.Objective = "not the authorized objective"
			}
			if err := selected.RespondPlanning(ctx, delivered, proposed); err != nil {
				t.Fatal(err)
			}
			p := <-responses
			failure := <-failures
			if mode == "wrong objective" {
				if failure == nil {
					t.Fatal("changed objective accepted")
				}
				return
			}
			if err := failure; err != nil {
				t.Fatal(err)
			}
			snapshot, err := scheduler.Read(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Tasks) != 0 || len(snapshot.Claims) != 0 || len(snapshot.Objectives) != 0 {
				t.Fatal("proposal bypassed human adoption")
			}
			p.Plan.ID = "human-reviewed-objective"
			if err := AdmitEnrolled(root, p.Plan, config, report, "", provider.Evidence()); err == nil {
				t.Fatal("no human authorization")
			}
			if disconnectBeforeApproval {
				if err := selected.Disconnect(ctx); err != nil {
					t.Fatal(err)
				}
			}
			err = AdmitEnrolled(root, p.Plan, config, report, "human reviewed exact proposal", provider.Evidence())
			if disconnectBeforeApproval {
				if err == nil {
					t.Fatal("disconnected Planner passed human adoption fence")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err = scheduler.Read(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Tasks) != 1 || len(snapshot.Claims) != 0 || snapshot.Objectives[p.Plan.ID].PlannerEvidence == nil {
				t.Fatal("incorrect authoritative adoption")
			}
			p.Plan.ID = "replayed-objective"
			if err := AdmitEnrolled(root, p.Plan, config, report, "human", provider.Evidence()); err == nil {
				t.Fatal("response reused for second objective")
			}
			if err := selected.Disconnect(ctx); err != nil {
				t.Fatal(err)
			}
			if err := registry.CheckPlanner(selected.Binding(), delivered.Correlation, true); !errors.Is(err, agentregistry.ErrPlanner) {
				t.Fatal("disconnect retained active binding", err)
			}
		})
	}
}

func objectivePlanForEnrollment() objective.Plan {
	return objective.Plan{ID: "suggested", Objective: "test objective", Tasks: []objective.Task{task("enrolled")}}
}
