package main

import (
	"github.com/eeyung1/multi-agent-orchestrator/internal/controller"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
)

func validateProposal(workspace string, p planner.Proposal, c controller.Config) error {
	return objective.Admit(workspace, p.Plan, c.Policy())
}
