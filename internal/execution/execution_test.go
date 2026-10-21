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
	meeting:="# Agent Meeting\n\n## Active Agents\n\n| Agent/session ID | Role | Status | Heartbeat UTC | Feature | Branch/worktree | Claim reference |\n| --- | --- | --- | --- | --- | --- | --- |\n| a1 | backend | WORKING | old | F1 | branch @ "+wt+" | t1 |\n\n## Atomic Claims\n\n| Token | Agent/session ID | Feature | Scope | Claimed UTC |\n| --- | --- | --- | --- | --- |\n| t1 | a1 | F1 | result.txt | old |\n"
	if err:=os.WriteFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"),[]byte(meeting),0o644); err!=nil { t.Fatal(err) }
	if err:=os.WriteFile(filepath.Join(root,"multi-agent-orchestrator","resolution.md"),[]byte("# Resolution\n"),0o644); err!=nil { t.Fatal(err) }
	return root,wt
}

func TestRunExecutesInsideWorktree(t *testing.T) {
	root,wt:=setup(t)
	got,err:=Run(Request{Primary:root,Worktree:wt,SessionID:"a1",Command:Command{Program:"sh",Args:[]string{"-c","printf $ORCHESTRATOR_SESSION_ID > result.txt"}},Timeout:time.Second})
	if err!=nil { t.Fatal(err) }
	if got.ExitCode!=0 { t.Fatalf("exit=%d",got.ExitCode) }
	data,err:=os.ReadFile(filepath.Join(wt,"result.txt")); if err!=nil { t.Fatal(err) }
	if string(data)!="a1" { t.Fatalf("result=%q",data) }
}

func TestRunFailureBlocksSession(t *testing.T) {
	root,wt:=setup(t)
	_,err:=Run(Request{Primary:root,Worktree:wt,SessionID:"a1",Command:Command{Program:"sh",Args:[]string{"-c","echo failed; exit 7"}},Timeout:time.Second})
	if err==nil { t.Fatal("expected failure") }
	data,_:=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"))
	if !strings.Contains(string(data),"| a1 | backend | BLOCKED |") { t.Fatalf("ledger=%s",data) }
}

func TestMetacharactersAreLiteralArgs(t *testing.T){root,wt:=setup(t);marker:=filepath.Join(wt,"pwned");_,err:=Run(Request{Primary:root,Worktree:wt,SessionID:"a1",Command:Command{Program:"printf",Args:[]string{"%s","; touch "+marker}},Timeout:time.Second});if err!=nil{t.Fatal(err)};if _,err:=os.Stat(marker);!os.IsNotExist(err){t.Fatal("shell metacharacters were interpreted")}}

func TestRunRejectsWrongWorktree(t *testing.T) {
	root,wt:=setup(t)
	other:=filepath.Join(root,"other"); if err:=os.MkdirAll(other,0o755);err!=nil{t.Fatal(err)}
	_,err:=Run(Request{Primary:root,Worktree:other,SessionID:"a1",Command:Command{Program:"touch",Args:[]string{"should-not-run"}},Timeout:time.Second})
	if err==nil || !strings.Contains(err.Error(),"worktree does not match"){t.Fatalf("err=%v",err)}
	if _,err:=os.Stat(filepath.Join(other,"should-not-run"));!os.IsNotExist(err){t.Fatal("unauthorized command executed")}
	_ = wt
}

func TestRunRefreshesHeartbeatWhileWorking(t *testing.T) {
	root,wt:=setup(t)
	_,err:=Run(Request{Primary:root,Worktree:wt,SessionID:"a1",Command:Command{Program:"sh",Args:[]string{"-c","sleep 0.08"}},Timeout:time.Second,HeartbeatInterval:20*time.Millisecond})
	if err!=nil{t.Fatal(err)}
	data,err:=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"));if err!=nil{t.Fatal(err)}
	if strings.Contains(string(data),"| a1 | backend | WORKING | old |"){t.Fatalf("heartbeat was not refreshed: %s",data)}
}

func TestRunRejectsSessionWithoutClaim(t *testing.T) {
	root,wt:=setup(t)
	path:=filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md")
	data,err:=os.ReadFile(path);if err!=nil{t.Fatal(err)}
	lines:=strings.Split(string(data),"\n")
	out:=make([]string,0,len(lines))
	for _,line:=range lines{if strings.HasPrefix(line,"| t1 | a1 |"){continue};out=append(out,line)}
	if err:=os.WriteFile(path,[]byte(strings.Join(out,"\n")),0o644);err!=nil{t.Fatal(err)}
	_,err=Run(Request{Primary:root,Worktree:wt,SessionID:"a1",Command:Command{Program:"true"},Timeout:time.Second})
	if err==nil || !strings.Contains(err.Error(),"no active claims"){t.Fatalf("err=%v",err)}
}
