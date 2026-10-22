package agentattachment

import (
	"context"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
)

func planningClient(t *testing.T, f fixture, agent, session string, replace bool) (*Client, <-chan error) {
	t.Helper()
	_, proof, err := f.s.Invite(InviteRequest{AgentID: agentprotocol.AgentID(agent), Roles: []agentprotocol.Role{agentprotocol.RolePlanner}, Capabilities: []agentprotocol.Capability{"planning"}, TTL: time.Minute, Replace: replace}, true)
	if err != nil {
		t.Fatal(err)
	}
	id := identity(agent, session)
	id.ProtocolVersion = agentprotocol.Version3
	id.Capabilities = []agentprotocol.Capability{"planning"}
	id.SupportedRoles = []agentprotocol.Role{agentprotocol.RolePlanner}
	return attach(t, f, id, proof)
}
func planningRequest(c agentprotocol.PlannerCorrelation) agentprotocol.PlanningRequest {
	return agentprotocol.PlanningRequest{Correlation: c, Request: planning.Request{Objective: "test", Recon: planning.Report{Workspace: "/workspace", Head: "abc"}, Capabilities: []string{"coding"}, Validators: []string{"check"}, AllowedScopes: []string{"src"}}}
}

func TestPlanningRoutingHeartbeatsAndUnselectedRejection(t *testing.T) {
	f := setup(t, nil)
	selected, done := planningClient(t, f, "selected", "selected-session", false)
	other, otherDone := planningClient(t, f, "other", "other-session", false)
	b, err := f.r.SelectPlanner("run", selected.Binding(), "", true, "human selected Planner")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.r.BeginPlannerRequest(b.RunID, b.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := f.s.RequestPlanning(ctx, selected.Binding(), planningRequest(c)); result <- err }()
	request, err := selected.ReceivePlanning(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(other.requests) != 0 {
		t.Fatal("unselected Planner received request")
	}
	f.clock.Advance(time.Second)
	if err := selected.Heartbeat(ctx); err != nil {
		t.Fatal("heartbeat competed for Planner receive", err)
	}
	if !record(t, f, selected.Binding().SessionID).Session.LastSeenAt.Equal(f.clock.Now()) {
		t.Fatal("heartbeat did not update")
	}
	p := planning.Proposal{Questions: []string{"Which behavior?"}}
	if err := other.RespondPlanning(ctx, request, p); err != nil {
		t.Fatal(err)
	}
	if err := await(t, otherDone); err == nil {
		t.Fatal("unselected response accepted")
	}
	if err := selected.RespondPlanning(ctx, request, p); err != nil {
		t.Fatal(err)
	}
	if err := await(t, result); err != nil {
		t.Fatal(err)
	}
	// Completed response is not retryable, even on the same bound transport.
	if err := selected.RespondPlanning(ctx, request, p); err != nil {
		t.Fatal(err)
	}
	if err := await(t, done); err == nil {
		t.Fatal("duplicate response accepted")
	}
	if readState(t, f).AgentRegistry.Planners[b.RunID].Active {
		t.Fatal("protocol failure did not invalidate selection")
	}
}

func TestPlanningWrongCorrelationIdentityAndDisconnect(t *testing.T) {
	for _, mode := range []string{"unknown request", "wrong identity", "wrong session", "disconnect", "supersede"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t, nil)
			client, done := planningClient(t, f, "agent", "old-session", false)
			b, err := f.r.SelectPlanner("run", client.Binding(), "", true, "human selected Planner")
			if err != nil {
				t.Fatal(err)
			}
			c, err := f.r.BeginPlannerRequest(b.RunID, b.BindingID)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := f.s.RequestPlanning(ctx, client.Binding(), planningRequest(c)); result <- err }()
			request, err := client.ReceivePlanning(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ref := client.Binding()
			if mode == "disconnect" {
				if err := client.Disconnect(ctx); err != nil {
					t.Fatal(err)
				}
				_ = await(t, done)
			} else {
				if mode == "supersede" {
					fresh, freshDone := planningClient(t, f, "agent", "new-session", true)
					defer func() { _ = fresh.Close(); _ = await(t, freshDone) }()
				}
				response := agentprotocol.PlanningResponse{Correlation: request.Correlation, Proposal: planning.Proposal{Questions: []string{"Question?"}}}
				e := agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version3, Type: agentprotocol.PlannerResponse, AgentID: ref.AgentID, SessionID: ref.SessionID, PlanningResponse: &response}
				switch mode {
				case "unknown request":
					e.PlanningResponse.Correlation.RequestID = "unknown"
				case "wrong identity":
					e.AgentID = "another-agent"
				case "wrong session":
					e.SessionID = "another-session"
				}
				if err := client.transport.Send(ctx, e); err != nil {
					t.Fatal(err)
				}
				if err := await(t, done); err == nil {
					t.Fatal("invalid/stale response accepted")
				}
				if mode == "supersede" {
					if !record(t, f, "new-session").Live {
						t.Fatal("stale close disconnected replacement")
					}
				}
			}
			if err := await(t, result); err == nil {
				t.Fatal("failed Planner returned proposal")
			}
			if readState(t, f).AgentRegistry.Planners[b.RunID].Active {
				t.Fatal("selection survived disconnect")
			}
		})
	}
}
