package completion

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
)
type Request struct{Primary,Worktree,SessionID,Token,Feature string; Validate execution.Command}
type Result struct{State,Output string}
func Complete(req Request)(Result,error){
	if strings.TrimSpace(req.Validate.Program)==""{return Result{},fmt.Errorf("validation program is required")}
	cmd:=exec.Command(req.Validate.Program,req.Validate.Args...);cmd.Dir=req.Worktree;out,err:=cmd.CombinedOutput();output:=strings.TrimSpace(string(out))
	if err!=nil{
		if logErr:=appendResolution(req,"BLOCKED",output);logErr!=nil{return Result{State:"BLOCKED",Output:output},logErr}
		if transitionErr:=coordination.Transition(req.Primary,req.SessionID,"BLOCKED");transitionErr!=nil{return Result{State:"BLOCKED",Output:output},transitionErr}
		return Result{State:"BLOCKED",Output:output},fmt.Errorf("validation failed: %w",err)
	}
	if err:=appendResolution(req,"COMPLETE",output);err!=nil{return Result{},err}
	if err:=coordination.Transition(req.Primary,req.SessionID,"COMPLETE");err!=nil{return Result{},err}
	if err:=coordination.ReleaseClaim(req.Primary,req.SessionID,req.Token);err!=nil{return Result{},err}
	return Result{State:"COMPLETE",Output:output},nil
}
func appendResolution(req Request,state,output string)error{
	path:=filepath.Join(req.Primary,"multi-agent-orchestrator","resolution.md");f,err:=os.OpenFile(path,os.O_WRONLY|os.O_APPEND,0o644);if err!=nil{return err};defer f.Close()
	safe:=strings.ReplaceAll(output,"\n","; ");display:=strings.Join(append([]string{req.Validate.Program},req.Validate.Args...)," ")
	_,err=fmt.Fprintf(f,"\n## %s — %s\n- Session: %s\n- State: %s\n- Validation: %s\n- Evidence: %s\n- Recorded UTC: %s\n",req.Feature,req.Token,req.SessionID,state,display,safe,time.Now().UTC().Format(time.RFC3339));return err
}
