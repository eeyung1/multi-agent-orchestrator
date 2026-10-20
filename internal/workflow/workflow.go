package workflow

import (
	"fmt"
	"path/filepath"

	"github.com/eeyung1/multi-agent-orchestrator/internal/activation"
	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/worktree"
)

type Request struct {
	Workspace string
	Agent     string
	Role      string
	Feature   string
	Scope     string
	Token     string
	Base      string
}

type Result struct {
	SessionID string
	State     string
	Branch    string
	Worktree  string
	Claim     string
}

func Start(req Request) (Result, error) {
	active, err := activation.Activate(req.Workspace, req.Agent, req.Role)
	if err != nil { return Result{}, err }

	primary := filepath.Dir(active.CoordinationDir)
	if err := coordination.ClaimScope(primary, coordination.Claim{
		SessionID: active.SessionID, Feature: req.Feature, Scope: req.Scope, Token: req.Token,
	}); err != nil {
		_ = coordination.Transition(primary, active.SessionID, "BLOCKED")
		return Result{SessionID:active.SessionID, State:"BLOCKED"}, fmt.Errorf("claim: %w", err)
	}

	wt, err := worktree.Create(primary, req.Feature, req.Base)
	if err != nil {
		_ = coordination.ReleaseClaim(primary, active.SessionID, req.Token)
		_ = coordination.Transition(primary, active.SessionID, "BLOCKED")
		return Result{SessionID:active.SessionID, State:"BLOCKED"}, fmt.Errorf("worktree: %w", err)
	}

	if err := coordination.UpdateAssignment(primary, active.SessionID, req.Feature, wt.Branch+" @ "+wt.Path, req.Token); err != nil {
		return Result{}, fmt.Errorf("record assignment: %w", err)
	}
	if err := coordination.Transition(primary, active.SessionID, "WORKING"); err != nil {
		return Result{}, fmt.Errorf("working transition: %w", err)
	}
	return Result{SessionID:active.SessionID, State:"WORKING", Branch:wt.Branch, Worktree:wt.Path, Claim:req.Token}, nil
}
