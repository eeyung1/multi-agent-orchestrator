// Package operations projects existing Go authority for local operators. Views
// are observations, never ownership grants, persisted authority, or wire input.
package operations

import (
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

const Version = "orchestrator-operations/v1"

type Snapshot struct {
	Version           string      `json:"version"`
	ObservedAt        time.Time   `json:"observed_at"`
	Workspace         string      `json:"workspace"`
	RepositoryID      string      `json:"repository_id"`
	Initialized       bool        `json:"initialized"`
	StateSchema       int         `json:"state_schema"`
	StateProtocol     string      `json:"state_protocol"`
	Revision          uint64      `json:"revision"`
	Enrollment        string      `json:"enrollment"`
	MaxWorkers        *int        `json:"max_workers"`
	ConfigurationFrom string      `json:"configuration_source"`
	ActiveObjectives  []string    `json:"active_objective_ids"`
	Objectives        []Objective `json:"objectives"`
	Agents            []Agent     `json:"agents"`
	Planners          []Planner   `json:"planners"`
	PlannerHistory    []Planner   `json:"planner_history"`
	Tasks             []Task      `json:"tasks"`
	System            Counts      `json:"system"`
	Attention         []Attention `json:"attention"`
	Audit             []Audit     `json:"recent_audit"`
	Limitations       []string    `json:"limitations"`
}
type Objective struct {
	ID              string `json:"id"`
	Description     string `json:"description"`
	Status          string `json:"authoritative_status"`
	Authorized      bool   `json:"human_authorized"`
	ReviewStatus    string `json:"review_status"`
	PlannerProvider string `json:"managed_planner_provider,omitempty"`
}
type Assignment struct {
	ObjectiveID string `json:"objective_id"`
	TaskID      string `json:"task_id"`
	Generation  uint64 `json:"assignment_generation"`
	DispatchID  string `json:"dispatch_id,omitempty"`
}
type Owner struct {
	Mode               string                       `json:"mode"`
	AgentID            agentprotocol.AgentID        `json:"agent_id,omitempty"`
	SessionID          agentprotocol.SessionID      `json:"session_id,omitempty"`
	Incarnation        state.AgentSessionGeneration `json:"incarnation,omitempty"`
	OperationalSession string                       `json:"operational_session_id"`
}
type Agent struct {
	AgentID          agentprotocol.AgentID         `json:"agent_id"`
	SessionID        agentprotocol.SessionID       `json:"session_id"`
	Incarnation      state.AgentSessionGeneration  `json:"incarnation"`
	Protocol         agentprotocol.ProtocolVersion `json:"protocol_version"`
	Metadata         agentprotocol.Metadata        `json:"metadata"`
	Capabilities     []agentprotocol.Capability    `json:"capabilities"`
	SupportedRoles   []agentprotocol.Role          `json:"supported_roles"`
	AssignedRole     agentprotocol.Role            `json:"assigned_role"`
	Status           state.AgentStatus             `json:"authoritative_status"`
	StateReason      string                        `json:"state_reason"`
	RoleDecision     string                        `json:"role_decision,omitempty"`
	Current          bool                          `json:"current"`
	Live             bool                          `json:"live"`
	Stale            bool                          `json:"stale"`
	LastHeartbeat    time.Time                     `json:"last_heartbeat"`
	SupersededBy     agentprotocol.SessionID       `json:"superseded_by,omitempty"`
	Connection       string                        `json:"connection"` // OBSERVED_CONNECTED, OBSERVED_ABSENT, UNKNOWN
	PlannerCandidate bool                          `json:"planner_candidate"`
	WorkerCandidate  bool                          `json:"worker_candidate"` // registry eligibility; connection separate
	Assignments      []Assignment                  `json:"current_assignments"`
	History          []History                     `json:"assignment_history"`
}
type Planner struct {
	RunID         string                       `json:"run_id"`
	BindingID     string                       `json:"binding_id"`
	AgentID       agentprotocol.AgentID        `json:"agent_id"`
	SessionID     agentprotocol.SessionID      `json:"session_id"`
	Incarnation   state.AgentSessionGeneration `json:"incarnation"`
	Active        bool                         `json:"authoritative_active"`
	Live          bool                         `json:"live"`
	Current       bool                         `json:"current_session"`
	RequestID     string                       `json:"request_id,omitempty"`
	RequestStatus string                       `json:"request_status"`
	ObjectiveID   string                       `json:"adopted_objective,omitempty"`
	Reason        string                       `json:"reason"`
	Decision      string                       `json:"human_decision"`
}
type History struct {
	Assignment    Assignment `json:"assignment"`
	Owner         Owner      `json:"owner"`
	Attempt       uint64     `json:"attempt"`
	Branch        string     `json:"branch"`
	Worktree      string     `json:"worktree"`
	Blocker       string     `json:"blocker"`
	DispatchPhase string     `json:"dispatch_phase,omitempty"`
}
type Result struct {
	ID              string   `json:"id"`
	Head            string   `json:"head"`
	RecordedAt      string   `json:"recorded_at"`
	EvidencePresent bool     `json:"validation_evidence_present"`
	Artifacts       []string `json:"artifacts"`
}
type Task struct {
	Assignment        Assignment                `json:"assignment"`
	Description       string                    `json:"description"`
	Scope             string                    `json:"scope"`
	Dependencies      []string                  `json:"dependencies"`
	Capability        string                    `json:"required_capability"`
	Status            string                    `json:"authoritative_status"`
	Owner             *Owner                    `json:"owner"`
	OwnershipRetained bool                      `json:"ownership_retained"`
	Attempt           uint64                    `json:"attempt"`
	OperationalStatus string                    `json:"operational_status"`
	Claim             string                    `json:"claim_status"` // status only, never private claim material
	Branch            string                    `json:"branch"`
	Worktree          string                    `json:"worktree"`
	DispatchPhase     string                    `json:"dispatch_phase"`
	LastMessage       agentprotocol.MessageType `json:"last_worker_message,omitempty"`
	Progress          string                    `json:"agent_report_summary"`
	Blocker           string                    `json:"go_blocker"`
	ValidationStatus  string                    `json:"validation_status"`
	Validators        []string                  `json:"validators"`
	ValidatedHead     string                    `json:"validated_head"`
	ReviewStatus      string                    `json:"review_status"`
	Recovery          string                    `json:"recovery"`
	HumanAction       bool                      `json:"requires_human_action"`
	History           []History                 `json:"assignment_history"`
	Result            *Result                   `json:"result"`
}
type Counts struct {
	ActiveAssignments int  `json:"active_assignments"`
	ReadyWorkers      int  `json:"ready_workers"`
	AvailableWorkers  *int `json:"available_workers"`
	BlockedTasks      int  `json:"blocked_tasks"`
	CompletedTasks    int  `json:"completed_tasks"`
	HumanActionTasks  int  `json:"human_action_tasks"`
}
type Attention struct {
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	ObjectiveID string `json:"objective_id,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Generation  uint64 `json:"assignment_generation,omitempty"`
	Message     string `json:"message"`
	Action      string `json:"suggested_action"`
}
type Audit struct {
	Actor     string `json:"recorded_actor"`
	Operation string `json:"operation"`
	Revision  uint64 `json:"revision"`
	At        string `json:"at"`
}
