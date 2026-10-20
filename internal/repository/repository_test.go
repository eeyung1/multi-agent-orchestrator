package repository
import("os";"os/exec";"path/filepath";"testing")
func TestPrimaryFromLinkedWorktree(t *testing.T){
	root:=t.TempDir(); run(t,root,"git","init","-b","main");run(t,root,"git","config","user.email","test@example.com");run(t,root,"git","config","user.name","Test")
	os.WriteFile(filepath.Join(root,"x"),[]byte("x"),0644);run(t,root,"git","add","x");run(t,root,"git","commit","-m","init")
	linked:=filepath.Join(t.TempDir(),"linked");run(t,root,"git","worktree","add","-b","linked",linked,"main")
	got,err:=Primary(linked);if err!=nil{t.Fatal(err)}
	want,_:=filepath.Abs(root);if got!=want{t.Fatalf("got %s want %s",got,want)}
}
func run(t *testing.T,dir,name string,args ...string){t.Helper();c:=exec.Command(name,args...);c.Dir=dir;if out,err:=c.CombinedOutput();err!=nil{t.Fatalf("%v %s",err,out)}}
