package workflow

import (
	"fmt"
	"path/filepath"

	"github.com/eeyung1/multi-agent-orchestrator/internal/activation"
	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/worktree"
)

type Request struct {
	Workspace   string
	Agent       string
	Role        string
	Feature     string
	Scope       string
	Token       string
	Base        string
	IsolationID string
}

type Result struct {
	SessionID string
	State     string
	Branch    string
	Worktree  string
	Claim     string
}

func Start(req Request) (Result, error) {
	if err := enrollment.RequireEnrolled(req.Workspace); err != nil {
		return Result{}, err
	}
	active, err := activation.Activate(req.Workspace, req.Agent, req.Role)
	if err != nil {
		return Result{}, err
	}

	primary := filepath.Dir(active.CoordinationDir)
	if err := coordination.ClaimScope(primary, coordination.Claim{
		SessionID: active.SessionID, Feature: req.Feature, Scope: req.Scope, Token: req.Token,
	}); err != nil {
		_ = coordination.Transition(primary, active.SessionID, "BLOCKED")
		return Result{SessionID: active.SessionID, State: "BLOCKED"}, fmt.Errorf("claim: %w", err)
	}

	isolationID := req.IsolationID
	if isolationID == "" {
		isolationID = req.Feature
	}
	wt, err := worktree.Create(primary, isolationID, req.Base)
	if err != nil {
		_ = coordination.ReleaseClaim(primary, active.SessionID, req.Token)
		_ = coordination.Transition(primary, active.SessionID, "BLOCKED")
		return Result{SessionID: active.SessionID, State: "BLOCKED"}, fmt.Errorf("worktree: %w", err)
	}

	if err := coordination.UpdateAssignment(primary, active.SessionID, req.Feature, wt.Branch+" @ "+wt.Path, req.Token); err != nil {
		return rollbackStart(primary, active.SessionID, req.Token, wt, fmt.Errorf("record assignment: %w", err))
	}
	if err := coordination.Transition(primary, active.SessionID, "WORKING"); err != nil {
		return rollbackStart(primary, active.SessionID, req.Token, wt, fmt.Errorf("working transition: %w", err))
	}
	return Result{SessionID: active.SessionID, State: "WORKING", Branch: wt.Branch, Worktree: wt.Path, Claim: req.Token}, nil
}

func rollbackStart(primary, sessionID, token string, wt worktree.Result, cause error) (Result, error) {
	cleanupErr := worktree.Remove(primary, wt)
	releaseErr := coordination.ReleaseClaim(primary, sessionID, token)
	transitionErr := coordination.Transition(primary, sessionID, "BLOCKED")
	if cleanupErr != nil || releaseErr != nil || transitionErr != nil {
		return Result{SessionID: sessionID, State: "BLOCKED"}, fmt.Errorf("%w; rollback worktree=%v release=%v transition=%v", cause, cleanupErr, releaseErr, transitionErr)
	}
	return Result{SessionID: sessionID, State: "BLOCKED"}, cause
}
