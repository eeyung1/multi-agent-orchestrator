package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eeyung1/multi-agent-orchestrator/internal/activation"
	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
)

func main() {
	var workspace, agent, role, session, feature, scope, token, state string
	var heartbeat bool
	flag.StringVar(&workspace, "workspace", ".", "target project workspace")
	flag.StringVar(&agent, "agent", "", "agent identity prefix")
	flag.StringVar(&role, "role", "coding-agent", "agent role")
	flag.StringVar(&session, "session", "", "existing agent session ID")
	flag.StringVar(&feature, "feature", "", "feature identifier")
	flag.StringVar(&scope, "scope", "", "claimed file scope")
	flag.StringVar(&token, "token", "", "claim token")
	flag.StringVar(&state, "state", "", "lifecycle state transition")
	flag.BoolVar(&heartbeat, "heartbeat", false, "refresh session heartbeat")
	flag.Parse()

	primary, err := filepath.Abs(workspace)
	if err != nil { fail(err) }

	if session != "" {
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
