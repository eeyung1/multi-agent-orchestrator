package enrollment

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func repo(t *testing.T) string {
	t.Helper()
	root:=t.TempDir()
	cmd:=exec.Command("git","init"); cmd.Dir=root
	if out,err:=cmd.CombinedOutput();err!=nil{t.Fatalf("git init: %v %s",err,out)}
	return root
}

func TestPreflightNative(t *testing.T){
	root:=repo(t)
	r,err:=Preflight(root);if err!=nil{t.Fatal(err)}
	if r.Disposition!=Native || len(r.Findings)!=0{t.Fatalf("%+v",r)}
}

func TestPreflightDetectsLegacyToolAndInstruction(t *testing.T){
	root:=repo(t)
	if err:=os.WriteFile(filepath.Join(root,"AGENTS.md"),[]byte("Before editing, agent must claim every scope.\n"),0644);err!=nil{t.Fatal(err)}
	if err:=os.MkdirAll(filepath.Join(root,"scripts"),0755);err!=nil{t.Fatal(err)}
	if err:=os.WriteFile(filepath.Join(root,"scripts","agent_coordination.py"),[]byte("# legacy"),0644);err!=nil{t.Fatal(err)}
	r,err:=Preflight(root);if err!=nil{t.Fatal(err)}
	if r.Disposition!=MigrationRequired || len(r.Findings)!=2{t.Fatalf("%+v",r)}
}

func TestNativeEnrollmentRefusesFindings(t *testing.T){
	root:=repo(t)
	if err:=os.WriteFile(filepath.Join(root,"AGENTS.md"),[]byte("agent claim scope"),0644);err!=nil{t.Fatal(err)}
	if _,err:=Enroll(root,Native,"user-approved");err==nil{t.Fatal("expected refusal")}
}

func TestNativeEnrollmentAndRequire(t *testing.T){
	root:=repo(t)
	if _,err:=Enroll(root,Native,"user-approved objective");err!=nil{t.Fatal(err)}
	if err:=RequireEnrolled(root);err!=nil{t.Fatal(err)}
}
