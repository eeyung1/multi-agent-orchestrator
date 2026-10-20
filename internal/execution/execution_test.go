package execution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T)(string,string) {
	t.Helper(); root:=t.TempDir(); wt:=filepath.Join(root,"wt")
	if err:=os.MkdirAll(filepath.Join(root,"multi-agent-orchestrator"),0o755); err!=nil { t.Fatal(err) }
	if err:=os.MkdirAll(wt,0o755); err!=nil { t.Fatal(err) }
	meeting:="# Agent Meeting\n\n## Active Agents\n\n| Agent/session ID | Role | Status | Heartbeat UTC | Feature | Branch/worktree | Claim reference |\n| --- | --- | --- | --- | --- | --- | --- |\n| a1 | backend | WORKING | old | F1 | branch | t1 |\n"
	if err:=os.WriteFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"),[]byte(meeting),0o644); err!=nil { t.Fatal(err) }
	if err:=os.WriteFile(filepath.Join(root,"multi-agent-orchestrator","resolution.md"),[]byte("# Resolution\n"),0o644); err!=nil { t.Fatal(err) }
	return root,wt
}

func TestRunExecutesInsideWorktree(t *testing.T) {
	root,wt:=setup(t)
	got,err:=Run(Request{Primary:root,Worktree:wt,SessionID:"a1",Command:"printf $ORCHESTRATOR_SESSION_ID > result.txt",Timeout:time.Second})
	if err!=nil { t.Fatal(err) }
	if got.ExitCode!=0 { t.Fatalf("exit=%d",got.ExitCode) }
	data,err:=os.ReadFile(filepath.Join(wt,"result.txt")); if err!=nil { t.Fatal(err) }
	if string(data)!="a1" { t.Fatalf("result=%q",data) }
}

func TestRunFailureBlocksSession(t *testing.T) {
	root,wt:=setup(t)
	_,err:=Run(Request{Primary:root,Worktree:wt,SessionID:"a1",Command:"echo failed; exit 7",Timeout:time.Second})
	if err==nil { t.Fatal("expected failure") }
	data,_:=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"))
	if !strings.Contains(string(data),"| a1 | backend | BLOCKED |") { t.Fatalf("ledger=%s",data) }
}
