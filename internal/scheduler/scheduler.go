package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
	"github.com/eeyung1/multi-agent-orchestrator/internal/workflow"
)

type Request struct{ Workspace,Agent,Role,Base string; Plan objective.Plan; Completed map[string]bool }
type Result struct{ Task objective.Task; SessionID,State,Branch,Worktree,Token string; Attempt,AssignmentGeneration uint64 }

func Acquire(req Request)(Result,error){
	primary,err:=repository.Primary(req.Workspace);if err!=nil{return Result{},err}
	if err:=objective.Validate(primary,req.Plan);err!=nil{return Result{},err}
	id,err:=repository.Identity(primary);if err!=nil{return Result{},err}
	store,err:=state.Open(primary,id);if err!=nil{return Result{},err}
	token,err:=randomToken();if err!=nil{return Result{},err}
	var selected objective.Task
	var attempt,generation uint64
	err=store.Update("ORCHESTRATOR","acquire-next",func(s *state.Snapshot)error{
		if existing,ok:=s.Objectives[req.Plan.ID];ok && existing.Description!=req.Plan.Objective{return fmt.Errorf("objective %s definition changed",req.Plan.ID)}
		s.Objectives[req.Plan.ID]=state.Objective{ID:req.Plan.ID,Description:req.Plan.Objective,Status:"ACTIVE"}
		for _,t:=range req.Plan.Tasks{
			key:=taskKey(req.Plan.ID,t.ID)
			if old,ok:=s.Tasks[key];ok {
				if old.Feature!=t.Feature||old.Scope!=t.Scope||old.Priority!=t.Priority||strings.Join(old.DependsOn,"\x00")!=strings.Join(t.DependsOn,"\x00"){return fmt.Errorf("task %s definition changed",t.ID)}
				continue
			}
			s.Tasks[key]=state.Task{ID:t.ID,ObjectiveID:req.Plan.ID,Feature:t.Feature,Scope:t.Scope,DependsOn:append([]string(nil),t.DependsOn...),Priority:t.Priority,Status:"PENDING"}
		}
		completed:=map[string]bool{}
		for id,v:=range req.Completed{if v{completed[id]=true}}
		for _,st:=range s.Tasks{if st.ObjectiveID==req.Plan.ID&&st.Status=="COMPLETE"{completed[st.ID]=true}}
		active:=[]string{}
		for _,st:=range s.Tasks{if st.Status=="ASSIGNED"{active=append(active,st.Scope)}}
		eligible:=objective.Eligible(req.Plan,completed,active)
		for _,candidate:=range eligible{
			st:=s.Tasks[taskKey(req.Plan.ID,candidate.ID)]
			if st.Status!="PENDING"&&st.Status!="BLOCKED"{continue}
			selected=candidate
			st.Status="ASSIGNED";st.Attempt++;st.AssignmentGeneration=s.Authority.Generation+1;st.Token=token
			attempt=st.Attempt;generation=st.AssignmentGeneration;s.Tasks[taskKey(req.Plan.ID,candidate.ID)]=st
			return nil
		}
		return errors.New("no eligible task")
	})
	if err!=nil{return Result{},err}

	started,err:=workflow.Start(workflow.Request{Workspace:req.Workspace,Agent:req.Agent,Role:req.Role,Feature:selected.Feature,Scope:selected.Scope,Token:token,Base:req.Base})
	if err!=nil{
		_ = store.Update("ORCHESTRATOR","acquire-rollback",func(s *state.Snapshot)error{st:=s.Tasks[taskKey(req.Plan.ID,selected.ID)];if st.Token==token{st.Status="BLOCKED";st.Token="";st.SessionID="";s.Tasks[taskKey(req.Plan.ID,selected.ID)]=st};return nil})
		return Result{Task:selected,SessionID:started.SessionID,State:started.State,Token:token,Attempt:attempt,AssignmentGeneration:generation},fmt.Errorf("acquire task %s: %w",selected.ID,err)
	}
	if err:=store.Update("ORCHESTRATOR","bind-assignment",func(s *state.Snapshot)error{
		st:=s.Tasks[taskKey(req.Plan.ID,selected.ID)]
		if st.Status!="ASSIGNED"||st.Token!=token||st.AssignmentGeneration!=generation{return errors.New("task assignment generation changed")}
		st.SessionID=started.SessionID;s.Tasks[taskKey(req.Plan.ID,selected.ID)]=st;return nil
	});err!=nil{return Result{},fmt.Errorf("bind task assignment: %w",err)}
	return Result{Task:selected,SessionID:started.SessionID,State:started.State,Branch:started.Branch,Worktree:started.Worktree,Token:token,Attempt:attempt,AssignmentGeneration:generation},nil
}

func Complete(workspace,objectiveID,taskID,token,sessionID string,generation uint64)error{
	primary,err:=repository.Primary(workspace);if err!=nil{return err};id,err:=repository.Identity(primary);if err!=nil{return err};store,err:=state.Open(primary,id);if err!=nil{return err}
	return store.Update("ORCHESTRATOR","task-complete",func(s *state.Snapshot)error{
		key:=taskKey(objectiveID,taskID);st,ok:=s.Tasks[key];if !ok{return errors.New("task not found")}
		if st.Status!="ASSIGNED"||st.Token!=token||st.SessionID!=sessionID||st.AssignmentGeneration!=generation{return errors.New("stale or unauthorized task completion")}
		st.Status="COMPLETE";st.Token="";s.Tasks[key]=st
		all:=true;for _,t:=range s.Tasks{if t.ObjectiveID==objectiveID&&t.Status!="COMPLETE"{all=false;break}}
		if all{o:=s.Objectives[objectiveID];o.Status="COMPLETE";s.Objectives[objectiveID]=o};return nil
	})
}
func taskKey(objectiveID,taskID string)string{return objectiveID+"::"+taskID}
func randomToken()(string,error){b:=make([]byte,16);if _,err:=rand.Read(b);err!=nil{return "",err};return "claim-"+hex.EncodeToString(b),nil}
