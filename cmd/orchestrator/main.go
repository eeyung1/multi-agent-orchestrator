package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eeyung1/multi-agent-orchestrator/internal/activation"
	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/worktree"
	workflowrun "github.com/eeyung1/multi-agent-orchestrator/internal/workflow"
)

func main() {
	var workspace, agent, role, session, feature, scope, token, state, nextSession, base string
	var heartbeat, release, createWorktree, start bool
	flag.StringVar(&workspace, "workspace", ".", "target project workspace")
	flag.StringVar(&agent, "agent", "", "agent identity prefix")
	flag.StringVar(&role, "role", "coding-agent", "agent role")
	flag.StringVar(&session, "session", "", "existing agent session ID")
	flag.StringVar(&feature, "feature", "", "feature identifier")
	flag.StringVar(&scope, "scope", "", "claimed file scope")
	flag.StringVar(&token, "token", "", "claim token")
	flag.StringVar(&state, "state", "", "lifecycle state transition")
	flag.BoolVar(&heartbeat, "heartbeat", false, "refresh session heartbeat")
	flag.BoolVar(&release, "release", false, "release a claim")
	flag.StringVar(&nextSession, "handoff", "", "transfer claim to another session")
	flag.BoolVar(&createWorktree, "create-worktree", false, "create isolated feature worktree")
	flag.StringVar(&base, "base", "main", "base ref for worktree")
	flag.BoolVar(&start, "start", false, "run activation, claim, worktree and WORKING transition")
	flag.Parse()

	primary, err := filepath.Abs(workspace)
	if err != nil { fail(err) }

	if start {
		result,err:=workflowrun.Start(workflowrun.Request{Workspace:workspace,Agent:agent,Role:role,Feature:feature,Scope:scope,Token:token,Base:base})
		if err!=nil { fail(err) }
		fmt.Printf("agent=%s state=%s claim=%s branch=%s worktree=%s\n",result.SessionID,result.State,result.Claim,result.Branch,result.Worktree)
		return
	}

	if createWorktree {
		result,err:=worktree.Create(primary,feature,base); if err!=nil { fail(err) }
		fmt.Printf("branch=%s worktree=%s\n",result.Branch,result.Path); return
	}

	if session != "" {
		if release {
			if err:=coordination.ReleaseClaim(primary,session,token); err!=nil { fail(err) }
			fmt.Println("claim released"); return
		}
		if nextSession != "" {
			if err:=coordination.Handoff(primary,session,token,nextSession); err!=nil { fail(err) }
			fmt.Printf("handoff=%s\n",nextSession); return
		}
		if heartbeat {
			if err:=coordination.Heartbeat(primary,session); err!=nil { fail(err) }
			fmt.Println("heartbeat refreshed")
			return
		}
		if state != "" {
			if err:=coordination.Transition(primary,session,state); err!=nil { fail(err) }
			fmt.Printf("state=%s\n",state)
			return
		}
		if feature != "" || scope != "" || token != "" {
			if err:=coordination.ClaimScope(primary,coordination.Claim{SessionID:session,Feature:feature,Scope:scope,Token:token}); err!=nil { fail(err) }
			fmt.Printf("claim=%s scope=%s\n",token,scope)
			return
		}
	}

	if agent == "" {
		fmt.Fprintln(os.Stderr, "--agent is required for activation")
		os.Exit(2)
	}
	result, err := activation.Activate(workspace, agent, role)
	if err != nil { fail(err) }
	fmt.Printf("agent=%s state=%s coordination=%s\n", result.SessionID, result.State, result.CoordinationDir)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "orchestrator:",err)
	os.Exit(1)
}
