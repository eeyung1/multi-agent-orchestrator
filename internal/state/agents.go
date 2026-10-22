package state

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
)

// AgentSessionGeneration is an enrollment incarnation, not a scheduler
// AssignmentGeneration or the store's Authority.Generation revision counter.
type AgentSessionGeneration uint64

type AgentStatus string

const (
	AgentReady        AgentStatus = "READY"
	AgentBusy         AgentStatus = "BUSY"
	AgentBlocked      AgentStatus = "BLOCKED"
	AgentDisconnected AgentStatus = "DISCONNECTED"
)

// AgentSession records protocol admission only. Existing Session/Claim/Task
// records still own managed execution; they are not copied into this registry.
type AgentSession struct {
	Identity          agentprotocol.Identity  `json:"identity"`
	Generation        AgentSessionGeneration  `json:"session_generation"`
	AssignedRole      agentprotocol.Role      `json:"assigned_role,omitempty"`
	RoleDecision      string                  `json:"role_decision,omitempty"`
	Status            AgentStatus             `json:"status"`
	EnrolledAt        time.Time               `json:"enrolled_at"`
	LastSeenAt        time.Time               `json:"last_seen_at"`
	StateChangedAt    time.Time               `json:"state_changed_at"`
	AdmissionDecision string                  `json:"admission_decision"`
	StateReason       string                  `json:"state_reason"`
	DisconnectedAt    *time.Time              `json:"disconnected_at,omitempty"`
	SupersededBy      agentprotocol.SessionID `json:"superseded_by,omitempty"`
}

// AgentRegistryState lives in the same atomic resource as scheduler authority.
// Current session is derived from the highest generation for each AgentID; no
// second current-session index or Markdown projection claims that authority.
type AgentRegistryState struct {
	StaleAfterNS   time.Duration                            `json:"stale_after_ns"`
	LastGeneration AgentSessionGeneration                   `json:"last_session_generation"`
	ObservedAt     time.Time                                `json:"observed_at"`
	Sessions       map[agentprotocol.SessionID]AgentSession `json:"sessions"`
}

// RegistryDecision validates non-secret internal decision provenance/reasons.
func RegistryDecision(value string) error {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return errors.New("registry decision/reason must be nonempty canonical text")
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return errors.New("registry decision/reason contains control characters")
		}
	}
	return nil
}

func (s Snapshot) validateAgentRegistry() error {
	r := s.AgentRegistry
	if r == nil {
		for _, event := range s.Events {
			if strings.HasPrefix(event.Operation, "agent-registry-") {
				return errors.New("agent registry is missing despite durable registry history")
			}
		}
		return nil
	}
	if s.Authority.Schema != SchemaVersion || r.StaleAfterNS <= 0 || r.ObservedAt.IsZero() || r.Sessions == nil {
		return errors.New("malformed agent registry header")
	}
	if uint64(len(r.Sessions)) != uint64(r.LastGeneration) {
		return errors.New("agent registry incarnation history is incomplete")
	}
	legacy := map[string]bool{}
	for id := range s.Sessions {
		legacy[id] = true
	}
	for _, claim := range s.Claims {
		legacy[claim.SessionID] = true
	}
	for _, task := range s.Tasks {
		legacy[task.SessionID] = true
		for _, old := range task.AssignmentHistory {
			legacy[old.SessionID] = true
		}
	}
	seen := map[AgentSessionGeneration]bool{}
	chains := map[agentprotocol.AgentID][]AgentSession{}
	for key, session := range r.Sessions {
		if err := session.Identity.Validate(); err != nil {
			return fmt.Errorf("invalid registered identity: %w", err)
		}
		if key != session.Identity.SessionID || legacy[string(key)] {
			return errors.New("registry session key conflicts with identity or managed execution")
		}
		if session.Generation == 0 || session.Generation > r.LastGeneration || seen[session.Generation] {
			return errors.New("invalid or duplicate registry incarnation")
		}
		seen[session.Generation] = true
		if err := RegistryDecision(session.AdmissionDecision); err != nil {
			return err
		}
		if err := RegistryDecision(session.StateReason); err != nil {
			return err
		}
		if session.RoleDecision != "" {
			if err := RegistryDecision(session.RoleDecision); err != nil {
				return err
			}
		}
		if session.AssignedRole != "" {
			if err := session.AssignedRole.Validate(); err != nil {
				return err
			}
			supported := false
			for _, role := range session.Identity.SupportedRoles {
				supported = supported || role == session.AssignedRole
			}
			if !supported || session.RoleDecision == "" {
				return errors.New("registered assigned role lacks support or decision")
			}
		}
		if session.EnrolledAt.IsZero() || session.LastSeenAt.Before(session.EnrolledAt) ||
			session.StateChangedAt.Before(session.EnrolledAt) || session.LastSeenAt.After(r.ObservedAt) || session.StateChangedAt.After(r.ObservedAt) {
			return errors.New("invalid registry session timestamps")
		}
		switch session.Status {
		case AgentReady, AgentBusy, AgentBlocked:
			if session.DisconnectedAt != nil || session.SupersededBy != "" {
				return errors.New("active session has disconnect/supersession state")
			}
			if session.Status == AgentBusy && session.AssignedRole == "" {
				return errors.New("busy session has no assigned role")
			}
		case AgentDisconnected:
			if session.DisconnectedAt == nil || !session.DisconnectedAt.Equal(session.StateChangedAt) || session.DisconnectedAt.Before(session.LastSeenAt) {
				return errors.New("disconnected session lacks valid terminal timestamp")
			}
		default:
			return errors.New("unknown registry session status")
		}
		chains[session.Identity.AgentID] = append(chains[session.Identity.AgentID], session)
	}
	for _, chain := range chains {
		sort.Slice(chain, func(i, j int) bool { return chain[i].Generation < chain[j].Generation })
		for i, session := range chain {
			if i == len(chain)-1 {
				if session.SupersededBy != "" {
					return errors.New("current registry session is marked superseded")
				}
				continue
			}
			next := chain[i+1]
			if session.Status != AgentDisconnected || session.SupersededBy != next.Identity.SessionID ||
				session.DisconnectedAt.After(next.EnrolledAt) {
				return errors.New("invalid registry supersession chain")
			}
		}
	}
	return nil
}
