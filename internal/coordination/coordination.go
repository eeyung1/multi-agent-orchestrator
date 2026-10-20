package coordination

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const lockDir = ".agent-coordination-lock"

type Claim struct {
	SessionID string
	Feature   string
	Scope     string
	Token     string
}

func Heartbeat(primary, sessionID string) error {
	return withLock(primary, func() error {
		path := filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md")
		data, err := os.ReadFile(path)
		if err != nil { return err }
		lines := strings.Split(string(data), "\n")
		found := false
		for i, line := range lines {
			if strings.HasPrefix(line, "| "+sessionID+" |") {
				parts := strings.Split(line, "|")
				if len(parts) < 9 { return fmt.Errorf("malformed agent row for %s", sessionID) }
				parts[4] = " " + time.Now().UTC().Format(time.RFC3339) + " "
				lines[i] = strings.Join(parts, "|")
				found = true
				break
			}
		}
		if !found { return fmt.Errorf("agent session not found: %s", sessionID) }
		return atomicWrite(path, strings.Join(lines, "\n"))
	})
}

func ClaimScope(primary string, claim Claim) error {
	if strings.TrimSpace(claim.SessionID)=="" || strings.TrimSpace(claim.Feature)=="" || strings.TrimSpace(claim.Scope)=="" || strings.TrimSpace(claim.Token)=="" {
		return errors.New("session, feature, scope and token are required")
	}
	return withLock(primary, func() error {
		path := filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md")
		data, err := os.ReadFile(path)
		if err != nil { return err }
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "| ") { continue }
			parts:=strings.Split(line,"|")
			if len(parts)<7 { continue }
			existing:=strings.TrimSpace(parts[4])
			if existing!="" && scopesOverlap(existing,claim.Scope) {
				return fmt.Errorf("scope conflicts with existing claim: %s",existing)
			}
		}
		entry := fmt.Sprintf("| %s | %s | %s | %s | %s |\n", claim.Token, claim.SessionID, claim.Feature, claim.Scope, time.Now().UTC().Format(time.RFC3339))
		text := string(data)
		if !strings.Contains(text, "## Atomic Claims") {
			text += "\n## Atomic Claims\n\n| Token | Agent/session ID | Feature | Scope | Claimed UTC |\n| --- | --- | --- | --- | --- |\n"
		}
		return atomicWrite(path, text+entry)
	})
}

func Transition(primary, sessionID, state string) error {
	allowed := map[string]bool{"ACTIVE":true,"WORKING":true,"BLOCKED":true,"HANDOFF":true,"COMPLETE":true}
	if !allowed[state] { return fmt.Errorf("invalid lifecycle state: %s", state) }
	return withLock(primary, func() error {
		path := filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md")
		data, err := os.ReadFile(path)
		if err != nil { return err }
		lines := strings.Split(string(data), "\n")
		found := false
		for i,line := range lines {
			if strings.HasPrefix(line,"| "+sessionID+" |") {
				parts:=strings.Split(line,"|")
				if len(parts)<9 { return fmt.Errorf("malformed agent row for %s",sessionID) }
				parts[3]=" "+state+" "
				parts[4]=" "+time.Now().UTC().Format(time.RFC3339)+" "
				lines[i]=strings.Join(parts,"|"); found=true; break
			}
		}
		if !found { return fmt.Errorf("agent session not found: %s",sessionID) }
		return atomicWrite(path,strings.Join(lines,"\n"))
	})
}

func withLock(primary string, fn func() error) error {
	lock:=filepath.Join(primary,lockDir)
	if err:=os.Mkdir(lock,0o700); err!=nil {
		if errors.Is(err,os.ErrExist) { return fmt.Errorf("coordination is locked: %s",lock) }
		return err
	}
	defer os.Remove(lock)
	return fn()
}

func atomicWrite(path, content string) error {
	tmp:=path+".tmp"
	if err:=os.WriteFile(tmp,[]byte(content),0o644); err!=nil { return err }
	return os.Rename(tmp,path)
}


func ReleaseClaim(primary, sessionID, token string) error {
	if strings.TrimSpace(sessionID)=="" || strings.TrimSpace(token)=="" { return errors.New("session and token are required") }
	return withLock(primary,func() error {
		path:=filepath.Join(primary,"multi-agent-orchestrator","agentmeeting.md")
		data,err:=os.ReadFile(path); if err!=nil { return err }
		lines:=strings.Split(string(data),"\n"); found:=false
		for i,line:=range lines {
			if strings.HasPrefix(line,"| "+token+" | "+sessionID+" |") {
				lines=append(lines[:i],lines[i+1:]...); found=true; break
			}
		}
		if !found { return fmt.Errorf("claim not found for session/token") }
		return atomicWrite(path,strings.Join(lines,"\n"))
	})
}

func Handoff(primary, sessionID, token, nextSession string) error {
	if strings.TrimSpace(nextSession)=="" { return errors.New("next session is required") }
	return withLock(primary,func() error {
		path:=filepath.Join(primary,"multi-agent-orchestrator","agentmeeting.md")
		data,err:=os.ReadFile(path); if err!=nil { return err }
		lines:=strings.Split(string(data),"\n"); found:=false
		for i,line:=range lines {
			if strings.HasPrefix(line,"| "+token+" | "+sessionID+" |") {
				parts:=strings.Split(line,"|")
				if len(parts)<7 { return fmt.Errorf("malformed claim row") }
				parts[2]=" "+nextSession+" "; lines[i]=strings.Join(parts,"|"); found=true; break
			}
		}
		if !found { return fmt.Errorf("claim not found for handoff") }
		return atomicWrite(path,strings.Join(lines,"\n"))
	})
}

func scopesOverlap(a,b string) bool {
	a=filepath.Clean(strings.TrimSpace(a)); b=filepath.Clean(strings.TrimSpace(b))
	if a=="." || b=="." { return true }
	return a==b || strings.HasPrefix(a,b+string(filepath.Separator)) || strings.HasPrefix(b,a+string(filepath.Separator))
}
