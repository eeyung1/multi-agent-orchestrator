package planner

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

// Enrolled implements the same reasoning contract as managed Provider. It owns
// no task/scheduler authority. Registry persistence is the selection authority;
// the attachment server only routes messages to its private bound connection.
type Enrolled struct {
	Registry *agentregistry.Registry
	Server   *agentattachment.Server
	Binding  state.PlannerBinding
	Timeout  time.Duration
	mu       sync.Mutex
	evidence state.PlannerEvidence
}

func (p *Enrolled) Propose(ctx context.Context, r Request) (Proposal, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evidence = state.PlannerEvidence{}
	if p.Timeout <= 0 || p.Registry == nil || p.Server == nil {
		return Proposal{}, errors.New("invalid enrolled Planner configuration")
	}
	c, err := p.Registry.BeginPlannerRequest(p.Binding.RunID, p.Binding.BindingID)
	if err != nil {
		return Proposal{}, err
	}
	call, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	ref := agentregistry.Ref{AgentID: p.Binding.AgentID, SessionID: p.Binding.SessionID, Generation: p.Binding.Generation}
	proposal, err := p.Server.RequestPlanning(call, ref, agentprotocol.PlanningRequest{Correlation: c, Request: r})
	if err == nil {
		err = call.Err()
	}
	if err != nil {
		stopErr := p.Registry.StopPlanner(p.Binding.RunID, p.Binding.BindingID, "planner_request_failed")
		return Proposal{}, errors.Join(errors.New("enrolled Planner request failed; review selection/session state"), err, stopErr)
	}
	if proposal.Validate() != nil || len(proposal.Questions) == 0 && proposal.Plan.Objective != r.Objective {
		stopErr := p.Registry.StopPlanner(p.Binding.RunID, p.Binding.BindingID, "invalid_planner_proposal")
		return Proposal{}, errors.Join(errors.New("enrolled Planner returned invalid proposal"), stopErr)
	}
	p.evidence = state.PlannerEvidence{Correlation: c, ProposalDigest: planning.PlanDigest(proposal.Plan)}
	return proposal, nil
}
func (p *Enrolled) Evidence() state.PlannerEvidence {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.evidence
}
func (p *Enrolled) Close() error {
	return p.Registry.StopPlanner(p.Binding.RunID, p.Binding.BindingID, "planner_run_closed")
}
