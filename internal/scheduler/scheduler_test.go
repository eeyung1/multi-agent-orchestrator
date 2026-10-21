package scheduler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
)

func initRepo(t *testing.T)string{
	t.Helper();d:=t.TempDir();run:=func(args ...string){c:=exec.Command("git",args...);c.Dir=d;if out,err:=c.CombinedOutput();err!=nil{t.Fatalf("git %v: %v %s",args,err,out)}}
	run("init","-b","main");run("config","user.email","test@example.com");run("config","user.name","Test");if err:=os.WriteFile(filepath.Join(d,"seed"),[]byte("x"),0644);err!=nil{t.Fatal(err)};run("add",".");run("commit","-m","seed")
	if _,err:=enrollment.Enroll(d,enrollment.Native,"test");err!=nil{t.Fatal(err)}
	return d
}
func TestAcquirePersistsAndPreventsDuplicateTask(t *testing.T){
	d:=initRepo(t);p:=objective.Plan{ID:"obj",Objective:"ship",Tasks:[]objective.Task{{ID:"a",Feature:"a",Scope:"backend",Priority:1}}}
	first,err:=Acquire(Request{Workspace:d,Agent:"one",Role:"coding-agent",Base:"main",Plan:p});if err!=nil{t.Fatal(err)}
	if first.Attempt!=1||first.AssignmentGeneration==0||first.Token==""{t.Fatalf("bad assignment: %+v",first)}
	if _,err:=Acquire(Request{Workspace:d,Agent:"two",Role:"coding-agent",Base:"main",Plan:p});err==nil{t.Fatal("expected duplicate acquisition rejection")}
}
func TestCompleteRejectsStaleGeneration(t *testing.T){
	d:=initRepo(t);p:=objective.Plan{ID:"obj",Objective:"ship",Tasks:[]objective.Task{{ID:"a",Feature:"a",Scope:"backend"}}}
	r,err:=Acquire(Request{Workspace:d,Agent:"one",Role:"coding-agent",Base:"main",Plan:p});if err!=nil{t.Fatal(err)}
	if err:=Complete(d,"obj","a",r.Token,r.SessionID,r.AssignmentGeneration+1);err==nil{t.Fatal("expected stale generation rejection")}
	if err:=Complete(d,"obj","a",r.Token,r.SessionID,r.AssignmentGeneration);err!=nil{t.Fatal(err)}
}
