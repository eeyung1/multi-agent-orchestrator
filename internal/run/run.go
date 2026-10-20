package run

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/completion"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/workflow"
)

type Request struct {
	Workspace string
	Agent string
	Role string
	Feature string
	Scope string
	Token string
	Base string
	AgentCommand string
	Validate string
	Timeout time.Duration
}

type Result struct {
	SessionID string
	State string
	Branch string
	Worktree string
	ExecutionOutput string
	ValidationOutput string
}

func Run(req Request)(Result,error) {
	started,err:=workflow.Start(workflow.Request{Workspace:req.Workspace,Agent:req.Agent,Role:req.Role,Feature:req.Feature,Scope:req.Scope,Token:req.Token,Base:req.Base})
	if err!=nil { return Result{SessionID:started.SessionID,State:started.State},fmt.Errorf("start: %w",err) }
	result:=Result{SessionID:started.SessionID,State:started.State,Branch:started.Branch,Worktree:started.Worktree}
	primary,err:=filepath.Abs(req.Workspace); if err!=nil { return result,err }

	executed,err:=execution.Run(execution.Request{Primary:primary,Worktree:started.Worktree,SessionID:started.SessionID,Command:req.AgentCommand,Timeout:req.Timeout})
	result.ExecutionOutput=executed.Output
	if err!=nil { result.State="BLOCKED"; return result,fmt.Errorf("execute: %w",err) }

	completed,err:=completion.Complete(completion.Request{Primary:primary,Worktree:started.Worktree,SessionID:started.SessionID,Token:req.Token,Feature:req.Feature,Validate:req.Validate})
	result.State=completed.State
	result.ValidationOutput=completed.Output
	if err!=nil { return result,fmt.Errorf("complete: %w",err) }
	return result,nil
}
