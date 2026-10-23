package state

import (
	"errors"
	"fmt"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
)

type WorkerDispatch struct {
	LastMessage agentprotocol.MessageType       `json:"last_message,omitempty"`
	AgentID     agentprotocol.AgentID           `json:"agent_id"`
	SessionID   agentprotocol.SessionID         `json:"session_id"`
	Incarnation AgentSessionGeneration          `json:"incarnation"`
	Correlation agentprotocol.WorkerCorrelation `json:"correlation"`
	Phase       string                          `json:"phase"`            // RESERVED -> SENT -> ACCEPTED -> REPORTED -> FINISHED; failures -> FAILED
	Detail      string                          `json:"detail,omitempty"` // bounded latest progress/terminal evidence
}

// CheckWorkerSession is a pure check inside the caller's authoritative state
// transaction. It never refreshes liveness, issues identity, or changes ownership.
func (s Snapshot) CheckWorkerSession(d WorkerDispatch, now time.Time, status AgentStatus) error {
	r := s.AgentRegistry
	if r == nil || now.Before(r.ObservedAt) {
		return errors.New("worker registry unavailable or clock regression")
	}
	a, ok := r.Sessions[d.SessionID]
	if !ok || a.Identity.AgentID != d.AgentID || a.Generation != d.Incarnation || a.SupersededBy != "" {
		return errors.New("worker session identity fenced")
	}
	if a.Status != status {
		return fmt.Errorf("worker session state %s; requires %s", a.Status, status)
	}
	if now.After(a.LastSeenAt.Add(r.StaleAfterNS)) {
		return errors.New("worker heartbeat expired")
	}
	if a.AssignedRole != agentprotocol.RoleWorker || a.Identity.ProtocolVersion != agentprotocol.Version4 {
		return errors.New("worker role or protocol ineligible")
	}
	for _, other := range r.Sessions {
		if other.Identity.AgentID == d.AgentID && other.Generation > d.Incarnation {
			return errors.New("worker superseded")
		}
	}
	return nil
}
func (s Snapshot) validateWorkerDispatches() error {
	seen := map[string]bool{}
	busy := map[agentprotocol.SessionID]bool{}
	check := func(d *WorkerDispatch, objective, task string, generation uint64, active bool) error {
		if d == nil {
			return nil
		}
		if s.Authority.Schema < 4 || d.AgentID.Validate() != nil || d.SessionID.Validate() != nil || d.Incarnation == 0 || d.Correlation.Validate() != nil || d.Correlation.ObjectiveID != objective || d.Correlation.TaskID != task || d.Correlation.Generation != generation || len(d.Detail) > 8192 || s.AgentRegistry == nil {
			return errors.New("invalid worker dispatch identity")
		}
		a, ok := s.AgentRegistry.Sessions[d.SessionID]
		if !ok || a.Identity.AgentID != d.AgentID || a.Generation != d.Incarnation || seen[d.Correlation.DispatchID] {
			return errors.New("missing or duplicate worker dispatch enrollment")
		}
		seen[d.Correlation.DispatchID] = true
		switch d.Phase {
		case "RESERVED", "SENT", "ACCEPTED", "REPORTED", "FINISHED", "FAILED":
		default:
			return errors.New("invalid worker dispatch phase")
		}
		if active && d.Phase != "FAILED" && d.Phase != "FINISHED" {
			if busy[d.SessionID] {
				return errors.New("worker has multiple active assignments")
			}
			busy[d.SessionID] = true
		}
		return nil
	}
	for _, t := range s.Tasks {
		if t.WorkerDispatch != nil {
			d := t.WorkerDispatch
			if t.Status != "ASSIGNED" && t.Status != "BLOCKED" && t.Status != "COMPLETE" {
				return errors.New("worker dispatch without assigned task")
			}
			if t.Status == "COMPLETE" && d.Phase != "FINISHED" || t.Status == "BLOCKED" && d.Phase != "FAILED" || t.Status == "ASSIGNED" && (d.Phase == "FAILED" || d.Phase == "FINISHED") {
				return errors.New("worker dispatch phase conflicts with task status")
			}
		}
		if err := check(t.WorkerDispatch, t.ObjectiveID, t.ID, t.AssignmentGeneration, t.Status == "ASSIGNED"); err != nil {
			return err
		}
		for _, h := range t.AssignmentHistory {
			if err := check(h.WorkerDispatch, t.ObjectiveID, t.ID, h.AssignmentGeneration, false); err != nil {
				return err
			}
		}
	}

	if s.AgentRegistry != nil && s.AgentRegistry.WorkerDispatchCount != uint64(len(seen)) {
		return errors.New("worker dispatch history is incomplete")
	}
	return nil
}

// CheckWorkerResult adds enrollment fencing to the existing exact task/claim/
// scope/HEAD review gates, including the final atomic result collection.
func (s Snapshot) CheckWorkerResult(t Task, now time.Time) error {
	if t.WorkerDispatch == nil {
		return nil
	}
	if t.WorkerDispatch.Phase != "REPORTED" || t.WorkerDispatch.LastMessage != agentprotocol.WorkerResult {
		return errors.New("worker has not relinquished assignment")
	}
	// Durable Go validation is a checkpoint for metadata-only crash recovery.
	// It cannot be minted by any worker message. Exact HEAD/evidence gates in
	// review still apply; no worker is relaunched or replacement session touched.
	if t.ValidatedHead != "" && t.ValidationEvidence != "" {
		return nil
	}
	return s.CheckWorkerSession(*t.WorkerDispatch, now, AgentBusy)
}
