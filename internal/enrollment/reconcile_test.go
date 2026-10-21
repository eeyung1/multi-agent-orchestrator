package enrollment

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func legacyRepo(t *testing.T)string{t.Helper();d:=t.TempDir();c:=exec.Command("git","init","-b","main");c.Dir=d;if b,e:=c.CombinedOutput();e!=nil{t.Fatalf("%v %s",e,b)};os.WriteFile(filepath.Join(d,"agent_coordination.py"),[]byte("# legacy coordination"),0644);os.WriteFile(filepath.Join(d,"AGENTS.md"),[]byte("Use agent claims and scope coordination."),0644);return d}
func TestReconcileFlagsInstructionAsAmbiguous(t *testing.T){d:=legacyRepo(t);r,e:=Reconcile(d);if e!=nil{t.Fatal(e)};if len(r.Ambiguous)==0{t.Fatal("expected instruction ambiguity")}}
func TestMigrateRefusesAmbiguousAuthority(t *testing.T){d:=legacyRepo(t);if _,e:=Migrate(d,"user-approved",true);e==nil{t.Fatal("expected ambiguous migration refusal")}}
func TestMigrateHistoricalLedgerAfterInstructionFenced(t *testing.T){
	d:=legacyRepo(t);if e:=os.Remove(filepath.Join(d,"AGENTS.md"));e!=nil{t.Fatal(e)}
	dir:=filepath.Join(d,"multi-agent-orchestrator");os.MkdirAll(dir,0755)
	ledger:="## Atomic Claims\n| Token | Session | Feature | Scope | Status |\n|---|---|---|---|---|\n| old | s1 | docs | README.md | COMPLETE |\n";os.WriteFile(filepath.Join(dir,"agentmeeting.md"),[]byte(ledger),0644)
	r,e:=Migrate(d,"user-approved",true);if e!=nil{t.Fatal(e)};if r.Fingerprint==""{t.Fatal("missing fingerprint")}
	if e:=RequireEnrolled(d);e!=nil{t.Fatal(e)}
}
func TestMigrateRefusesActiveLegacyClaim(t *testing.T){
	d:=legacyRepo(t);os.Remove(filepath.Join(d,"AGENTS.md"));dir:=filepath.Join(d,"multi-agent-orchestrator");os.MkdirAll(dir,0755)
	os.WriteFile(filepath.Join(dir,"agentmeeting.md"),[]byte("## Atomic Claims\n| Token | Session | Feature | Scope | Status |\n|---|---|---|---|---|\n| old | s1 | f | backend | WORKING |\n"),0644)
	if _,e:=Migrate(d,"user-approved",true);e==nil{t.Fatal("expected active claim refusal")}
}
