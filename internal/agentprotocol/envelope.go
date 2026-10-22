package agentprotocol

import "errors"

// MessageType is a logical protocol operation, never a provider-specific union.
// The vocabulary reserves future interactions without implementing their effects.
type MessageType string

const (
	EnrollmentRequest      MessageType = "enrollment_request"
	EnrollmentResponse     MessageType = "enrollment_response"
	CapabilityRegistration MessageType = "capability_registration"
	RoleAssignment         MessageType = "role_assignment"
	Assignment             MessageType = "assignment"
	AssignmentAcceptance   MessageType = "assignment_acceptance"
	Progress               MessageType = "progress"
	Blocker                MessageType = "blocker"
	Result                 MessageType = "result"
	Heartbeat              MessageType = "heartbeat"
	Handoff                MessageType = "handoff"
	Disconnect             MessageType = "disconnect"
)

// Origin must be supplied by a trusted caller, not decoded from message data.
// It is a direction check, not transport authentication or enrollment proof.
type Origin string

const (
	FromAgent        Origin = "AGENT"
	FromOrchestrator Origin = "ORCHESTRATOR"
)

// EnrollmentDecision describes a future Go admission response, not a state write.
type EnrollmentDecision struct {
	State  EnrollmentState `json:"state"`
	Reason string          `json:"reason,omitempty"`
}

// Envelope is a transport-neutral Phase 1 message. AgentID/SessionID always
// identify the participant, including on messages sent by Go to that agent.
// Identity is an advertisement; Role is explicit Go-issued responsibility;
// Assignment is a scheduler reference; Detail is untrusted report/reason text.
// Full task/proposal/artifact payload schemas remain future protocol work.
type Envelope struct {
	ProtocolVersion ProtocolVersion     `json:"protocol_version"`
	Type            MessageType         `json:"type"`
	AgentID         AgentID             `json:"agent_id"`
	SessionID       SessionID           `json:"session_id"`
	Identity        *Identity           `json:"identity,omitempty"`
	Enrollment      *EnrollmentDecision `json:"enrollment,omitempty"`
	Role            Role                `json:"role,omitempty"`
	Assignment      *AssignmentRef      `json:"assignment,omitempty"`
	Detail          string              `json:"detail,omitempty"`
}

func (e Envelope) Validate() error {
	if err := e.ProtocolVersion.Validate(); err != nil {
		return err
	}
	if err := e.AgentID.Validate(); err != nil {
		return err
	}
	if err := e.SessionID.Validate(); err != nil {
		return err
	}
	var identity, enrollment, role, assignment, optionalAssignment, detail, requiredDetail bool
	switch e.Type {
	case EnrollmentRequest, CapabilityRegistration:
		identity = true
	case EnrollmentResponse:
		enrollment = true
	case RoleAssignment:
		role = true
	case Assignment:
		role, assignment = true, true
	case AssignmentAcceptance:
		assignment = true
	case Progress, Blocker, Result:
		assignment, detail, requiredDetail = true, true, true
	case Heartbeat:
		optionalAssignment = true
	case Handoff, Disconnect:
		optionalAssignment, detail = true, true
	default:
		return errors.New("unsupported agent message type")
	}
	if (e.Identity != nil) != identity || (e.Enrollment != nil) != enrollment || (e.Role != "") != role {
		return errors.New("message fields do not match message type")
	}
	if identity {
		if err := e.Identity.Validate(); err != nil {
			return err
		}
		if e.Identity.ProtocolVersion != e.ProtocolVersion || e.Identity.AgentID != e.AgentID || e.Identity.SessionID != e.SessionID {
			return errors.New("advertised identity differs from envelope identity")
		}
	}
	if enrollment {
		if err := e.Enrollment.State.Validate(); err != nil {
			return err
		}
		if e.Enrollment.State != EnrollmentAdmitted && e.Enrollment.State != EnrollmentRejected {
			return errors.New("enrollment response requires ADMITTED or REJECTED")
		}
		if e.Enrollment.Reason != "" || e.Enrollment.State == EnrollmentRejected {
			if err := text("enrollment.reason", e.Enrollment.Reason, 4096); err != nil {
				return err
			}
		}
	}
	if role {
		if err := e.Role.Validate(); err != nil {
			return err
		}
	}
	if assignment && e.Assignment == nil || !assignment && !optionalAssignment && e.Assignment != nil {
		return errors.New("assignment reference does not match message type")
	}
	if e.Assignment != nil {
		if err := e.Assignment.Validate(); err != nil {
			return err
		}
		if e.Assignment.SessionID != e.SessionID {
			return errors.New("assignment session differs from envelope session")
		}
	}
	if !detail && e.Detail != "" {
		return errors.New("detail is not allowed for this message type")
	}
	if requiredDetail || e.Detail != "" {
		if err := text("detail", e.Detail, 65536); err != nil {
			return err
		}
	}
	return nil
}

// ValidateFrom rejects agent messages purporting to admit sessions, assign roles,
// or issue assignments. An accepted report still requires Go's ownership and
// validation gates. No message in this package applies an authority mutation.
func (e Envelope) ValidateFrom(origin Origin) error {
	if err := e.Validate(); err != nil {
		return err
	}
	want := FromAgent
	switch e.Type {
	case EnrollmentResponse, RoleAssignment, Assignment:
		want = FromOrchestrator
	}
	if origin != want {
		return errors.New("message type is not permitted from this origin")
	}
	return nil
}
