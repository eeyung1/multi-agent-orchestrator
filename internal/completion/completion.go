package completion

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
)

type Request struct {
	Primary   string
	Worktree  string
	SessionID string
	Token     string
	Feature   string
	Validate  string
}

type Result struct {
	State  string
	Output string
}

func Complete(req Request) (Result,error) {
	if strings.TrimSpace(req.Validate)=="" { return Result{},fmt.Errorf("validation command is required") }
	cmd:=exec.Command("sh","-c",req.Validate)
	cmd.Dir=req.Worktree
	out,err:=cmd.CombinedOutput()
	output:=strings.TrimSpace(string(out))
	if err!=nil {
		_ = appendResolution(req,"BLOCKED",output)
		_ = coordination.Transition(req.Primary,req.SessionID,"BLOCKED")
		return Result{State:"BLOCKED",Output:output},fmt.Errorf("validation failed: %w",err)
	}
	if err:=appendResolution(req,"COMPLETE",output); err!=nil { return Result{},err }
	if err:=coordination.Transition(req.Primary,req.SessionID,"COMPLETE"); err!=nil { return Result{},err }
	if err:=coordination.ReleaseClaim(req.Primary,req.SessionID,req.Token); err!=nil { return Result{},err }
	return Result{State:"COMPLETE",Output:output},nil
}

func appendResolution(req Request,state,output string) error {
	path:=filepath.Join(req.Primary,"multi-agent-orchestrator","resolution.md")
	f,err:=os.OpenFile(path,os.O_WRONLY|os.O_APPEND,0o644); if err!=nil { return err }
	defer f.Close()
	safe:=strings.ReplaceAll(output,"\n","; ")
	_,err=fmt.Fprintf(f,"\n## %s — %s\n- Session: %s\n- State: %s\n- Validation: `%s`\n- Evidence: %s\n- Recorded UTC: %s\n",
		req.Feature,req.Token,req.SessionID,state,req.Validate,safe,time.Now().UTC().Format(time.RFC3339))
	return err
}
