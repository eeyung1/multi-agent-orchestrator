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
	body:="# Agent Meeting\n\n## Active Agents\n\n| Agent/session ID | Role | Status | Heartbeat UTC | Feature | Branch/worktree | Claim reference |\n| --- | --- | --- | --- | --- | --- | --- |\n| agent-1 | backend | ACTIVE | old | activation | - | bootstrap |\n| agent-2 | reviewer | ACTIVE | old | activation | - | bootstrap |\n"
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
	for _,line:=range strings.Split(string(data),"\n") {
		if strings.HasPrefix(line,"| agent-1 |") && strings.Contains(line,"| old |") { t.Fatal("agent-1 heartbeat was not updated") }
	}
}

func TestTransitionUpdatesState(t *testing.T) {
	root:=setup(t)
	if err:=Transition(root,"agent-1","WORKING"); err!=nil { t.Fatal(err) }
	data,_:=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"))
	if !strings.Contains(string(data),"| agent-1 | backend | WORKING |") { t.Fatalf("state not updated: %s",data) }
}


func TestClaimRejectsParentChildOverlap(t *testing.T) {
	root:=setup(t)
	if err:=ClaimScope(root,Claim{SessionID:"agent-1",Feature:"F1",Scope:"internal",Token:"one"}); err!=nil { t.Fatal(err) }
	if err:=ClaimScope(root,Claim{SessionID:"agent-2",Feature:"F2",Scope:"internal/foo.go",Token:"two"}); err==nil { t.Fatal("expected overlapping scope rejection") }
}

func TestReleaseAndHandoff(t *testing.T) {
	root:=setup(t)
	if err:=ClaimScope(root,Claim{SessionID:"agent-1",Feature:"F1",Scope:"internal/foo.go",Token:"one"}); err!=nil { t.Fatal(err) }
	if err:=Handoff(root,"agent-1","one","agent-2"); err!=nil { t.Fatal(err) }
	data,_:=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"))
	if !strings.Contains(string(data),"| one | agent-2 |") { t.Fatal("handoff missing") }
	if err:=ReleaseClaim(root,"agent-2","one"); err!=nil { t.Fatal(err) }
	data,_=os.ReadFile(filepath.Join(root,"multi-agent-orchestrator","agentmeeting.md"))
	if strings.Contains(string(data),"| one | agent-2 |") { t.Fatal("claim not released") }
}


func TestInvalidLifecycleTransitionRejected(t *testing.T) {
	root:=setup(t)
	if err:=Transition(root,"agent-1","COMPLETE"); err==nil { t.Fatal("ACTIVE -> COMPLETE must be rejected") }
	if err:=Transition(root,"agent-1","WORKING"); err!=nil { t.Fatal(err) }
	if err:=Transition(root,"agent-1","COMPLETE"); err!=nil { t.Fatal(err) }
	if err:=Transition(root,"agent-1","WORKING"); err==nil { t.Fatal("COMPLETE -> WORKING must be rejected") }
}


func TestClaimRequiresRegisteredSession(t *testing.T) {
	root:=setup(t)
	err:=ClaimScope(root,Claim{SessionID:"ghost",Feature:"F",Scope:"ghost.txt",Token:"ghost-token"})
	if err==nil || !strings.Contains(err.Error(),"agent session not found") { t.Fatalf("err=%v",err) }
}

func TestHandoffRequiresRegisteredTarget(t *testing.T) {
	root:=setup(t)
	if err:=ClaimScope(root,Claim{SessionID:"agent-1",Feature:"F",Scope:"x.go",Token:"x"}); err!=nil { t.Fatal(err) }
	err:=Handoff(root,"agent-1","x","ghost")
	if err==nil || !strings.Contains(err.Error(),"handoff target session not found") { t.Fatalf("err=%v",err) }
}

func TestActiveAgentFieldsDoNotConflictWithClaimScopes(t *testing.T) {
	root:=setup(t)
	if err:=ClaimScope(root,Claim{SessionID:"agent-1",Feature:"F",Scope:"old",Token:"x"}); err!=nil { t.Fatalf("active-agent heartbeat must not be parsed as a claim scope: %v",err) }
}
