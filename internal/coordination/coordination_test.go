package coordination

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) string {
	t.Helper()
	root:=t.TempDir()
	dir:=filepath.Join(root,"multi-agent-orchestrator")
	if err:=os.MkdirAll(dir,0o755); err!=nil { t.Fatal(err) }
	body:="# Agent Meeting\n\n## Active Agents\n\n| Agent/session ID | Role | Status | Heartbeat UTC | Feature | Branch/worktree | Claim reference |\n| --- | --- | --- | --- | --- | --- | --- |\n| agent-1 | backend | ACTIVE | old | activation | - | bootstrap |\n"
	if err:=os.WriteFile(filepath.Join(dir,"agentmeeting.md"),[]byte(body),0o644); err!=nil { t.Fatal(err) }
	return root
}

func TestClaimRejectsConflict(t *testing.T) {
	root:=setup(t)
	first:=Claim{SessionID:"agent-1",Feature:"F1",Scope:"internal/foo.go",Token:"token-1"}
	if err:=ClaimScope(root,first); err!=nil { t.Fatal(err) }
	if err:=ClaimScope(root,Claim{SessionID:"agent-2",Feature:"F2",Scope:"internal/foo.go",Token:"token-2"}); err==nil {
		t.Fatal("expected conflicting scope to be rejected")
	}
}

func TestHeartbeatUpdatesAgent(t *testing.T) {
	root:=setup(t)
	if err:=Heartbeat(root,"agent-1"); err!=nil { t.Fatal(err) }
	data,_:=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"))
	if strings.Contains(string(data),"| old |") { t.Fatal("heartbeat was not updated") }
}

func TestTransitionUpdatesState(t *testing.T) {
	root:=setup(t)
	if err:=Transition(root,"agent-1","WORKING"); err!=nil { t.Fatal(err) }
	data,_:=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"))
	if !strings.Contains(string(data),"| agent-1 | backend | WORKING |") { t.Fatalf("state not updated: %s",data) }
}
