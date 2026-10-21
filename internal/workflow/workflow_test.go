package workflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"\n\n\t"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
)

func TestStartRunsActivationClaimWorktreeAndWorkingTransition(t *testing.T) {
	root:=t.TempDir()
	run(t,root,"git","init","-b","main")
	run(t,root,"git","config","user.email","test@example.com")
	run(t,root,"git","config","user.name","Test")
	if err:=os.WriteFile(filepath.Join(root,"README.md"),[]byte("x\n"),0o644); err!=nil { t.Fatal(err) }
	run(t,root,"git","add","README.md"); run(t,root,"git","commit","-m","init")

	got,err:=Start(Request{Workspace:root,Agent:"Codex",Role:"backend",Feature:"phase-four",Scope:"internal/phase4",Token:"claim-4",Base:"main"})
	if err!=nil { t.Fatal(err) }
	if got.State!="WORKING" || got.Branch!="agent/phase-four" { t.Fatalf("unexpected result: %+v",got) }
	data,err:=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md")); if err!=nil { t.Fatal(err) }
	text:=string(data)
	if !strings.Contains(text,"| "+got.SessionID+" | backend | WORKING |") { t.Fatal("WORKING state missing") }
	if !strings.Contains(text,"| phase-four | "+got.Branch+" @ "+got.Worktree+" | claim-4 |") { t.Fatal("assignment missing") }
}

func run(t *testing.T,dir,name string,args ...string) {
	t.Helper(); c:=exec.Command(name,args...); c.Dir=dir
	if out,err:=c.CombinedOutput(); err!=nil { t.Fatalf("%s: %v: %s",name,err,out) }
}

func TestStartRequiresAuthorityEnrollment(t *testing.T) {
	root:=t.TempDir()
	run(t,root,"git","init","-b","main")
	run(t,root,"git","config","user.email","test@example.com")
	run(t,root,"git","config","user.name","Test")
	if err:=os.WriteFile(filepath.Join(root,"README.md"),[]byte("x\n"),0o644);err!=nil{t.Fatal(err)}
	run(t,root,"git","add","README.md");run(t,root,"git","commit","-m","init")
	if _,err:=Start(Request{Workspace:root,Agent:"Codex",Role:"backend",Feature:"x",Scope:"internal/x",Token:"x",Base:"main"});err==nil{
		t.Fatal("expected unenrolled start to fail closed")
	}
}
