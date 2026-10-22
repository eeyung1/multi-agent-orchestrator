package agentprotocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestV2AttachmentWireContractAndV1Compatibility(t *testing.T) {
	e, _ := testEnvelope(EnrollmentRequest)
	e.ProtocolVersion = Version2
	e.Identity.ProtocolVersion = Version2
	e.Invitation = &InvitationProof{ID: strings.Repeat("a", 32), Secret: strings.Repeat("b", 64)}
	for _, tc := range []struct {
		e      Envelope
		origin Origin
	}{
		{e, FromAgent},
		{Envelope{ProtocolVersion: Version2, Type: EnrollmentResponse, AgentID: e.AgentID, SessionID: e.SessionID, Enrollment: &EnrollmentDecision{State: EnrollmentAdmitted, SessionGeneration: ^uint64(0)}}, FromOrchestrator},
		{Envelope{ProtocolVersion: Version2, Type: SessionAcknowledgment, AgentID: e.AgentID, SessionID: e.SessionID, Acknowledgment: &SessionAck{For: Heartbeat, SessionGeneration: 7}}, FromOrchestrator},
	} {
		b, err := Encode(tc.e)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(b, tc.origin)
		if err != nil || !reflect.DeepEqual(got, tc.e) {
			t.Fatalf("round trip: %v", err)
		}
		if _, err := Decode(b, map[Origin]Origin{FromAgent: FromOrchestrator, FromOrchestrator: FromAgent}[tc.origin]); err == nil {
			t.Fatal("wrong origin accepted")
		}
	}
	legacy, _ := testEnvelope(EnrollmentRequest)
	b, err := Encode(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "invitation") || strings.Contains(string(b), "acknowledgment") {
		t.Fatal("v1 serialization changed")
	}
	e.ProtocolVersion = Version1
	e.Identity.ProtocolVersion = Version1
	if e.Validate() == nil {
		t.Fatal("v1 accepted v2 fields")
	}
}

func TestAttachmentFieldsFailClosed(t *testing.T) {
	base := Envelope{ProtocolVersion: Version2, Type: Heartbeat, AgentID: "agent", SessionID: "run"}
	for _, mutate := range []func(*Envelope){
		func(e *Envelope) {
			e.Invitation = &InvitationProof{ID: strings.Repeat("a", 32), Secret: strings.Repeat("b", 64)}
		},
		func(e *Envelope) { e.Acknowledgment = &SessionAck{For: Heartbeat, SessionGeneration: 7} },
		func(e *Envelope) {
			e.Type = SessionAcknowledgment
			e.Acknowledgment = &SessionAck{For: Result, SessionGeneration: 7}
		},
		func(e *Envelope) { e.Type = SessionAcknowledgment; e.Acknowledgment = &SessionAck{For: Heartbeat} },
		func(e *Envelope) {
			e.Type = EnrollmentResponse
			e.Enrollment = &EnrollmentDecision{State: EnrollmentAdmitted}
		},
		func(e *Envelope) {
			e.Type = EnrollmentResponse
			e.Enrollment = &EnrollmentDecision{State: EnrollmentRejected, Reason: "denied", SessionGeneration: 7}
		},
	} {
		e := base
		mutate(&e)
		b, _ := json.Marshal(e)
		if _, err := Decode(b, FromAgent); err == nil {
			t.Fatal("malformed/authority fields accepted")
		}
	}
	for _, p := range []InvitationProof{{}, {ID: strings.Repeat("a", 32), Secret: strings.Repeat("x", 64)}, {ID: strings.Repeat("a", 32), Secret: strings.Repeat("b", 65)}} {
		if p.Validate() == nil {
			t.Fatal("invalid proof accepted")
		}
	}
}

func TestV1RejectsV2WireFieldsEvenWhenNullOrZero(t *testing.T) {
	e, _ := testEnvelope(EnrollmentRequest)
	b, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"invitation":null`, `"INVITATION":null`, `"acknowledgment":null`} {
		wire := string(b[:len(b)-1]) + "," + field + "}"
		if _, err := Decode([]byte(wire), FromAgent); err == nil {
			t.Fatal("v1 accepted new wire field")
		}
	}
	e, _ = testEnvelope(EnrollmentResponse)
	b, err = Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"0", "null"} {
		wire := strings.Replace(string(b), `"state":"ADMITTED"`, `"state":"ADMITTED","session_generation":`+value, 1)
		if _, err := Decode([]byte(wire), FromOrchestrator); err == nil {
			t.Fatal("v1 accepted registry generation field")
		}
	}
}
