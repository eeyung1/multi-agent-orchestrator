package coordination

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

// Observation is internal diagnostic input, never a public JSON representation.
// Read one atomically published ledger; the operations reader brackets this with
// equal structured-state revisions. No locks, repair, or lifecycle writes occur.
type Observation struct {
	Present     bool                             `json:"-"`
	Assignments map[string]AssignmentObservation `json:"-"`
	Sensitive   []string                         `json:"-"`
}
type AssignmentObservation struct {
	State string
	Claim string // HELD, RELEASED, MISSING, MISMATCH, UNBOUND
}

func Observe(primary string, tasks map[string]state.Task) (Observation, error) {
	o := Observation{Assignments: map[string]AssignmentObservation{}}
	path := filepath.Join(primary, "multi-agent-orchestrator", "agentmeeting.md")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return o, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return o, errors.New("operational ledger must be a regular file")
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return o, nil
	}
	if err != nil {
		return o, err
	}
	defer f.Close()
	const limit = 16 << 20
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(data) > limit {
		return o, errors.New("cannot read bounded operational ledger")
	}
	if _, err := sessionClaimRows(data, ""); err != nil {
		return o, errors.New("malformed operational claims")
	}
	o.Present = true
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "|") && strings.Trim(trim, "| -:\t") != "" {
			cells := strings.Split(trim, "|")
			if len(cells) != 7 && len(cells) != 9 {
				return o, errors.New("malformed operational table row")
			}
		}
		p := strings.Split(strings.TrimSpace(line), "|")
		if len(p) != 9 || strings.TrimSpace(p[1]) == "Agent/session ID" || strings.Trim(strings.TrimSpace(p[1]), "-: ") == "" {
			continue
		}
		id, status := strings.TrimSpace(p[1]), strings.TrimSpace(p[3])
		if seen[id] || (status != "ACTIVE" && status != "WORKING" && status != "BLOCKED" && status != "COMPLETE" && status != "HANDOFF") {
			return o, errors.New("malformed operational session")
		}
		seen[id] = true
		o.Sensitive = append(o.Sensitive, strings.TrimSpace(p[7]))
	}
	for _, line := range claimLines(data) {
		p := strings.Split(strings.TrimSpace(line), "|")
		if len(p) == 7 && strings.TrimSpace(p[1]) != "Token" && strings.Trim(strings.TrimSpace(p[1]), "-: ") != "" {
			o.Sensitive = append(o.Sensitive, strings.TrimSpace(p[1]))
		}
	}
	for key, t := range tasks {
		a := AssignmentObservation{Claim: "UNBOUND"}
		if t.SessionID == "" {
			o.Assignments[key] = a
			continue
		}
		_, row, err := agentRow(data, t.SessionID)
		if err != nil {
			a.Claim = "MISSING"
			o.Assignments[key] = a
			continue
		}
		a.State = strings.TrimSpace(row[3])
		claims, err := sessionClaimRows(data, t.SessionID)
		if err != nil {
			return o, errors.New("malformed operational ownership")
		}
		if t.Status == "ASSIGNED" && t.ValidatedHead != "" && len(claims) == 0 && a.State == "COMPLETE" && verifyAssignmentRow(data, t, false) == nil {
			a.Claim = "RELEASED"
		} else if t.Status == "COMPLETE" && len(claims) == 0 && a.State == "COMPLETE" && strings.TrimSpace(row[5]) == t.Feature && strings.TrimSpace(row[6]) == t.Branch+" @ "+t.Worktree {
			a.Claim = "RELEASED"
		} else if t.Status == "BLOCKED" && a.State == "HANDOFF" && len(claims) == 0 && verifyAssignmentRow(data, t, false) == nil {
			a.Claim = "RELEASED" // operational recovery published before PENDING
		} else if verifyAssignmentRow(data, t, true) == nil {
			a.Claim = "HELD"
		} else {
			a.Claim = "MISMATCH"
		}
		o.Assignments[key] = a
	}
	return o, nil
}
