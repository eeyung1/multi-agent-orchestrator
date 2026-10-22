package state

import (
	"encoding/hex"
	"errors"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
)

// PlannerBinding is one human-selected planning run, never task ownership.
// Closed bindings remain audit evidence. Request IDs are Go-issued and single use.
type PlannerBinding struct {
	RunID            string                  `json:"run_id"`
	BindingID        string                  `json:"binding_id"`
	AgentID          agentprotocol.AgentID   `json:"agent_id"`
	SessionID        agentprotocol.SessionID `json:"session_id"`
	Generation       AgentSessionGeneration  `json:"session_generation"`
	Active           bool                    `json:"active"`
	Decision         string                  `json:"decision"`
	Reason           string                  `json:"reason"`
	SelectedAt       time.Time               `json:"selected_at"`
	RequestID        string                  `json:"request_id,omitempty"`
	RequestStatus    string                  `json:"request_status,omitempty"`
	ProposalDigest   string                  `json:"proposal_digest,omitempty"`
	AdoptedObjective string                  `json:"adopted_objective,omitempty"`
}

type PlannerEvidence struct {
	Correlation    agentprotocol.PlannerCorrelation `json:"correlation"`
	ProposalDigest string                           `json:"proposal_digest"`
}

func (r *AgentRegistryState) validatePlanners() error {
	bindings := make([]PlannerBinding, 0, len(r.Planners)+len(r.PlannerHistory))
	for key, b := range r.Planners {
		if key != b.RunID {
			return errors.New("mismatched Planner run")
		}
		bindings = append(bindings, b)
	}
	for _, b := range r.PlannerHistory {
		if b.Active {
			return errors.New("active historical Planner")
		}
		bindings = append(bindings, b)
	}
	seen := map[string]bool{}
	activeSessions := map[agentprotocol.SessionID]bool{}
	for _, b := range bindings {
		if agentprotocol.SessionID(b.RunID).Validate() != nil || agentprotocol.SessionID(b.BindingID).Validate() != nil || RegistryDecision(b.Decision) != nil || RegistryDecision(b.Reason) != nil || b.SelectedAt.IsZero() || b.SelectedAt.After(r.ObservedAt) {
			return errors.New("malformed Planner binding")
		}
		if seen[b.BindingID] {
			return errors.New("duplicate Planner binding identity")
		}
		seen[b.BindingID] = true
		if b.Active {
			if activeSessions[b.SessionID] {
				return errors.New("session selected for multiple active planning runs")
			}
			activeSessions[b.SessionID] = true
		}
		session, ok := r.Sessions[b.SessionID]
		if !ok || session.Identity.AgentID != b.AgentID || session.Generation != b.Generation {
			return errors.New("Planner binding lacks enrollment")
		}
		if b.Active && ((session.Status != AgentReady && session.Status != AgentBusy) || session.SupersededBy != "" || session.AssignedRole != agentprotocol.RolePlanner || session.Identity.ProtocolVersion != agentprotocol.Version3) {
			return errors.New("active Planner binding is fenced")
		}
		switch b.RequestStatus {
		case "":
			if b.RequestID != "" || b.ProposalDigest != "" {
				return errors.New("malformed empty Planner request")
			}
		case "PENDING", "FAILED", "COMPLETE", "CLARIFICATION":
			if agentprotocol.SessionID(b.RequestID).Validate() != nil {
				return errors.New("invalid Planner request identity")
			}
			if (b.RequestStatus == "COMPLETE") != (b.ProposalDigest != "") {
				return errors.New("invalid Planner response evidence")
			}
			if b.ProposalDigest != "" {
				raw, err := hex.DecodeString(b.ProposalDigest)
				if err != nil || len(raw) != 32 {
					return errors.New("invalid Planner proposal digest")
				}
			}
		default:
			return errors.New("unknown Planner request status")
		}
		if b.Active && b.RequestStatus == "PENDING" && session.Status != AgentBusy {
			return errors.New("pending Planner request requires BUSY session")
		}
		if !b.Active && b.RequestStatus == "PENDING" {
			return errors.New("inactive Planner has pending request")
		}
		if b.AdoptedObjective != "" && b.RequestStatus != "COMPLETE" {
			return errors.New("invalid Planner adoption")
		}
	}
	return nil
}

func (s Snapshot) validatePlannerObjectives() error {
	for id, o := range s.Objectives {
		e := o.PlannerEvidence
		if e == nil {
			continue
		}
		if s.Authority.Schema != SchemaVersion || s.AgentRegistry == nil || e.Correlation.Validate() != nil {
			return errors.New("invalid objective Planner evidence")
		}
		bindings := append([]PlannerBinding(nil), s.AgentRegistry.PlannerHistory...)
		if b, ok := s.AgentRegistry.Planners[e.Correlation.RunID]; ok {
			bindings = append(bindings, b)
		}
		found := false
		for _, b := range bindings {
			if b.RunID == e.Correlation.RunID && b.BindingID == e.Correlation.BindingID && b.RequestID == e.Correlation.RequestID && b.RequestStatus == "COMPLETE" && b.ProposalDigest == e.ProposalDigest && b.AdoptedObjective == id {
				found = true
				break
			}
		}
		if !found {
			return errors.New("objective Planner evidence lacks recorded adoption")
		}
	}
	return nil
}

// CheckPlannerEvidence runs under the scheduler's state transaction at human
// adoption. A response never authorizes a plan on its own. No stale result can
// cross this boundary even if disconnect/replacement raced the approval prompt.
func (s Snapshot) CheckPlannerEvidence(e PlannerEvidence, plan planning.Plan, now time.Time) error {
	if e.Correlation.Validate() != nil || s.AgentRegistry == nil {
		return errors.New("missing Planner evidence")
	}
	r := s.AgentRegistry
	b, ok := r.Planners[e.Correlation.RunID]
	if !ok || !b.Active || b.BindingID != e.Correlation.BindingID || b.RequestID != e.Correlation.RequestID || b.RequestStatus != "COMPLETE" || b.AdoptedObjective != "" || b.ProposalDigest != e.ProposalDigest || planning.PlanDigest(plan) != e.ProposalDigest {
		return errors.New("Planner evidence is stale, replayed or mismatched")
	}
	session := r.Sessions[b.SessionID]
	if session.Identity.AgentID != b.AgentID || session.Generation != b.Generation || session.SupersededBy != "" || session.Status != AgentReady || session.AssignedRole != agentprotocol.RolePlanner || now.Before(r.ObservedAt) || now.After(session.LastSeenAt.Add(r.StaleAfterNS)) {
		return errors.New("selected Planner is no longer live/current")
	}
	return nil
}
