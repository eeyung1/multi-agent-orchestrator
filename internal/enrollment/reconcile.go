package enrollment

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

type LegacyClaim struct { Token,SessionID,Feature,Scope,Status,Source string }
type Reconciliation struct { RepositoryID string `json:"repository_id"`; Findings []Finding `json:"findings"`; Claims []LegacyClaim `json:"claims"`; Ambiguous []string `json:"ambiguous"`; Fingerprint string `json:"fingerprint"` }

func Reconcile(workspace string)(Reconciliation,error){
	report,err:=Preflight(workspace);if err!=nil{return Reconciliation{},err};primary,err:=repository.Primary(workspace);if err!=nil{return Reconciliation{},err}
	r:=Reconciliation{RepositoryID:report.RepositoryID,Findings:report.Findings}
	for _,f:=range report.Findings {
		if f.Kind!="coordination-tool"{continue}
		path:=filepath.Join(primary,filepath.FromSlash(f.Path));claims,ok,detail:=inspectKnownPythonLedger(primary,path)
		if !ok{r.Ambiguous=append(r.Ambiguous,fmt.Sprintf("%s: %s",f.Path,detail));continue}
		r.Claims=append(r.Claims,claims...)
	}
	for _,f:=range report.Findings{if f.Kind=="instruction"{r.Ambiguous=append(r.Ambiguous,fmt.Sprintf("%s: coordination instruction requires explicit fencing/update",f.Path))}}
	sort.Strings(r.Ambiguous);sort.Slice(r.Claims,func(i,j int)bool{return r.Claims[i].Token<r.Claims[j].Token})
	r.Fingerprint=fingerprint(report,r.Claims);return r,nil
}

func Migrate(workspace,provenance string,acknowledgeHistorical bool)(Reconciliation,error){
	if strings.TrimSpace(provenance)==""{return Reconciliation{},errors.New("migration provenance is required")}
	r,err:=Reconcile(workspace);if err!=nil{return r,err}
	if len(r.Ambiguous)>0{return r,fmt.Errorf("legacy authority remains ambiguous: %s",strings.Join(r.Ambiguous,"; "))}
	if len(r.Claims)>0&&!acknowledgeHistorical{return r,errors.New("legacy claims require explicit historical/ownership acknowledgement")}
	primary,err:=repository.Primary(workspace);if err!=nil{return r,err}
	if err:=bootstrapMigratedState(primary,r.RepositoryID,provenance,r.Fingerprint,r.Claims);err!=nil{return r,err}
	return r,nil
}

func bootstrapMigratedState(primary,repositoryID,provenance,fingerprint string,claims []LegacyClaim)error{
	dir:=filepath.Join(primary,"multi-agent-orchestrator");if err:=os.MkdirAll(dir,0755);err!=nil{return err}
	path:=filepath.Join(dir,"state.json");if _,err:=os.Stat(path);err==nil{return errors.New("structured state already exists; use enrolled-state reconciliation instead")}
	now:=time.Now().UTC().Format(time.RFC3339)
	s:=state.Snapshot{Authority:state.Authority{RepositoryID:repositoryID,Protocol:state.ProtocolVersion,Schema:state.SchemaVersion,Generation:1,Enrollment:"MIGRATED",Provenance:provenance+"; legacy-fingerprint="+fingerprint,EnrolledUTC:now},Sessions:map[string]state.Session{},Claims:map[string]state.Claim{},Objectives:map[string]state.Objective{},Tasks:map[string]state.Task{},Results:map[string]state.ResultRecord{},Reviews:map[string]state.Review{},Events:[]state.Event{}}
	for _,c:=range claims{if strings.EqualFold(c.Status,"ACTIVE")||strings.EqualFold(c.Status,"WORKING"){return fmt.Errorf("active legacy claim %s cannot be auto-migrated; quiesce or hand off explicitly",c.Token)}}
	data,err:=json.MarshalIndent(s,"","  ");if err!=nil{return err};data=append(data,'\\n');return atomicCreate(path,data)
}

func inspectKnownPythonLedger(primary,script string)([]LegacyClaim,bool,string){
	name:=filepath.Base(script);if name!="agent_coordination.py"&&name!="agent-coordination.py"{return nil,false,"unsupported coordination writer"}
	ledger:=filepath.Join(primary,"multi-agent-orchestrator","agentmeeting.md");f,err:=os.Open(ledger);if errors.Is(err,os.ErrNotExist){return nil,true,""};if err!=nil{return nil,false,err.Error()};defer f.Close()
	var claims []LegacyClaim;s:=bufio.NewScanner(f);inClaims:=false
	for s.Scan(){line:=strings.TrimSpace(s.Text());if strings.HasPrefix(line,"## "){inClaims=strings.Contains(strings.ToLower(line),"claim");continue};if !inClaims||!strings.HasPrefix(line,"|"){continue};cells:=tableCells(line);if len(cells)<5||strings.EqualFold(cells[0],"token")||strings.Trim(cells[0],"-: ")==""{continue};claims=append(claims,LegacyClaim{Token:cells[0],SessionID:cells[1],Feature:cells[2],Scope:cells[3],Status:cells[4],Source:filepath.Base(script)})}
	if err:=s.Err();err!=nil{return nil,false,err.Error()};return claims,true,""
}
func tableCells(line string)[]string{raw:=strings.Split(strings.TrimSpace(line),"|");var out []string;for _,v:=range raw[1:len(raw)-1]{out=append(out,strings.TrimSpace(v))};return out}
func fingerprint(r Report,c []LegacyClaim)string{parts:=[]string{r.RepositoryID};for _,f:=range r.Findings{parts=append(parts,f.Path+":"+f.Kind)};for _,x:=range c{parts=append(parts,x.Token+":"+x.SessionID+":"+x.Scope+":"+x.Status)};sort.Strings(parts);return fmt.Sprintf("%x",sha256sum(strings.Join(parts,"\n")))}

func atomicCreate(path string,data []byte)error{
	f,err:=os.OpenFile(path,os.O_WRONLY|os.O_CREATE|os.O_EXCL,0600);if err!=nil{return err}
	if _,err=f.Write(data);err!=nil{f.Close();os.Remove(path);return err};if err=f.Sync();err!=nil{f.Close();os.Remove(path);return err};return f.Close()
}
