package coordination

import (
	"os"
	"path/filepath"
	"strings"
)

func SessionStatus(primary, sessionID string) (string, error) {
	var status string
	err := withLock(primary, func() error {
		data, err := os.ReadFile(filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md"))
		if err != nil {
			return err
		}
		status, err = sessionState(data, sessionID)
		return err
	})
	return status, err
}

func Claims(primary string) ([]Claim, error) {
	var claims []Claim
	err := withLock(primary, func() error {
		data, err := os.ReadFile(filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md"))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := sessionClaimRows(data, ""); err != nil {
			return err
		}
		for _, line := range claimLines(data) {
			parts := strings.Split(strings.TrimSpace(line), "|")
			if len(parts) != 7 || strings.TrimSpace(parts[1]) == "Token" || strings.Trim(strings.TrimSpace(parts[1]), "-:") == "" {
				continue
			}
			claims = append(claims, Claim{Token: strings.TrimSpace(parts[1]), SessionID: strings.TrimSpace(parts[2]), Feature: strings.TrimSpace(parts[3]), Scope: strings.TrimSpace(parts[4])})
		}
		return nil
	})
	return claims, err
}
