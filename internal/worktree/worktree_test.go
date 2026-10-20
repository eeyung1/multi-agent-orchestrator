package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
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
