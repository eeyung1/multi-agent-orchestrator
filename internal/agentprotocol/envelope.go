package agentprotocol

import "errors"

// MessageType is a logical protocol operation, never a provider-specific union.
// The vocabulary reserves future interactions without implementing their effects.
type MessageType string

const (
	WorkerAssignment       MessageType = "worker_assignment"
	WorkerAcceptance       MessageType = "worker_acceptance"
	WorkerProgress         MessageType = "worker_progress"
	WorkerBlocker          MessageType = "worker_blocker"
	WorkerResult           MessageType = "worker_result"
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
	PlannerRequest         MessageType = "planner_request"
	PlannerResponse        MessageType = "planner_response"
	SessionAcknowledgment  MessageType = "session_acknowledgment"
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
	State             EnrollmentState `json:"state"`
	Reason            string          `json:"reason,omitempty"`
	SessionGeneration uint64          `json:"session_generation,omitempty"`
}

// InvitationProof is a sensitive one-use local bearer credential, not proof of
// provider identity. It is permitted only in a V2 enrollment request; never log it.
type InvitationProof struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

func (p InvitationProof) Validate() error {
	for _, f := range []struct {
		value string
		size  int
	}{{p.ID, 32}, {p.Secret, 64}} {
		if len(f.value) != f.size {
			return errors.New("invalid invitation proof")
		}
		for _, c := range f.value {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return errors.New("invalid invitation proof")
			}
		}
	}
	return nil
}

// SessionAck is Go-origin confirmation of a lifecycle operation, not an
// assignment or role grant. Requests never select a registry incarnation.
type SessionAck struct {
	For               MessageType `json:"for"`
	SessionGeneration uint64      `json:"session_generation"`
}

// Envelope is a transport-neutral Phase 1 message. AgentID/SessionID always
// identify the participant, including on messages sent by Go to that agent.
// Identity is an advertisement; Role is explicit Go-issued responsibility;
// Assignment is a scheduler reference; Detail is untrusted report/reason text.
// V3 adds typed reasoning requests/proposals; V4 adds bounded worker exchanges.
// Shape validation grants no execution authority.
type Envelope struct {
	ProtocolVersion  ProtocolVersion     `json:"protocol_version"`
	Type             MessageType         `json:"type"`
	AgentID          AgentID             `json:"agent_id"`
	SessionID        SessionID           `json:"session_id"`
	Identity         *Identity           `json:"identity,omitempty"`
	Enrollment       *EnrollmentDecision `json:"enrollment,omitempty"`
	Role             Role                `json:"role,omitempty"`
	Assignment       *AssignmentRef      `json:"assignment,omitempty"`
	Detail           string              `json:"detail,omitempty"`
	Invitation       *InvitationProof    `json:"invitation,omitempty"`
	PlanningRequest  *PlanningRequest    `json:"planning_request,omitempty"`
	PlanningResponse *PlanningResponse   `json:"planning_response,omitempty"`
	Work             *WorkerMessage      `json:"work,omitempty"`
	Acknowledgment   *SessionAck         `json:"acknowledgment,omitempty"`
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
	if err := e.validateWork(); err != nil {
		return err
	}
	if e.Type == PlannerRequest || e.Type == PlannerResponse {
		if e.ProtocolVersion != Version3 && e.ProtocolVersion != Version4 {
			return errors.New("Planner messages require v3 or v4")
		}
		if (e.PlanningRequest != nil) != (e.Type == PlannerRequest) || (e.PlanningResponse != nil) != (e.Type == PlannerResponse) {
			return errors.New("invalid Planner payload")
		}
		if e.PlanningRequest != nil {
			if err := e.PlanningRequest.Validate(); err != nil {
				return err
			}
		}
		if e.PlanningResponse != nil {
			if err := e.PlanningResponse.Validate(); err != nil {
				return err
			}
		}
	} else if e.PlanningRequest != nil || e.PlanningResponse != nil {
		return errors.New("unexpected Planner payload")
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
	case PlannerRequest, PlannerResponse, WorkerAssignment, WorkerAcceptance, WorkerProgress, WorkerBlocker, WorkerResult:
	case SessionAcknowledgment:
		if e.ProtocolVersion == Version1 || e.Acknowledgment == nil {
			return errors.New("session acknowledgment requires v2")
		}
	default:
		return ErrUnsupportedMessageType
	}
	if e.Invitation != nil {
		if e.ProtocolVersion == Version1 || e.Type != EnrollmentRequest {
			return errors.New("invitation is only permitted in v2 enrollment request")
		}
		if err := e.Invitation.Validate(); err != nil {
			return err
		}
	}
	if e.Type != SessionAcknowledgment && e.Acknowledgment != nil {
		return errors.New("unexpected session acknowledgment")
	}
	if e.Acknowledgment != nil {
		a := e.Acknowledgment
		if a.SessionGeneration == 0 || (a.For != Heartbeat && a.For != Disconnect && a.For != Handoff) {
			return errors.New("invalid session acknowledgment")
		}
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
		if e.ProtocolVersion == Version1 && e.Enrollment.SessionGeneration != 0 ||
			e.Enrollment.State != EnrollmentAdmitted && e.Enrollment.SessionGeneration != 0 ||
			e.ProtocolVersion != Version1 && e.Enrollment.State == EnrollmentAdmitted && e.Enrollment.SessionGeneration == 0 {
			return errors.New("invalid enrollment session generation")
		}
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
	case EnrollmentResponse, RoleAssignment, Assignment, SessionAcknowledgment, PlannerRequest, WorkerAssignment:
		want = FromOrchestrator
	}
	if origin != want {
		return errors.New("message type is not permitted from this origin")
	}
	return nil
}
