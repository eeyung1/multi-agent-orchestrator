package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"path/filepath"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/activation"
	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/completion"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	runflow "github.com/eeyung1/multi-agent-orchestrator/internal/run"
	"github.com/eeyung1/multi-agent-orchestrator/internal/worktree"
	workflowrun "github.com/eeyung1/multi-agent-orchestrator/internal/workflow"
)

func main() {
	var workspace, agent, role, session, feature, scope, token, state, nextSession, base, worktreePath, validateProgram, agentProgram, planFile, completedTasks string
	var validateArgs, agentArgs stringList
	var heartbeat, release, createWorktree, start, complete, execute, fullRun, preflight, enroll, acquireNext bool
	var timeout time.Duration
	var enrollmentDisposition, enrollmentProvenance string
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
	flag.BoolVar(&complete, "complete", false, "validate and complete an agent session")
	flag.StringVar(&worktreePath, "worktree", "", "agent worktree path")
	flag.StringVar(&validateProgram, "validate-program", "", "validation executable")
	flag.Var(&validateArgs, "validate-arg", "validation argument; repeat for multiple arguments")
	flag.BoolVar(&execute, "execute", false, "run an implementation agent command inside its worktree")
	flag.StringVar(&agentProgram, "agent-program", "", "implementation agent executable")
	flag.Var(&agentArgs, "agent-arg", "implementation argument; repeat for multiple arguments")
	flag.DurationVar(&timeout, "timeout", 30*time.Minute, "agent execution timeout")
	flag.BoolVar(&fullRun, "run", false, "run start, execution, validation and completion end-to-end")
	flag.BoolVar(&preflight, "preflight", false, "inspect repository coordination authority before enrollment")
	flag.BoolVar(&enroll, "enroll", false, "explicitly enroll repository under ORCHESTRATOR authority")
	flag.StringVar(&enrollmentDisposition, "enrollment-disposition", "", "enrollment disposition: NATIVE or COMPATIBLE_ADAPTER")
	flag.StringVar(&enrollmentProvenance, "enrollment-provenance", "", "human/decision provenance for enrollment")
	flag.BoolVar(&acquireNext, "acquire-next", false, "acquire the next eligible task from an objective plan")
	flag.StringVar(&planFile, "plan-file", "", "JSON objective plan file")
	flag.StringVar(&completedTasks, "completed-tasks", "", "comma-separated completed task IDs")
	flag.Parse()

	primary, err := filepath.Abs(workspace)
	if err != nil { fail(err) }

	if acquireNext {
		data,err:=os.ReadFile(planFile);if err!=nil{fail(err)}
		var plan objective.Plan;if err:=json.Unmarshal(data,&plan);err!=nil{fail(err)}
		done:=map[string]bool{};for _,id:=range strings.Split(completedTasks,","){if id=strings.TrimSpace(id);id!=""{done[id]=true}}
		result,err:=scheduler.Acquire(scheduler.Request{Workspace:workspace,Agent:agent,Role:role,Base:base,Plan:plan,Completed:done});if err!=nil{fail(err)}
		out,err:=json.MarshalIndent(result,"","  ");if err!=nil{fail(err)};fmt.Println(string(out));return
	}

	if preflight {
		report, err := enrollment.Preflight(workspace)
		if err != nil { fail(err) }
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil { fail(err) }
		fmt.Println(string(data))
		return
	}
	if enroll {
		report, err := enrollment.Enroll(workspace, enrollment.Disposition(enrollmentDisposition), enrollmentProvenance)
		if err != nil { fail(err) }
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil { fail(err) }
		fmt.Println(string(data))
		return
	}

	if fullRun {
		result,err:=runflow.Run(runflow.Request{Workspace:workspace,Agent:agent,Role:role,Feature:feature,Scope:scope,Token:token,Base:base,AgentCommand:execution.Command{Program:agentProgram,Args:agentArgs},Validate:execution.Command{Program:validateProgram,Args:validateArgs},Timeout:timeout})
		if err!=nil { fail(err) }
		fmt.Printf("agent=%s state=%s branch=%s worktree=%s\n",result.SessionID,result.State,result.Branch,result.Worktree)
		return
	}

	if execute {
		result,err:=execution.Run(execution.Request{Primary:primary,Worktree:worktreePath,SessionID:session,Command:execution.Command{Program:agentProgram,Args:agentArgs},Timeout:timeout})
		if err!=nil { fail(err) }
		fmt.Printf("exit=%d output=%s\n",result.ExitCode,result.Output)
		return
	}

	if complete {
		result,err:=completion.Complete(completion.Request{Primary:primary,Worktree:worktreePath,SessionID:session,Token:token,Feature:feature,Validate:execution.Command{Program:validateProgram,Args:validateArgs}})
		if err!=nil { fail(err) }
		fmt.Printf("state=%s validation=%s\n",result.State,result.Output)
		return
	}

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

type stringList []string
func (s *stringList) String() string{return fmt.Sprint([]string(*s))}
func (s *stringList) Set(v string) error{*s=append(*s,v);return nil}
