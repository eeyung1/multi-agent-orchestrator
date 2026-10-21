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
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		found := false
		for i, line := range lines {
			if strings.HasPrefix(line, "| "+sessionID+" |") {
				parts := strings.Split(line, "|")
				if len(parts) < 9 {
					return fmt.Errorf("malformed agent row for %s", sessionID)
				}
				parts[4] = " " + time.Now().UTC().Format(time.RFC3339) + " "
				lines[i] = strings.Join(parts, "|")
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("agent session not found: %s", sessionID)
		}
		return atomicWrite(path, strings.Join(lines, "\n"))
	})
}

func ClaimScope(primary string, claim Claim) error {
	if strings.TrimSpace(claim.SessionID) == "" || strings.TrimSpace(claim.Feature) == "" || strings.TrimSpace(claim.Scope) == "" || strings.TrimSpace(claim.Token) == "" {
		return errors.New("session, feature, scope and token are required")
	}
	return withLock(primary, func() error {
		path := filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md")
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		state, err := sessionState(data, claim.SessionID)
		if err != nil {
			return err
		}
		if state == "COMPLETE" {
			return fmt.Errorf("completed session cannot acquire claims: %s", claim.SessionID)
		}
		for _, line := range claimLines(data) {
			if !strings.HasPrefix(line, "| ") {
				continue
			}
			parts := strings.Split(line, "|")
			if len(parts) != 7 {
				continue
			}
			existing := strings.TrimSpace(parts[4])
			if existing != "" && scopesOverlap(existing, claim.Scope) {
				return fmt.Errorf("scope conflicts with existing claim: %s", existing)
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
	return withLock(primary, func() error {
		path := filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md")
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines, err := transitionLines(data, sessionID, state)
		if err != nil {
			return err
		}
		if state == "COMPLETE" {
			claims, err := sessionClaimRows(data, sessionID)
			if err != nil {
				return err
			}
			if len(claims) > 0 {
				return fmt.Errorf("session has outstanding claims; use validation-backed completion")
			}
		}
		return atomicWrite(path, strings.Join(lines, "\n"))
	})
}

func withLock(primary string, fn func() error) error {
	lock := filepath.Join(primary, lockDir)
	if err := os.Mkdir(lock, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("coordination is locked: %s", lock)
		}
		return err
	}
	defer os.Remove(lock)
	return fn()
}

func atomicWrite(path, content string) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ReleaseClaim(primary, sessionID, token string) error {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(token) == "" {
		return errors.New("session and token are required")
	}
	return withLock(primary, func() error {
		path := filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md")
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		found := false
		for i, line := range lines {
			if strings.HasPrefix(line, "| "+token+" | "+sessionID+" |") {
				lines = append(lines[:i], lines[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("claim not found for session/token")
		}
		return atomicWrite(path, strings.Join(lines, "\n"))
	})
}

func Handoff(primary, sessionID, token, nextSession string) error {
	if strings.TrimSpace(nextSession) == "" {
		return errors.New("next session is required")
	}
	return withLock(primary, func() error {
		path := filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md")
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		state, err := sessionState(data, nextSession)
		if err != nil {
			return fmt.Errorf("handoff target session not found: %s: %w", nextSession, err)
		}
		if state == "COMPLETE" {
			return fmt.Errorf("cannot hand off a claim to completed session: %s", nextSession)
		}
		lines := strings.Split(string(data), "\n")
		found := false
		for i, line := range lines {
			if strings.HasPrefix(line, "| "+token+" | "+sessionID+" |") {
				parts := strings.Split(line, "|")
				if len(parts) < 7 {
					return fmt.Errorf("malformed claim row")
				}
				parts[2] = " " + nextSession + " "
				lines[i] = strings.Join(parts, "|")
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("claim not found for handoff")
		}
		return atomicWrite(path, strings.Join(lines, "\n"))
	})
}

func scopesOverlap(a, b string) bool {
	a = filepath.Clean(strings.TrimSpace(a))
	b = filepath.Clean(strings.TrimSpace(b))
	if a == "." || b == "." {
		return true
	}
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

func UpdateAssignment(primary, sessionID, feature, branchWorktree, claimRef string) error {
	return withLock(primary, func() error {
		path := filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md")
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		found := false
		for i, line := range lines {
			if strings.HasPrefix(line, "| "+sessionID+" |") {
				parts := strings.Split(line, "|")
				if len(parts) < 9 {
					return fmt.Errorf("malformed agent row for %s", sessionID)
				}
				parts[5] = " " + feature + " "
				parts[6] = " " + branchWorktree + " "
				parts[7] = " " + claimRef + " "
				lines[i] = strings.Join(parts, "|")
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("agent session not found: %s", sessionID)
		}
		return atomicWrite(path, strings.Join(lines, "\n"))
	})
}

func claimLines(data []byte) []string {
	text := string(data)
	start := strings.Index(text, "## Atomic Claims")
	if start < 0 {
		return nil
	}
	return strings.Split(text[start:], "\n")
}


func VerifyExecution(primary, sessionID, worktree string) error {
	return withLock(primary, func() error {
		path := filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md")
		data, err := os.ReadFile(path)
		if err != nil { return err }
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if !strings.HasPrefix(line, "| "+sessionID+" |") { continue }
			parts := strings.Split(line, "|")
			if len(parts) < 9 { return fmt.Errorf("malformed agent row for %s", sessionID) }
			if strings.TrimSpace(parts[3]) != "WORKING" { return fmt.Errorf("session is not WORKING: %s", strings.TrimSpace(parts[3])) }
			assignment := strings.TrimSpace(parts[6])
			if assignment == "" || !strings.Contains(assignment, " @ ") { return fmt.Errorf("session has no bound worktree assignment") }
			assigned := strings.TrimSpace(strings.SplitN(assignment, " @ ", 2)[1])
			got, err := filepath.Abs(worktree); if err != nil { return err }
			want, err := filepath.Abs(assigned); if err != nil { return err }
			if filepath.Clean(got) != filepath.Clean(want) { return fmt.Errorf("worktree does not match session assignment") }
			claims, err := sessionClaimRows(data, sessionID); if err != nil { return err }
			if len(claims) == 0 { return fmt.Errorf("session has no active claims") }
			return nil
		}
		return fmt.Errorf("agent session not found: %s", sessionID)
	})
}
