package operations

import (
	"errors"
	"sort"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

func project(workspace, repositoryID string, s *state.Snapshot, ledger coordination.Observation, opt Options) (Snapshot, error) {
	now := opt.Now().UTC()
	if now.IsZero() {
		return Snapshot{}, errors.New("invalid observation clock")
	}
	v := Snapshot{Version: Version, ObservedAt: now, Workspace: workspace, RepositoryID: repositoryID,
		MaxWorkers: opt.MaxWorkers, ConfigurationFrom: opt.ConfigurationSource,
		ActiveObjectives: []string{}, Objectives: []Objective{}, Agents: []Agent{}, Planners: []Planner{}, PlannerHistory: []Planner{}, Tasks: []Task{}, Attention: []Attention{}, Audit: []Audit{},
		Limitations: []string{"Projection only: reported state is not permission to execute or recover.", "Local paths and untrusted agent summaries are exposed; raw validation output and claim credentials are omitted.", "Registry liveness is not proof of a transport connection or a stopped external writer.", "Worktree paths and validated HEADs are persisted metadata; ordinary status does not inspect current Git/filesystem output."}}
	if v.ConfigurationFrom == "" {
		v.ConfigurationFrom = "not_available"
	}
	if opt.MaxWorkers != nil {
		n := *opt.MaxWorkers
		v.MaxWorkers = &n
	}
	if opt.Connected != nil {
		n := 0
		v.System.AvailableWorkers = &n
	}
	if s == nil {
		v.Attention = append(v.Attention, Attention{Code: "PROJECT_NOT_INITIALIZED", Severity: "info", Message: "No authoritative state exists.", Action: "Use the existing explicit repository enrollment and Planner approval flow."})
		return redact(v, state.Snapshot{}, ledger.Sensitive)
	}
	v.Initialized, v.StateSchema, v.StateProtocol, v.Revision, v.Enrollment = true, s.Authority.Schema, s.Authority.Protocol, s.Authority.Generation, s.Authority.Enrollment
	if s.Authority.Generation == 0 {
		return Snapshot{}, ErrAuthority
	}
	if s.Authority.Enrollment != "NATIVE" && s.Authority.Enrollment != "COMPATIBLE_ADAPTER" && s.Authority.Enrollment != "MIGRATED" {
		v.Attention = append(v.Attention, Attention{Code: "REPOSITORY_ENROLLMENT_REQUIRED", Severity: "action", Message: "Repository authority is not enrolled for execution.", Action: "Use the existing explicit repository enrollment/reconciliation flow."})
	}
	if opt.MaxWorkers == nil {
		v.Limitations = append(v.Limitations, "Concurrency policy is not persisted in schema 4; provide --config or inspect from the owning host console.")
	}
	for key, r := range s.Reviews {
		if r.ObjectiveID != key || s.Objectives[key].ID == "" || (r.Status != "AWAITING_REVIEW" && r.Status != "APPROVED" && r.Status != "CHANGES_REQUESTED") {
			return Snapshot{}, ErrAuthority
		}
	}
	for key, o := range s.Objectives {
		if o.ID != key || key == "" {
			return Snapshot{}, ErrAuthority
		}
		view := Objective{ID: o.ID, Description: o.Description, Status: o.Status, Authorized: o.AuthorizedBy != "", ReviewStatus: s.Reviews[key].Status, PlannerProvider: o.PlannerProvider}
		v.Objectives = append(v.Objectives, view)
		if o.Status == "ACTIVE" {
			v.ActiveObjectives = append(v.ActiveObjectives, key)
		}
		if !view.Authorized {
			v.Attention = append(v.Attention, Attention{Code: "OBJECTIVE_AUTHORIZATION_REQUIRED", Severity: "action", ObjectiveID: key, Message: "Objective has no recorded human authorization.", Action: "Use the Planner's existing plan approval flow."})
		}
		if view.ReviewStatus == "AWAITING_REVIEW" {
			v.Attention = append(v.Attention, Attention{Code: "REVIEW_REQUIRED", Severity: "action", ObjectiveID: key, Message: "Go-collected results await human review.", Action: "Inspect branch heads/evidence and use the existing human review decision command."})
		}
	}
	agents := map[agentprotocol.SessionID]int{}
	if r := s.AgentRegistry; r != nil {
		latest := map[agentprotocol.AgentID]state.AgentSessionGeneration{}
		for _, a := range r.Sessions {
			if a.Generation > latest[a.Identity.AgentID] {
				latest[a.Identity.AgentID] = a.Generation
			}
		}
		clockOK := !now.Before(r.ObservedAt)
		if !clockOK {
			v.Attention = append(v.Attention, Attention{Code: "CLOCK_REGRESSION", Severity: "action", Message: "Observation clock precedes authoritative registry observations; liveness is unknown.", Action: "Reconcile the local clock; do not infer eligibility."})
		}
		for _, a := range r.Sessions {
			id := a.Identity
			current := latest[id.AgentID] == a.Generation
			stale := clockOK && current && a.Status != state.AgentDisconnected && now.After(a.LastSeenAt.Add(r.StaleAfterNS))
			live := clockOK && current && a.Status != state.AgentDisconnected && !stale
			connection := "UNKNOWN"
			if opt.Connected != nil {
				connection = "OBSERVED_ABSENT"
				if opt.Connected(state.AgentRef{AgentID: id.AgentID, SessionID: id.SessionID, Generation: a.Generation}) {
					connection = "OBSERVED_CONNECTED"
				}
			}
			p := Agent{AgentID: id.AgentID, SessionID: id.SessionID, Incarnation: a.Generation, Protocol: id.ProtocolVersion, Metadata: id.Metadata,
				Capabilities: append([]agentprotocol.Capability{}, id.Capabilities...), SupportedRoles: append([]agentprotocol.Role{}, id.SupportedRoles...), AssignedRole: a.AssignedRole, Status: a.Status, StateReason: a.StateReason, RoleDecision: a.RoleDecision,
				Current: current, Live: live, Stale: stale, LastHeartbeat: a.LastSeenAt, SupersededBy: a.SupersededBy, Connection: connection, Assignments: []Assignment{}, History: []History{}}
			sort.Slice(p.Capabilities, func(i, j int) bool { return p.Capabilities[i] < p.Capabilities[j] })
			sort.Slice(p.SupportedRoles, func(i, j int) bool { return p.SupportedRoles[i] < p.SupportedRoles[j] })
			p.PlannerCandidate = live && a.Status == state.AgentReady && (id.ProtocolVersion == agentprotocol.Version3 || id.ProtocolVersion == agentprotocol.Version4) && hasRole(id.SupportedRoles, agentprotocol.RolePlanner) && hasCap(id.Capabilities, "planning") && (a.AssignedRole == "" || a.AssignedRole == agentprotocol.RolePlanner)
			p.WorkerCandidate = live && a.Status == state.AgentReady && id.ProtocolVersion == agentprotocol.Version4 && a.AssignedRole == agentprotocol.RoleWorker && hasRole(id.SupportedRoles, agentprotocol.RoleWorker)
			if p.WorkerCandidate {
				v.System.ReadyWorkers++
				if connection == "OBSERVED_CONNECTED" {
					*v.System.AvailableWorkers++
				}
			}
			if stale {
				v.Attention = append(v.Attention, Attention{Code: "AGENT_STALE", Severity: "action", SessionID: string(id.SessionID), Message: "Heartbeat expired; persisted lifecycle is unchanged by this read.", Action: "Inspect the owning listener/controller and reconcile the exact session."})
			}
			if current && live && connection == "OBSERVED_ABSENT" {
				v.Attention = append(v.Attention, Attention{Code: "LOCAL_ROUTE_NOT_OBSERVED", Severity: "info", SessionID: string(id.SessionID), Message: "This host has no route for this exact enrollment; another local host's routing is unknown.", Action: "Inspect the owning listener/controller before inferring a disconnect or authorizing recovery."})
			}
			if current && a.Status == state.AgentDisconnected {
				v.Attention = append(v.Attention, Attention{Code: "AGENT_DISCONNECTED", Severity: "info", SessionID: string(id.SessionID), Message: "Current enrollment is disconnected.", Action: "Fresh enrollment and explicit role authorization are required to participate again."})
			}
			agents[id.SessionID] = len(v.Agents)
			v.Agents = append(v.Agents, p)
		}
		planner := func(b state.PlannerBinding) Planner {
			a := v.Agents[agents[b.SessionID]]
			return Planner{RunID: b.RunID, BindingID: b.BindingID, AgentID: b.AgentID, SessionID: b.SessionID, Incarnation: b.Generation, Active: b.Active, Live: a.Live, Current: a.Current, RequestID: b.RequestID, RequestStatus: b.RequestStatus, ObjectiveID: b.AdoptedObjective, Reason: b.Reason, Decision: b.Decision}
		}
		for _, b := range r.Planners {
			p := planner(b)
			v.Planners = append(v.Planners, p)
			if b.Active && !p.Live || b.RequestStatus == "FAILED" || b.Reason == "session_no_longer_eligible" {
				v.Attention = append(v.Attention, Attention{Code: "PLANNER_UNAVAILABLE", Severity: "action", ObjectiveID: b.AdoptedObjective, SessionID: string(b.SessionID), Message: "Selected Planner is unavailable or its request failed.", Action: "Use the owning Planner console for fresh admission and explicit selection/replacement."})
			}
		}
		for _, b := range r.PlannerHistory {
			v.PlannerHistory = append(v.PlannerHistory, planner(b))
		}
	}
	for key, t := range s.Tasks {
		if key != t.ObjectiveID+"::"+t.ID || t.ID == "" || s.Objectives[t.ObjectiveID].ID == "" || (t.Status != "PENDING" && t.Status != "ASSIGNED" && t.Status != "BLOCKED" && t.Status != "COMPLETE") {
			return Snapshot{}, ErrAuthority
		}
		if (t.Status == "ASSIGNED" || t.Status == "BLOCKED") && t.AssignmentGeneration == 0 {
			return Snapshot{}, ErrAuthority
		}
		for _, dep := range t.DependsOn {
			if s.Tasks[t.ObjectiveID+"::"+dep].ID != dep || dep == t.ID {
				return Snapshot{}, ErrAuthority
			}
		}
		p := Task{Assignment: Assignment{ObjectiveID: t.ObjectiveID, TaskID: t.ID, Generation: t.AssignmentGeneration}, Description: t.Description, Scope: t.Scope, Dependencies: append([]string{}, t.DependsOn...), Capability: t.Capability, Status: t.Status, Attempt: t.Attempt,
			Claim: "UNBOUND", Branch: t.Branch, Worktree: t.Worktree, Blocker: t.Blocker, ValidationStatus: "NOT_RECORDED", Validators: append([]string{}, t.Validation...), ValidatedHead: t.ValidatedHead, ReviewStatus: s.Reviews[t.ObjectiveID].Status, Recovery: "NONE", History: []History{}}
		p.HumanAction = s.Objectives[t.ObjectiveID].AuthorizedBy == ""
		p.OwnershipRetained = t.Status == "ASSIGNED" || t.Status == "BLOCKED"
		if o, ok := ledger.Assignments[key]; ok {
			p.Claim, p.OperationalStatus = o.Claim, o.State
		}
		if t.AssignmentGeneration != 0 && t.Status != "PENDING" {
			owner := ownerOf(t.SessionID, t.WorkerDispatch)
			p.Owner = &owner
		}
		if d := t.WorkerDispatch; d != nil {
			if d.LastMessage != "" && d.LastMessage != agentprotocol.WorkerAcceptance && d.LastMessage != agentprotocol.WorkerProgress && d.LastMessage != agentprotocol.WorkerBlocker && d.LastMessage != agentprotocol.WorkerResult {
				return Snapshot{}, ErrAuthority
			}
			p.Assignment.DispatchID, p.DispatchPhase, p.LastMessage, p.Progress = d.Correlation.DispatchID, d.Phase, d.LastMessage, d.Detail
			if t.Status == "ASSIGNED" {
				a := v.Agents[agents[d.SessionID]]
				if !a.Current || !a.Live || a.Status != state.AgentBusy {
					p.HumanAction, p.Recovery = true, "RECONCILE_ACTIVE_ASSIGNMENT"
					v.Attention = append(v.Attention, taskAttention(p, "WORKER_SESSION_LOST", "Assigned worker is stale, disconnected, superseded, unavailable, or no longer BUSY.", "Resume the owning controller to fail closed, then inspect preserved work before explicit recovery."))
				}
			}
		}
		if t.ValidatedHead != "" && t.ValidationEvidence != "" {
			p.ValidationStatus = "RECORDED"
		}
		if t.Status == "BLOCKED" {
			v.System.BlockedTasks++
			p.HumanAction, p.Recovery = true, "EXPLICIT_RECOVERY_AVAILABLE"
			if t.SessionID == "" || t.Branch == "" || t.Worktree == "" || (p.Claim != "HELD" && !(p.Claim == "RELEASED" && p.OperationalStatus == "HANDOFF")) {
				p.Recovery = "MANUAL_RECONCILIATION_REQUIRED"
			}
			v.Attention = append(v.Attention, taskAttention(p, "TASK_BLOCKED", p.Blocker, "Inspect preserved work and stop external writers before the existing generation-fenced recovery flow."))
			if strings.HasPrefix(t.Blocker, "validation ") && strings.Contains(t.Blocker, " failed") {
				p.ValidationStatus = "FAILED"
				v.Attention = append(v.Attention, taskAttention(p, "VALIDATION_FAILED", "Go validation failed; worker success reports do not complete the task.", "Inspect the preserved assignment and validator evidence before recovery."))
			}
		}
		if t.Status == "ASSIGNED" {
			v.System.ActiveAssignments++
			if p.Claim == "MISMATCH" || p.Claim == "MISSING" || p.Claim == "UNBOUND" {
				p.HumanAction, p.Recovery = true, "MANUAL_RECONCILIATION_REQUIRED"
				v.Attention = append(v.Attention, taskAttention(p, "CROSS_RESOURCE_RECONCILIATION", "Reservation and operational ownership are not fully bound or do not match.", "Inspect the owning controller and partial allocation; do not steal claims or delete worktrees."))
			} else if p.OperationalStatus == "COMPLETE" && p.ValidationStatus == "RECORDED" {
				p.HumanAction, p.Recovery = true, "FINALIZATION_REQUIRED"
				v.Attention = append(v.Attention, taskAttention(p, "FINALIZATION_REQUIRED", "Validated operational completion awaits structured result collection.", "Use the existing Planner --resume flow for exact-HEAD metadata finalization."))
			} else if p.OperationalStatus == "BLOCKED" {
				p.HumanAction, p.Recovery = true, "RECONCILE_ACTIVE_ASSIGNMENT"
				v.Attention = append(v.Attention, taskAttention(p, "BLOCK_PUBLICATION_PENDING", "Operational execution is BLOCKED while the structured task remains ASSIGNED.", "Resume/reconcile the owning controller before authorizing recovery."))
			}
		}
		if t.Status == "COMPLETE" {
			v.System.CompletedTasks++
		}
		if t.ResultID != "" {
			r, ok := s.Results[t.ResultID]
			if !ok || r.ID != t.ResultID || r.TaskID != t.ID || r.ObjectiveID != t.ObjectiveID || r.AssignmentGeneration != t.AssignmentGeneration || r.SessionID != t.SessionID {
				return Snapshot{}, ErrAuthority
			}
			p.Result = &Result{ID: r.ID, Head: r.HeadSHA, RecordedAt: r.RecordedUTC, EvidencePresent: r.Validation != "", Artifacts: append([]string{}, r.Artifacts...)}
		}
		for _, h := range t.AssignmentHistory {
			history := History{Assignment: Assignment{ObjectiveID: t.ObjectiveID, TaskID: t.ID, Generation: h.AssignmentGeneration}, Owner: ownerOf(h.SessionID, h.WorkerDispatch), Attempt: h.Attempt, Branch: h.Branch, Worktree: h.Worktree, Blocker: h.Blocker}
			if h.WorkerDispatch != nil {
				history.Assignment.DispatchID = h.WorkerDispatch.Correlation.DispatchID
				history.DispatchPhase = h.WorkerDispatch.Phase
				i := agents[h.WorkerDispatch.SessionID]
				v.Agents[i].History = append(v.Agents[i].History, history)
			}
			p.History = append(p.History, history)
		}
		if d := t.WorkerDispatch; d != nil {
			i := agents[d.SessionID]
			if t.Status == "ASSIGNED" || t.Status == "BLOCKED" {
				v.Agents[i].Assignments = append(v.Agents[i].Assignments, p.Assignment)
			} else {
				v.Agents[i].History = append(v.Agents[i].History, History{Assignment: p.Assignment, Owner: *p.Owner, Attempt: t.Attempt, Branch: t.Branch, Worktree: t.Worktree, DispatchPhase: d.Phase, Blocker: t.Blocker})
			}
		}
		if p.HumanAction {
			v.System.HumanActionTasks++
		}
		sort.Strings(p.Dependencies)
		v.Tasks = append(v.Tasks, p)
	}
	start := len(s.Events) - 20
	if start < 0 {
		start = 0
	}
	for _, e := range s.Events[start:] {
		v.Audit = append(v.Audit, Audit{Actor: e.Actor, Operation: e.Operation, Revision: e.Generation, At: e.UTC})
	}
	sort.Strings(v.ActiveObjectives)
	sort.Slice(v.Objectives, func(i, j int) bool { return v.Objectives[i].ID < v.Objectives[j].ID })
	sort.Slice(v.Tasks, func(i, j int) bool {
		return assignmentKey(v.Tasks[i].Assignment) < assignmentKey(v.Tasks[j].Assignment)
	})
	plannerSort := func(p []Planner) {
		sort.Slice(p, func(i, j int) bool {
			if p[i].RunID != p[j].RunID {
				return p[i].RunID < p[j].RunID
			}
			return p[i].BindingID < p[j].BindingID
		})
	}
	plannerSort(v.Planners)
	plannerSort(v.PlannerHistory)
	for i := range v.Agents {
		sort.Slice(v.Agents[i].Assignments, func(a, b int) bool {
			return assignmentKey(v.Agents[i].Assignments[a]) < assignmentKey(v.Agents[i].Assignments[b])
		})
		sort.SliceStable(v.Agents[i].History, func(a, b int) bool {
			x, y := v.Agents[i].History[a].Assignment, v.Agents[i].History[b].Assignment
			if assignmentKey(x) == assignmentKey(y) {
				return x.Generation < y.Generation
			}
			return assignmentKey(x) < assignmentKey(y)
		})
	}
	sort.Slice(v.Agents, func(i, j int) bool { return v.Agents[i].Incarnation < v.Agents[j].Incarnation })
	sort.Slice(v.Attention, func(i, j int) bool {
		a, b := v.Attention[i], v.Attention[j]
		left, right := []string{a.Code, a.ObjectiveID, a.TaskID, a.SessionID}, []string{b.Code, b.ObjectiveID, b.TaskID, b.SessionID}
		for k := range left {
			if left[k] != right[k] {
				return left[k] < right[k]
			}
		}
		return a.Generation < b.Generation
	})
	return redact(v, *s, ledger.Sensitive)
}

func assignmentKey(a Assignment) string { return a.ObjectiveID + "::" + a.TaskID }
func ownerOf(session string, d *state.WorkerDispatch) Owner {
	o := Owner{Mode: "managed", OperationalSession: session}
	if d != nil {
		o.Mode = "enrolled"
		o.AgentID = d.AgentID
		o.SessionID = d.SessionID
		o.Incarnation = d.Incarnation
	}
	return o
}
func taskAttention(t Task, code, message, action string) Attention {
	if message == "" {
		message = "Go recorded a blocked assignment without a detailed reason; inspect existing evidence."
	}
	session := ""
	if t.Owner != nil {
		session = string(t.Owner.SessionID)
	}
	return Attention{Code: code, Severity: "action", ObjectiveID: t.Assignment.ObjectiveID, TaskID: t.Assignment.TaskID, SessionID: session, Generation: t.Assignment.Generation, Message: message, Action: action}
}
func hasRole(roles []agentprotocol.Role, want agentprotocol.Role) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}
func hasCap(caps []agentprotocol.Capability, want agentprotocol.Capability) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}
