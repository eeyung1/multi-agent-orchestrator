package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var safe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

type Result struct { Branch, Path string }

func Create(primary, feature, base string) (Result,error) {
	feature=strings.TrimSpace(feature); base=strings.TrimSpace(base)
	if feature=="" { return Result{},fmt.Errorf("feature is required") }
	if base=="" { base="main" }
	name:=strings.Trim(safe.ReplaceAllString(feature,"-"),"-")
	if name=="" { return Result{},fmt.Errorf("feature produces empty branch name") }
	branch:="agent/"+name
	parent:=filepath.Join(primary,".agent-worktrees")
	if err:=os.MkdirAll(parent,0o755); err!=nil { return Result{},err }
	path:=filepath.Join(parent,name)
	if _,err:=os.Stat(path); !os.IsNotExist(err) { return Result{},fmt.Errorf("worktree path already exists: %s",path) }
	cmd:=exec.Command("git","-C",primary,"worktree","add","-b",branch,path,base)
	out,err:=cmd.CombinedOutput()
	if err!=nil { return Result{},fmt.Errorf("git worktree add: %w: %s",err,strings.TrimSpace(string(out))) }
	return Result{Branch:branch,Path:path},nil
}


func Remove(primary string, result Result) error {
	if strings.TrimSpace(result.Path)=="" || strings.TrimSpace(result.Branch)=="" { return fmt.Errorf("worktree path and branch are required") }
	parent:=filepath.Join(primary,".agent-worktrees")
	rel,err:=filepath.Rel(parent,result.Path); if err!=nil{return err}
	if rel=="." || rel==".." || strings.HasPrefix(rel,".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to remove worktree outside managed directory: %s",result.Path)
	}
	cmd:=exec.Command("git","-C",primary,"worktree","remove","--force",result.Path)
	if out,err:=cmd.CombinedOutput();err!=nil{return fmt.Errorf("git worktree remove: %w: %s",err,strings.TrimSpace(string(out)))}
	cmd=exec.Command("git","-C",primary,"branch","-D",result.Branch)
	if out,err:=cmd.CombinedOutput();err!=nil{return fmt.Errorf("git branch delete: %w: %s",err,strings.TrimSpace(string(out)))}
	return nil
}
