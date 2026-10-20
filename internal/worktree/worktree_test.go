package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateIsolatedWorktree(t *testing.T) {
	root:=t.TempDir()
	run(t,root,"git","init","-b","main")
	run(t,root,"git","config","user.email","test@example.com")
	run(t,root,"git","config","user.name","Test")
	if err:=os.WriteFile(filepath.Join(root,"README.md"),[]byte("x\n"),0o644); err!=nil { t.Fatal(err) }
	run(t,root,"git","add","README.md"); run(t,root,"git","commit","-m","init")
	got,err:=Create(root,"feature one","main")
	if err!=nil { t.Fatal(err) }
	if got.Branch!="agent/feature-one" { t.Fatalf("branch=%s",got.Branch) }
	if _,err:=os.Stat(filepath.Join(got.Path,"README.md")); err!=nil { t.Fatal(err) }
}

func run(t *testing.T,dir,name string,args ...string) {
	t.Helper(); c:=exec.Command(name,args...); c.Dir=dir
	if out,err:=c.CombinedOutput(); err!=nil { t.Fatalf("%s: %v: %s",name,err,out) }
}


func TestRemoveManagedWorktreeAndBranch(t *testing.T) {
	root:=t.TempDir();run(t,root,"git","init","-b","main");run(t,root,"git","config","user.email","test@example.com");run(t,root,"git","config","user.name","Test")
	os.WriteFile(filepath.Join(root,"README.md"),[]byte("x\n"),0o644);run(t,root,"git","add","README.md");run(t,root,"git","commit","-m","init")
	wt,err:=Create(root,"cleanup","main");if err!=nil{t.Fatal(err)}
	if err:=Remove(root,wt);err!=nil{t.Fatal(err)}
	if _,err:=os.Stat(wt.Path);!os.IsNotExist(err){t.Fatalf("worktree still exists: %v",err)}
	cmd:=exec.Command("git","branch","--list",wt.Branch);cmd.Dir=root;out,err:=cmd.CombinedOutput();if err!=nil{t.Fatal(err)}
	if strings.TrimSpace(string(out))!=""{t.Fatalf("branch still exists: %s",out)}
}

func TestRemoveRejectsUnmanagedPath(t *testing.T) {
	root:=t.TempDir()
	err:=Remove(root,Result{Branch:"agent/x",Path:filepath.Join(root,"outside")})
	if err==nil { t.Fatal("expected unmanaged path rejection") }
}
