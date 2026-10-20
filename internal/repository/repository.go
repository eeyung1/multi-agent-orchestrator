package repository

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func Primary(workspace string)(string,error) {
	abs,err:=filepath.Abs(workspace); if err!=nil{return "",err}
	top,err:=git(abs,"rev-parse","--show-toplevel"); if err!=nil{return "",fmt.Errorf("resolve git worktree: %w",err)}
	list,err:=git(top,"worktree","list","--porcelain"); if err!=nil{return "",fmt.Errorf("list git worktrees: %w",err)}
	for _,line:=range strings.Split(list,"\n") {
		if strings.HasPrefix(line,"worktree ") {
			return filepath.Clean(strings.TrimSpace(strings.TrimPrefix(line,"worktree "))),nil
		}
	}
	return "",fmt.Errorf("primary worktree not found")
}
func git(dir string,args ...string)(string,error){
	cmd:=exec.Command("git",args...);cmd.Dir=dir
	out,err:=cmd.CombinedOutput()
	if err!=nil{return "",fmt.Errorf("%s: %w",strings.TrimSpace(string(out)),err)}
	return strings.TrimSpace(string(out)),nil
}
