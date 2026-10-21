package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
	"github.com/eeyung1/multi-agent-orchestrator/internal/workflow"
)

type Request struct{ Workspace,Agent,Role,Base string; Plan objective.Plan; Completed map[string]bool }
type Result struct{ Task objective.Task; SessionID,State,Branch,Worktree,Token string }

func Acquire(req Request)(Result,error){
	primary,err:=repository.Primary(req.Workspace);if err!=nil{return Result{},err}
	if err:=objective.Validate(primary,req.Plan);err!=nil{return Result{},err}
	id,err:=repository.Identity(primary);if err!=nil{return Result{},err}
	store,err:=state.Open(primary,id);if err!=nil{return Result{},err}
	snap,err:=store.Read();if err!=nil{return Result{},err}
	active:=make([]string,0,len(snap.Claims));for _,c:=range snap.Claims{active=append(active,c.Scope)}
	eligible:=objective.Eligible(req.Plan,req.Completed,active)
	if len(eligible)==0{return Result{},errors.New("no eligible task")}
	task:=eligible[0]
	token,err:=randomToken();if err!=nil{return Result{},err}
	started,err:=workflow.Start(workflow.Request{Workspace:req.Workspace,Agent:req.Agent,Role:req.Role,Feature:task.Feature,Scope:task.Scope,Token:token,Base:req.Base})
	if err!=nil{return Result{Task:task,SessionID:started.SessionID,State:started.State,Token:token},fmt.Errorf("acquire task %s: %w",task.ID,err)}
	return Result{Task:task,SessionID:started.SessionID,State:started.State,Branch:started.Branch,Worktree:started.Worktree,Token:token},nil
}
func randomToken()(string,error){b:=make([]byte,16);if _,err:=rand.Read(b);err!=nil{return "",err};return "claim-"+hex.EncodeToString(b),nil}
