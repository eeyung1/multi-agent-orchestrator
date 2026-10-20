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
		if strings.Contains(string(data), "| "+claim.Scope+" |") {
			return fmt.Errorf("scope already claimed: %s", claim.Scope)
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
