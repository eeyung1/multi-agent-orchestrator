// Package agentprotocol defines the provider-neutral agent wire/domain model.
// Validation checks message shape, not enrollment or execution authority. This
// package has no registry, transport, persistence, or coordination side effects.
package agentprotocol

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ProtocolVersion is independent of the existing structured-state schema and
// WorkerContext instruction string. Only explicitly supported versions pass.
type ProtocolVersion string

const Version1 ProtocolVersion = "orchestrator-agent/v1"

// Version2 adds local invitation enrollment and session acknowledgments. V1
// remains supported for existing models/adapters; it cannot use V2-only fields.
const Version2 ProtocolVersion = "orchestrator-agent/v2"

// Version3 adds correlated structured Planner requests/proposals.
const Version3 ProtocolVersion = "orchestrator-agent/v3"

func (v ProtocolVersion) Validate() error {
	if v != Version1 && v != Version2 && v != Version3 {
		return ErrUnsupportedVersion
	}
	return nil
}

// AgentID names a logical participant, not a provider or an ownership grant.
// Its stability across processes is a future admission-policy responsibility.
type AgentID string

// SessionID names one enrollment/runtime session. New processes can retain an
// AgentID while using a different SessionID; this does not authorize reconnects.
// AgentID and SessionID are separate namespaces even if their text is identical.
type SessionID string

func (id AgentID) Validate() error   { return identifier("agent_id", string(id)) }
func (id SessionID) Validate() error { return identifier("session_id", string(id)) }

type Role string

const (
	RolePlanner Role = "PLANNER"
	RoleWorker  Role = "WORKER"
)

func (r Role) Validate() error {
	if r != RolePlanner && r != RoleWorker {
		return errors.New("unsupported agent role")
	}
	return nil
}

// Capability is an extensible, case-sensitive machine identifier, not a provider
// enum. Names may contain ASCII letters/digits and . _ - : / after the first
// letter/digit (for example org.example/review:v2). Advertisements grant nothing.
type Capability string

func (c Capability) Validate() error {
	if len(c) == 0 || len(c) > 128 {
		return errors.New("capability must contain 1 to 128 bytes")
	}
	for i, ch := range c {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			continue
		}
		if i > 0 && strings.ContainsRune("._-:/", ch) {
			continue
		}
		return errors.New("invalid capability identifier")
	}
	return nil
}

// Metadata is informational only. There is deliberately no provider enum or
// executable, authority, role, claim, or scheduling configuration here.
type Metadata struct {
	Provider        string `json:"provider"`
	ProviderVersion string `json:"provider_version,omitempty"`
	Runtime         string `json:"runtime,omitempty"`
	RuntimeVersion  string `json:"runtime_version,omitempty"`
}

// Identity is an advertisement, not proof that Go has admitted this session.
// SupportedRoles describe capabilities; they are not assigned roles. Lists retain
// their supplied order and duplicates are rejected rather than silently merged.
type Identity struct {
	ProtocolVersion ProtocolVersion `json:"protocol_version"`
	AgentID         AgentID         `json:"agent_id"`
	SessionID       SessionID       `json:"session_id"`
	Metadata        Metadata        `json:"metadata"`
	Capabilities    []Capability    `json:"capabilities"`
	SupportedRoles  []Role          `json:"supported_roles"`
}

func (id Identity) Validate() error {
	if err := id.ProtocolVersion.Validate(); err != nil {
		return err
	}
	if err := id.AgentID.Validate(); err != nil {
		return err
	}
	if err := id.SessionID.Validate(); err != nil {
		return err
	}
	for _, field := range []struct{ name, value string }{
		{"metadata.provider", id.Metadata.Provider},
		{"metadata.provider_version", id.Metadata.ProviderVersion},
		{"metadata.runtime", id.Metadata.Runtime},
		{"metadata.runtime_version", id.Metadata.RuntimeVersion},
	} {
		if field.name != "metadata.provider" && field.value == "" {
			continue
		}
		if err := text(field.name, field.value, 256); err != nil {
			return err
		}
	}
	if len(id.Capabilities) == 0 || len(id.Capabilities) > 128 {
		return errors.New("identity requires 1 to 128 capabilities")
	}
	capabilities := make(map[Capability]bool, len(id.Capabilities))
	for _, c := range id.Capabilities {
		if err := c.Validate(); err != nil {
			return err
		}
		if capabilities[c] {
			return errors.New("duplicate capability")
		}
		capabilities[c] = true
	}
	if len(id.SupportedRoles) == 0 {
		return errors.New("at least one supported role is required")
	}
	roles := make(map[Role]bool, len(id.SupportedRoles))
	for _, r := range id.SupportedRoles {
		if err := r.Validate(); err != nil {
			return err
		}
		if roles[r] {
			return errors.New("duplicate supported role")
		}
		roles[r] = true
	}
	return nil
}

// EnrollmentState is only the Phase 1 admission vocabulary. No transitions,
// registry, READY/BUSY states, heartbeat timers, or reconnect behavior exist here.
type EnrollmentState string

const (
	EnrollmentRequested EnrollmentState = "REQUESTED"
	EnrollmentAdmitted  EnrollmentState = "ADMITTED"
	EnrollmentRejected  EnrollmentState = "REJECTED"
)

func (s EnrollmentState) Validate() error {
	switch s {
	case EnrollmentRequested, EnrollmentAdmitted, EnrollmentRejected:
		return nil
	default:
		return errors.New("unsupported enrollment state")
	}
}

func identifier(field, value string) error {
	if err := text(field, value, 256); err != nil {
		return err
	}
	for _, ch := range value {
		if unicode.IsSpace(ch) {
			return fmt.Errorf("%s must not contain whitespace", field)
		}
	}
	return nil
}

func text(field, value string, limit int) error {
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is empty, oversized, or noncanonical", field)
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return fmt.Errorf("%s contains control characters", field)
		}
	}
	return nil
}
