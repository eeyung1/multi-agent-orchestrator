package completion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
)

func setup(t *testing.T)(string,string) {
	t.Helper(); root:=t.TempDir(); wt:=filepath.Join(root,"wt")
	if err:=os.MkdirAll(filepath.Join(root,"multi-agent-orchestrator"),0o755); err!=nil { t.Fatal(err) }
	if err:=os.MkdirAll(wt,0o755); err!=nil { t.Fatal(err) }
	meeting:="# Agent Meeting\n\n## Active Agents\n\n| Agent/session ID | Role | Status | Heartbeat UTC | Feature | Branch/worktree | Claim reference |\n| --- | --- | --- | --- | --- | --- | --- |\n| a1 | backend | WORKING | old | F1 | branch | t1 |\n\n## Atomic Claims\n\n| Token | Agent/session ID | Feature | Scope | Claimed UTC |\n| --- | --- | --- | --- | --- |\n| t1 | a1 | F1 | internal/foo | old |\n"
	if err:=os.WriteFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"),[]byte(meeting),0o644); err!=nil { t.Fatal(err) }
	if err:=os.WriteFile(filepath.Join(root,"multi-agent-orchestrator","resolution.md"),[]byte("# Resolution\n"),0o644); err!=nil { t.Fatal(err) }
	return root,wt
}

func TestCompleteValidatesRecordsAndReleases(t *testing.T) {
	root,wt:=setup(t)
	got,err:=Complete(Request{Primary:root,Worktree:wt,SessionID:"a1",Token:"t1",Feature:"F1",Validate:"printf ok"})
	if err!=nil { t.Fatal(err) }
	if got.State!="COMPLETE" { t.Fatalf("state=%s",got.State) }
	m,_:=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"))
	if !strings.Contains(string(m),"| a1 | backend | COMPLETE |") || strings.Contains(string(m),"| t1 | a1 | F1 |") { t.Fatalf("ledger=%s",m) }
	r,_:=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","resolution.md"))
	if !strings.Contains(string(r),"- State: COMPLETE") || !strings.Contains(string(r),"- Evidence: ok") { t.Fatalf("resolution=%s",r) }
}

func TestFailureBlocksAndKeepsClaim(t *testing.T) {
	root,wt:=setup(t)
	got,err:=Complete(Request{Primary:root,Worktree:wt,SessionID:"a1",Token:"t1",Feature:"F1",Validate:"echo broken; exit 1"})
	if err==nil || got.State!="BLOCKED" { t.Fatalf("got=%+v err=%v",got,err) }
	m,_:=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"))
	if !strings.Contains(string(m),"| a1 | backend | BLOCKED |") || !strings.Contains(string(m),"| t1 | a1 | F1 |") { t.Fatalf("ledger=%s",m) }
}

var _ = coordination.Claim{}
