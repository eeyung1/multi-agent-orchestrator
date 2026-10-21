package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
)

func repo(t *testing.T)string{t.Helper();d:=t.TempDir();run:=func(args ...string){c:=exec.Command("git",args...);c.Dir=d;if b,e:=c.CombinedOutput();e!=nil{t.Fatalf("%v %s",e,b)}};run("init","-b","main");run("config","user.email","test@example.com");run("config","user.name","Test");os.WriteFile(filepath.Join(d,"seed"),[]byte("x"),0644);run("add",".");run("commit","-m","seed");if _,e:=enrollment.Enroll(d,enrollment.Native,"test");e!=nil{t.Fatal(e)};return d}

func TestCollectBuildAndApprove(t *testing.T){
	d:=repo(t);p:=objective.Plan{ID:"obj",Objective:"ship",Tasks:[]objective.Task{{ID:"a",Feature:"a",Scope:"backend"}}};a,e:=scheduler.Acquire(scheduler.Request{Workspace:d,Agent:"one",Role:"coding-agent",Base:"main",Plan:p});if e!=nil{t.Fatal(e)}
	rec,e:=Collect(ResultInput{Workspace:d,ObjectiveID:"obj",TaskID:"a",SessionID:a.SessionID,Token:a.Token,Branch:a.Branch,Worktree:a.Worktree,AssignmentGeneration:a.AssignmentGeneration,Validation:"go test ./...: PASS",Artifacts:[]string{"report.txt"}});if e!=nil{t.Fatal(e)};if rec.HeadSHA==""{t.Fatal("missing head")}
	pkg,e:=Build(d,"obj");if e!=nil{t.Fatal(e)};if pkg.Objective.Status!="AWAITING_REVIEW"||len(pkg.Results)!=1{t.Fatalf("%+v",pkg)}
	if e:=Decide(d,"obj","APPROVED","user");e!=nil{t.Fatal(e)};pkg,_=Build(d,"obj");if pkg.Objective.Status!="APPROVED"{t.Fatalf("%+v",pkg.Objective)}
}
func TestCollectRejectsStaleGeneration(t *testing.T){
	d:=repo(t);p:=objective.Plan{ID:"obj",Objective:"ship",Tasks:[]objective.Task{{ID:"a",Feature:"a",Scope:"backend"}}};a,e:=scheduler.Acquire(scheduler.Request{Workspace:d,Agent:"one",Role:"coding-agent",Base:"main",Plan:p});if e!=nil{t.Fatal(e)}
	_,e=Collect(ResultInput{Workspace:d,ObjectiveID:"obj",TaskID:"a",SessionID:a.SessionID,Token:a.Token,Worktree:a.Worktree,AssignmentGeneration:a.AssignmentGeneration+1,Validation:"pass"});if e==nil{t.Fatal("expected stale result rejection")}
}
