package controller

import (
	"context"
	"errors"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

// Runtime routes are deliberately not configuration or policy identity. Only the
// explicit enrolled_capabilities allowlist enables dispatch. No auto enrollment.
type EnrolledWorkers struct {
	Registry *agentregistry.Registry
	Server   *agentattachment.Server
}

func workerRef(d state.WorkerDispatch) agentregistry.Ref {
	return agentregistry.Ref{AgentID: d.AgentID, SessionID: d.SessionID, Generation: d.Incarnation}
}
func (c Config) selectWorker(capability string) (agentregistry.Ref, error) {
	allowed := false
	for _, cap := range c.EnrolledCapabilities {
		allowed = allowed || string(cap) == capability
	}
	if !allowed || c.Runtime == nil || c.Runtime.Registry == nil || c.Runtime.Server == nil {
		return agentregistry.Ref{}, errors.New("no authorized enrolled worker runtime for capability")
	}
	all, err := c.Runtime.Registry.Eligible(agentprotocol.RoleWorker, agentprotocol.Capability(capability))
	if err != nil {
		return agentregistry.Ref{}, err
	}
	// Registry order is ascending Go-issued incarnation (oldest available first).
	for _, v := range all {
		if v.Session.Identity.ProtocolVersion == agentprotocol.Version4 && c.Runtime.Server.Connected(v.Ref()) {
			return v.Ref(), nil
		}
	}
	return agentregistry.Ref{}, errors.New("no connected live READY assigned WORKER matches capability")
}
func (c Config) executeEnrolled(ctx context.Context, primary string, plan objective.Plan, a scheduler.Result, wc WorkerContext, req execution.Request) error {
	if c.Runtime == nil {
		return errors.New("enrolled worker runtime unavailable")
	}
	if err := scheduler.MarkWorkerSent(primary, plan.ID, a); err != nil {
		return err
	}
	d := a.WorkerDispatch
	message := agentprotocol.WorkerMessage{Correlation: d.Correlation, Assignment: &agentprotocol.WorkerAssignmentData{Objective: wc.Objective, Task: wc.Task, Worktree: wc.Worktree, Branch: wc.Branch, Instructions: wc.Instructions, Rules: wc.Protocol + " A terminal result/blocker/rejection relinquishes all writing. Return only after all task writers stop."}}
	timeout := time.Duration(c.AcceptanceSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	_, err := execution.Supervise(req, func(ctx context.Context) error {
		_, err := c.Runtime.Server.Work(ctx, workerRef(*d), message, timeout, func(reportCtx context.Context, e agentprotocol.Envelope) error {
			return scheduler.WorkerEventContext(reportCtx, primary, workerRef(*d), e)
		})
		return err
	})
	return err
}
func assignmentResult(t state.Task) scheduler.Result {
	return scheduler.Result{Task: objective.Task{ID: t.ID, Feature: t.Feature, Scope: t.Scope, Capability: t.Capability}, SessionID: t.SessionID, Token: t.Token, Branch: t.Branch, Worktree: t.Worktree, Attempt: t.Attempt, AssignmentGeneration: t.AssignmentGeneration, WorkerDispatch: t.WorkerDispatch}
}
