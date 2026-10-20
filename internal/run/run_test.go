package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func repo(t *testing.T) string {
	t.Helper(); root:=t.TempDir()
	cmds:=[][]string{{"init","-b","main"},{"config","user.email","test@example.com"},{"config","user.name","Test"}}
	for _,args:=range cmds { git(t,root,args...) }
	if err:=os.WriteFile(filepath.Join(root,"README.md"),[]byte("x\n"),0o644); err!=nil { t.Fatal(err) }
	git(t,root,"add","README.md"); git(t,root,"commit","-m","init")
	return root
}
func git(t *testing.T,dir string,args ...string){t.Helper(); c:=exec.Command("git",args...);c.Dir=dir;if out,err:=c.CombinedOutput();err!=nil{t.Fatalf("git %v: %v %s",args,err,out)}}

func TestEndToEndRunCompletes(t *testing.T) {
	root:=repo(t)
	got,err:=Run(Request{Workspace:root,Agent:"Codex",Role:"backend",Feature:"e2e",Scope:"result.txt",Token:"e2e-token",Base:"main",AgentCommand:"printf implemented > result.txt",Validate:"test \"$(cat result.txt)\" = implemented",Timeout:time.Second})
	if err!=nil { t.Fatal(err) }
	if got.State!="COMPLETE" { t.Fatalf("state=%s",got.State) }
	data,err:=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md")); if err!=nil { t.Fatal(err) }
	if !strings.Contains(string(data),"| "+got.SessionID+" | backend | COMPLETE |") { t.Fatalf("ledger=%s",data) }
}

func TestExecutionFailureStopsBeforeValidation(t *testing.T) {
	root:=repo(t)
	got,err:=Run(Request{Workspace:root,Agent:"Codex",Role:"backend",Feature:"fail",Scope:"result.txt",Token:"fail-token",Base:"main",AgentCommand:"exit 3",Validate:"touch should-not-exist",Timeout:time.Second})
	if err==nil || got.State!="BLOCKED" { t.Fatalf("got=%+v err=%v",got,err) }
	if _,err:=os.Stat(filepath.Join(got.Worktree,"should-not-exist")); !os.IsNotExist(err) { t.Fatal("validation ran after execution failure") }
}
