package agentregistry

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

var ErrPlanner = errors.New("Planner selection/request is stale, ineligible or unauthorized")

func nonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("Planner identity generation failed")
	}
	return hex.EncodeToString(b[:]), nil
}
func planningCap(id agentprotocol.Identity) bool {
	for _, c := range id.Capabilities {
		if c == agentprotocol.PlanningCapability {
			return true
		}
	}
	return false
}
func candidate(v View) bool {
	return v.Current && v.Live && v.Session.Status == Ready && v.Session.Identity.ProtocolVersion == agentprotocol.Version3 && supports(v.Session.Identity, agentprotocol.RolePlanner) && planningCap(v.Session.Identity) && (v.Session.AssignedRole == "" || v.Session.AssignedRole == agentprotocol.RolePlanner)
}

// PlannerCandidates is an authoritative snapshot, never a role grant. Selection
// rechecks eligibility in the same durable transaction as binding/role assignment.
func (r *Registry) PlannerCandidates() ([]View, error) {
	all, err := r.List()
	if err != nil {
		return nil, err
	}
	out := make([]View, 0)
	for _, v := range all {
		if candidate(v) {
			out = append(out, v)
		}
	}
	return out, nil
}

// SelectPlanner is trusted human selection, not reachable from agent messages.
// A nonempty expected binding ID is mandatory to replace an existing run. The
// compare-and-swap also serializes competing human selections across processes.
func (r *Registry) SelectPlanner(run string, ref Ref, expected string, authorized bool, decision string) (state.PlannerBinding, error) {
	if !authorized || ref.validate() != nil || agentprotocol.SessionID(run).Validate() != nil || state.RegistryDecision(decision) != nil {
		return state.PlannerBinding{}, ErrPlanner
	}
	id, err := nonce()
	if err != nil {
		return state.PlannerBinding{}, err
	}
	var result state.PlannerBinding
	err = r.transact("planner-selection", func(data *state.AgentRegistryState, now time.Time) (bool, error) {
		old, exists := data.Planners[run]
		if exists && expected != old.BindingID || !exists && expected != "" {
			return false, ErrPlanner
		}
		session, e := verify(data, ref, false)
		if e != nil || !candidate(view(data, session, now)) {
			return false, ErrPlanner
		}
		for key, b := range data.Planners {
			if b.Active && b.SessionID == ref.SessionID && key != run {
				return false, ErrPlanner
			}
		}
		if exists && old.Active {
			previous := data.Sessions[old.SessionID]
			if previous.Status == Busy {
				return false, ErrPlanner
			} // cancel the outstanding request explicitly first
			previous.AssignedRole = ""
			previous.RoleDecision = decision
			data.Sessions[old.SessionID] = previous
		}
		if exists {
			old.Active = false
			old.Reason = "human_replaced_planner"
			data.PlannerHistory = append(data.PlannerHistory, old)
		}
		session.AssignedRole = agentprotocol.RolePlanner
		session.RoleDecision = decision
		data.Sessions[ref.SessionID] = session
		result = state.PlannerBinding{RunID: run, BindingID: id, AgentID: ref.AgentID, SessionID: ref.SessionID, Generation: ref.Generation, Active: true, Decision: decision, Reason: "human_selected", SelectedAt: now}
		if data.Planners == nil {
			data.Planners = map[string]state.PlannerBinding{}
		}
		data.Planners[run] = result
		return true, nil
	})
	return result, err
}

func bound(data *state.AgentRegistryState, c agentprotocol.PlannerCorrelation) (state.PlannerBinding, error) {
	b, ok := data.Planners[c.RunID]
	if !ok || !b.Active || b.BindingID != c.BindingID {
		return state.PlannerBinding{}, ErrPlanner
	}
	session, err := verify(data, Ref{b.AgentID, b.SessionID, b.Generation}, false)
	if err != nil || session.AssignedRole != agentprotocol.RolePlanner || !supports(session.Identity, agentprotocol.RolePlanner) || !planningCap(session.Identity) {
		return state.PlannerBinding{}, ErrPlanner
	}
	return b, nil
}

func (r *Registry) BeginPlannerRequest(run, binding string) (agentprotocol.PlannerCorrelation, error) {
	request, err := nonce()
	if err != nil {
		return agentprotocol.PlannerCorrelation{}, err
	}
	c := agentprotocol.PlannerCorrelation{RunID: run, BindingID: binding, RequestID: request}
	err = r.transact("planner-request", func(data *state.AgentRegistryState, now time.Time) (bool, error) {
		b, e := bound(data, c)
		if e != nil {
			return false, e
		}
		session := data.Sessions[b.SessionID]
		if session.Status != Ready || b.RequestStatus == "PENDING" || b.AdoptedObjective != "" {
			return false, ErrPlanner
		}
		b.RequestID = request
		b.RequestStatus = "PENDING"
		b.ProposalDigest = ""
		data.Planners[run] = b
		session.Status = Busy
		session.StateChangedAt = now
		session.StateReason = "planner_request"
		data.Sessions[b.SessionID] = session
		return true, nil
	})
	return c, err
}

// AcceptPlannerResponse atomically consumes the one pending request. The ref is
// supplied by the attachment's private binding, never by response payload data.
func (r *Registry) AcceptPlannerResponse(ref Ref, response agentprotocol.PlanningResponse) error {
	if response.Validate() != nil {
		return ErrPlanner
	}
	return r.transact("planner-response", func(data *state.AgentRegistryState, now time.Time) (bool, error) {
		c := response.Correlation
		b, e := bound(data, c)
		if e != nil {
			return false, e
		}
		if (Ref{b.AgentID, b.SessionID, b.Generation}) != ref || b.RequestStatus != "PENDING" || b.RequestID != c.RequestID {
			return false, ErrPlanner
		}
		session := data.Sessions[b.SessionID]
		if session.Status != Busy {
			return false, ErrPlanner
		}
		b.RequestStatus = "COMPLETE"
		b.ProposalDigest = planning.PlanDigest(response.Proposal.Plan)
		if len(response.Proposal.Questions) > 0 {
			b.RequestStatus = "CLARIFICATION"
			b.ProposalDigest = ""
		}
		data.Planners[c.RunID] = b
		session.Status = Ready
		session.StateChangedAt = now
		session.StateReason = "planner_response"
		data.Sessions[b.SessionID] = session
		return true, nil
	})
}

func (r *Registry) CheckPlanner(ref Ref, c agentprotocol.PlannerCorrelation, complete bool) error {
	if ref.validate() != nil || c.Validate() != nil {
		return ErrPlanner
	}
	return r.transact("planner-check", func(data *state.AgentRegistryState, _ time.Time) (bool, error) {
		b, e := bound(data, c)
		if e != nil {
			return false, e
		}
		if (Ref{b.AgentID, b.SessionID, b.Generation}) != ref {
			return false, ErrPlanner
		}
		if b.RequestID != c.RequestID || complete && b.RequestStatus != "COMPLETE" && b.RequestStatus != "CLARIFICATION" || !complete && b.RequestStatus != "PENDING" {
			return false, ErrPlanner
		}
		return false, nil
	})
}

// StopPlanner preserves an auditable reason and invalidates all outstanding
// responses. Release is explicit; disconnected/superseded refs are not revived.
func (r *Registry) StopPlanner(run, binding, reason string) error {
	if state.RegistryDecision(reason) != nil {
		return ErrPlanner
	}
	return r.transact("planner-stop", func(data *state.AgentRegistryState, now time.Time) (bool, error) {
		b, ok := data.Planners[run]
		if !ok || b.BindingID != binding {
			return false, ErrPlanner
		}
		if !b.Active {
			return false, nil
		}
		b.Active = false
		b.Reason = reason
		if b.RequestStatus == "PENDING" {
			b.RequestStatus = "FAILED"
		}
		data.Planners[run] = b
		session, e := verify(data, Ref{b.AgentID, b.SessionID, b.Generation}, false)
		if e == nil {
			if session.Status == Busy {
				session.Status = Ready
				session.StateChangedAt = now
				session.StateReason = "planner_stopped"
			}
			session.AssignedRole = ""
			session.RoleDecision = reason
			data.Sessions[b.SessionID] = session
		}
		return true, nil
	})
}

// All registry mutations run this inside their existing shared state lock.
// Disconnect, expiry, supersession or role/capability withdrawal cannot leave
// an effective Planner behind. No replacement inherits the selection.
func invalidatePlanners(data *state.AgentRegistryState) bool {
	changed := false
	for key, b := range data.Planners {
		if !b.Active {
			continue
		}
		session, e := verify(data, Ref{b.AgentID, b.SessionID, b.Generation}, false)
		if e != nil || (session.Status != Ready && session.Status != Busy) || session.AssignedRole != agentprotocol.RolePlanner || !planningCap(session.Identity) || !supports(session.Identity, agentprotocol.RolePlanner) {
			b.Active = false
			b.Reason = "session_no_longer_eligible"
			if b.RequestStatus == "PENDING" {
				b.RequestStatus = "FAILED"
			}
			data.Planners[key] = b
			changed = true
		}
	}
	return changed
}
