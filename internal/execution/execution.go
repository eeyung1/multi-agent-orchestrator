package execution

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
)

type Command struct { Program string; Args []string }
type Request struct { Primary,Worktree,SessionID string; Command Command; Timeout time.Duration; HeartbeatInterval time.Duration }
type Result struct { ExitCode int; Output string }

func Run(req Request)(Result,error){
	if strings.TrimSpace(req.SessionID)=="" { return Result{},fmt.Errorf("session is required") }
	if strings.TrimSpace(req.Command.Program)=="" { return Result{},fmt.Errorf("agent program is required") }
	info,err:=os.Stat(req.Worktree);if err!=nil||!info.IsDir(){return Result{},fmt.Errorf("worktree is not a directory: %s",req.Worktree)}
	if req.Timeout<=0{req.Timeout=30*time.Minute}
	if req.HeartbeatInterval<=0{req.HeartbeatInterval=30*time.Second}
	if err:=coordination.VerifyExecution(req.Primary,req.SessionID,req.Worktree);err!=nil{return Result{},fmt.Errorf("execution ownership check: %w",err)}
	ctx,cancel:=context.WithTimeout(context.Background(),req.Timeout);defer cancel()
	cmd:=exec.CommandContext(ctx,req.Command.Program,req.Command.Args...)
	cmd.Dir=req.Worktree
	cmd.Env=append(os.Environ(),"ORCHESTRATOR_SESSION_ID="+req.SessionID,"ORCHESTRATOR_WORKTREE="+req.Worktree)
	var out []byte
	var runErr error
	done:=make(chan struct{})
	go func(){ out,runErr=cmd.CombinedOutput(); close(done) }()
	ticker:=time.NewTicker(req.HeartbeatInterval); defer ticker.Stop()
	for {
		select {
		case <-done:
			goto finished
		case <-ticker.C:
			if err:=coordination.VerifyExecution(req.Primary,req.SessionID,req.Worktree);err!=nil{
				cancel(); <-done
				return Result{ExitCode:-1,Output:strings.TrimSpace(string(out))},fmt.Errorf("execution ownership lost: %w",err)
			}
			if err:=coordination.Heartbeat(req.Primary,req.SessionID);err!=nil{
				cancel(); <-done
				return Result{ExitCode:-1,Output:strings.TrimSpace(string(out))},fmt.Errorf("execution heartbeat failed: %w",err)
			}
		}
	}
finished:
	if err:=coordination.VerifyExecution(req.Primary,req.SessionID,req.Worktree);err!=nil{return Result{ExitCode:-1,Output:strings.TrimSpace(string(out))},fmt.Errorf("execution result rejected after ownership loss: %w",err)}
	output:=strings.TrimSpace(string(out));exitCode:=0
	if runErr!=nil{
		exitCode=-1;if ee,ok:=runErr.(*exec.ExitError);ok{exitCode=ee.ExitCode()}
		if err:=coordination.Transition(req.Primary,req.SessionID,"BLOCKED");err!=nil{return Result{ExitCode:exitCode,Output:output},fmt.Errorf("agent execution failed and blocking session failed: %w",err)}
		if err:=appendExecution(req,exitCode,output);err!=nil{return Result{ExitCode:exitCode,Output:output},err}
		if ctx.Err()==context.DeadlineExceeded{return Result{ExitCode:exitCode,Output:output},fmt.Errorf("agent execution timed out")}
		return Result{ExitCode:exitCode,Output:output},fmt.Errorf("agent execution failed: %w",runErr)
	}
	if err:=coordination.Heartbeat(req.Primary,req.SessionID);err!=nil{return Result{},err}
	if err:=appendExecution(req,exitCode,output);err!=nil{return Result{},err}
	return Result{ExitCode:exitCode,Output:output},nil
}
func appendExecution(req Request,exitCode int,output string)error{
	path:=filepath.Join(req.Primary,"multi-agent-orchestrator","resolution.md");f,err:=os.OpenFile(path,os.O_WRONLY|os.O_APPEND,0o644);if err!=nil{return err};defer f.Close()
	safe:=strings.ReplaceAll(output,"\n","; ");display:=strings.Join(append([]string{req.Command.Program},req.Command.Args...)," ")
	_,err=fmt.Fprintf(f,"\n## Agent execution — %s\n- Session: %s\n- Worktree: %s\n- Command: %s\n- Exit code: %d\n- Output: %s\n- Recorded UTC: %s\n",time.Now().UTC().Format("20060102T150405Z"),req.SessionID,req.Worktree,display,exitCode,safe,time.Now().UTC().Format(time.RFC3339));return err
}
