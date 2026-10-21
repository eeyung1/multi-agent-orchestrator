package coordination

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CompleteSession records successful validation evidence and publishes COMPLETE
// together with removal of all claims owned by sessionID in one ledger rename.
// token must identify an existing claim owned by that session; it does not limit
// cleanup to that claim. recordEvidence runs under the coordination lock and must
// not call other coordination operations. If ledger publication fails after the
// callback succeeds, evidence may remain, but the live state/claims are unchanged.
func CompleteSession(primary, sessionID, token string, recordEvidence func() error) error {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(token) == "" {
		return errors.New("session and token are required")
	}
	if recordEvidence == nil {
		return errors.New("completion evidence recorder is required")
	}
	return withLock(primary, func() error {
		path := filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md")
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines, err := transitionLines(data, sessionID, "COMPLETE")
		if err != nil {
			return err
		}
		claims, err := sessionClaimRows(data, sessionID)
		if err != nil {
			return err
		}
		ownsToken := false
		for _, claimedToken := range claims {
			if claimedToken == token {
				ownsToken = true
			}
		}
		if !ownsToken {
			return fmt.Errorf("claim not found for session/token")
		}

		remaining := make([]string, 0, len(lines)-len(claims))
		for i, line := range lines {
			if _, owned := claims[i]; !owned {
				remaining = append(remaining, line)
			}
		}
		if err := recordEvidence(); err != nil {
			return fmt.Errorf("record completion evidence: %w", err)
		}
		if err := atomicWrite(path, strings.Join(remaining, "\n")); err != nil {
			return fmt.Errorf("publish completion and release session claims: %w", err)
		}
		return nil
	})
}

func agentRow(data []byte, sessionID string) (int, []string, error) {
	// Activation appends registrations to the ledger, potentially after the
	// claims table. Distinguish nine-cell session rows from seven-cell claims.
	for i, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) == 9 && strings.TrimSpace(parts[1]) == sessionID {
			return i, parts, nil
		}
	}
	return 0, nil, fmt.Errorf("agent session not found: %s", sessionID)
}

func sessionState(data []byte, sessionID string) (string, error) {
	_, parts, err := agentRow(data, sessionID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(parts[3]), nil
}

func transitionLines(data []byte, sessionID, state string) ([]string, error) {
	allowed := map[string]map[string]bool{
		"ACTIVE":   {"WORKING": true, "BLOCKED": true, "HANDOFF": true},
		"WORKING":  {"BLOCKED": true, "HANDOFF": true, "COMPLETE": true},
		"BLOCKED":  {"WORKING": true, "HANDOFF": true},
		"HANDOFF":  {"ACTIVE": true, "WORKING": true, "BLOCKED": true},
		"COMPLETE": {},
	}
	i, parts, err := agentRow(data, sessionID)
	if err != nil {
		return nil, err
	}
	current := strings.TrimSpace(parts[3])
	if !allowed[current][state] {
		return nil, fmt.Errorf("invalid lifecycle transition: %s -> %s", current, state)
	}
	parts[3] = " " + state + " "
	parts[4] = " " + time.Now().UTC().Format(time.RFC3339) + " "
	lines := strings.Split(string(data), "\n")
	lines[i] = strings.Join(parts, "|")
	return lines, nil
}

// sessionClaimRows follows the claim region from Atomic Claims to EOF, matching
// ClaimScope's append convention even across later headings. It matches the full
// session field, not a token or ID prefix, and rejects malformed claim rows before
// recording success rather than risk completing with an unparsed claim.
func sessionClaimRows(data []byte, sessionID string) (map[int]string, error) {
	rows := make(map[int]string)
	active := false
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if trimmed == "## Atomic Claims" {
				active = true
			}
			continue
		}
		if !active || !strings.HasPrefix(trimmed, "|") {
			continue
		}
		if trimmed == "| Token | Agent/session ID | Feature | Scope | Claimed UTC |" || strings.Trim(trimmed, "| -:\t") == "" {
			continue
		}
		parts := strings.Split(trimmed, "|")
		// Preserve registrations appended by activation after the claims table.
		if len(parts) == 9 {
			switch strings.TrimSpace(parts[3]) {
			case "ACTIVE", "WORKING", "BLOCKED", "HANDOFF", "COMPLETE":
				continue
			}
		}
		if len(parts) != 7 || strings.TrimSpace(parts[0]) != "" || strings.TrimSpace(parts[6]) != "" {
			return nil, fmt.Errorf("malformed claim row at line %d", i+1)
		}
		for _, field := range parts[1:6] {
			if strings.TrimSpace(field) == "" {
				return nil, fmt.Errorf("malformed claim row at line %d", i+1)
			}
		}
		if strings.TrimSpace(parts[2]) == sessionID {
			rows[i] = strings.TrimSpace(parts[1])
		}
	}
	return rows, nil
}
