package agentprotocol

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func testReference() AssignmentRef {
	return AssignmentRef{ObjectiveID: "objective-1", TaskID: "task-1", AssignmentGeneration: 42, Token: "claim-secret-fixture", SessionID: "session:run-1"}
}

func testEnvelope(kind MessageType) (Envelope, Origin) {
	id := testIdentity()
	e := Envelope{ProtocolVersion: Version1, Type: kind, AgentID: id.AgentID, SessionID: id.SessionID}
	origin := FromAgent
	switch kind {
	case EnrollmentRequest, CapabilityRegistration:
		e.Identity = &id
	case EnrollmentResponse:
		origin = FromOrchestrator
		e.Enrollment = &EnrollmentDecision{State: EnrollmentAdmitted}
	case RoleAssignment:
		origin = FromOrchestrator
		e.Role = RoleWorker
	case Assignment:
		origin = FromOrchestrator
		e.Role = RoleWorker
		r := testReference()
		e.Assignment = &r
	case AssignmentAcceptance, Progress, Blocker, Result:
		r := testReference()
		e.Assignment = &r
		if kind != AssignmentAcceptance {
			e.Detail = "reported progress, blocker or result; subject to Go verification"
		}
	}
	return e, origin
}

func TestAllLogicalMessagesRoundTrip(t *testing.T) {
	for _, kind := range []MessageType{EnrollmentRequest, EnrollmentResponse, CapabilityRegistration, RoleAssignment, Assignment, AssignmentAcceptance, Progress, Blocker, Result, Heartbeat, Handoff, Disconnect} {
		t.Run(string(kind), func(t *testing.T) {
			e, origin := testEnvelope(kind)
			data, err := Encode(e)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := Decode(data, origin)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(e, decoded) {
				t.Fatal("round trip lost identity or message data")
			}
			again, err := Encode(decoded)
			if err != nil || !bytes.Equal(data, again) {
				t.Fatalf("serialization is not deterministic: %v", err)
			}
			if _, err := Decode(data, Origin("unknown")); err == nil {
				t.Fatal("accepted unknown origin")
			}
			wrongOrigin := FromAgent
			if origin == FromAgent {
				wrongOrigin = FromOrchestrator
			}
			if _, err := Decode(data, wrongOrigin); err == nil {
				t.Fatal("accepted message from the opposite origin")
			}
		})
	}
}

func TestWireFieldNamesAndFutureProviderRoundTrip(t *testing.T) {
	e, _ := testEnvelope(EnrollmentRequest)
	e.Identity.Metadata = Metadata{Provider: "never-before-seen-provider", Runtime: "future-runtime"}
	e.Identity.Capabilities = []Capability{"future.example/analysis:v9"}
	e.Identity.SupportedRoles = []Role{RolePlanner, RoleWorker}
	data, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"protocol_version":"orchestrator-agent/v1","type":"enrollment_request","agent_id":"agent:developer-1","session_id":"session:run-1","identity":{"protocol_version":"orchestrator-agent/v1","agent_id":"agent:developer-1","session_id":"session:run-1","metadata":{"provider":"never-before-seen-provider","runtime":"future-runtime"},"capabilities":["future.example/analysis:v9"],"supported_roles":["PLANNER","WORKER"]}}`
	if string(data) != want {
		t.Fatalf("unexpected wire contract: %s", data)
	}
	got, err := Decode(data, FromAgent)
	if err != nil || !reflect.DeepEqual(got, e) {
		t.Fatalf("future provider required protocol special handling: %v", err)
	}
}

func TestOnlyGoCanDescribeAdmissionRoleOrAssignment(t *testing.T) {
	for _, kind := range []MessageType{EnrollmentResponse, RoleAssignment, Assignment} {
		e, _ := testEnvelope(kind)
		if kind == RoleAssignment || kind == Assignment {
			e.Role = RolePlanner
		}
		data, err := Encode(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(data, FromAgent); err == nil {
			t.Fatalf("agent could assert authoritative %s", kind)
		}
		if _, err := Decode(data, FromOrchestrator); err != nil {
			t.Fatal(err)
		}
	}
	e, _ := testEnvelope(EnrollmentRequest)
	e.Identity.Metadata.Provider = "PLANNER"
	e.Identity.Metadata.Runtime = "ORCHESTRATOR"
	data, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data, FromAgent)
	if err != nil || got.Role != "" || !reflect.DeepEqual(got.Identity.SupportedRoles, []Role{RoleWorker}) {
		t.Fatalf("provider metadata implied protocol authority/role: %v", err)
	}
}

func TestAssignmentReferenceFencesEveryOwnershipField(t *testing.T) {
	expected := testReference()
	if err := expected.ValidateAgainst(expected); err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*AssignmentRef){
		"objective":  func(r *AssignmentRef) { r.ObjectiveID = "another-objective" },
		"task":       func(r *AssignmentRef) { r.TaskID = "another-task" },
		"generation": func(r *AssignmentRef) { r.AssignmentGeneration++ },
		"token":      func(r *AssignmentRef) { r.Token = "forged-secret-token" },
		"session":    func(r *AssignmentRef) { r.SessionID = "another-session" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			r := expected
			change(&r)
			if err := r.ValidateAgainst(expected); err == nil || strings.Contains(err.Error(), r.Token) || strings.Contains(err.Error(), expected.Token) {
				t.Fatalf("ownership mismatch accepted or token leaked: %v", err)
			}
		})
	}
	for _, ref := range []AssignmentRef{
		{}, {TaskID: "task", AssignmentGeneration: 1, Token: "token", SessionID: "session"},
		{ObjectiveID: "objective", AssignmentGeneration: 1, Token: "token", SessionID: "session"},
		{ObjectiveID: "objective", TaskID: "task", Token: "token", SessionID: "session"},
		{ObjectiveID: "objective", TaskID: "task", AssignmentGeneration: 1, SessionID: "session"},
		{ObjectiveID: "objective", TaskID: "task", AssignmentGeneration: 1, Token: "token"},
	} {
		if err := ref.Validate(); err == nil {
			t.Fatal("accepted incomplete reference")
		}
		if err := expected.ValidateAgainst(ref); err == nil {
			t.Fatal("accepted invalid authoritative reference")
		}
	}
}

func TestGenerationRoundTripPreservesUint64Precision(t *testing.T) {
	e, _ := testEnvelope(Result)
	e.Assignment.AssignmentGeneration = ^uint64(0)
	data, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data, FromAgent)
	if err != nil || got.Assignment.AssignmentGeneration != e.Assignment.AssignmentGeneration {
		t.Fatalf("generation lost uint64 precision: %v", err)
	}
}

func TestEnvelopeRejectsMismatchedOrIrrelevantFields(t *testing.T) {
	cases := map[string]struct {
		kind   MessageType
		change func(*Envelope)
	}{
		"missing version":         {Heartbeat, func(e *Envelope) { e.ProtocolVersion = "" }},
		"unsupported version":     {Heartbeat, func(e *Envelope) { e.ProtocolVersion = "v9" }},
		"missing agent":           {Heartbeat, func(e *Envelope) { e.AgentID = "" }},
		"missing session":         {Heartbeat, func(e *Envelope) { e.SessionID = "" }},
		"unknown type":            {Heartbeat, func(e *Envelope) { e.Type = "self_authorize" }},
		"missing advertisement":   {EnrollmentRequest, func(e *Envelope) { e.Identity = nil }},
		"nested agent mismatch":   {EnrollmentRequest, func(e *Envelope) { e.Identity.AgentID = "other-agent" }},
		"nested session mismatch": {EnrollmentRequest, func(e *Envelope) { e.Identity.SessionID = "other-session" }},
		"nested version mismatch": {EnrollmentRequest, func(e *Envelope) { e.Identity.ProtocolVersion = "v9" }},
		"duplicate roles":         {EnrollmentRequest, func(e *Envelope) { e.Identity.SupportedRoles = []Role{RoleWorker, RoleWorker} }},
		"missing decision":        {EnrollmentResponse, func(e *Envelope) { e.Enrollment = nil }},
		"requested response":      {EnrollmentResponse, func(e *Envelope) { e.Enrollment.State = EnrollmentRequested }},
		"invalid decision":        {EnrollmentResponse, func(e *Envelope) { e.Enrollment.State = "READY" }},
		"missing rejection reason": {EnrollmentResponse, func(e *Envelope) {
			e.Enrollment.State = EnrollmentRejected
		}},
		"missing role":          {RoleAssignment, func(e *Envelope) { e.Role = "" }},
		"invalid role":          {RoleAssignment, func(e *Envelope) { e.Role = "ADMIN" }},
		"missing reference":     {Result, func(e *Envelope) { e.Assignment = nil }},
		"reference session":     {Progress, func(e *Envelope) { e.Assignment.SessionID = "old-session" }},
		"zero generation":       {Result, func(e *Envelope) { e.Assignment.AssignmentGeneration = 0 }},
		"missing report":        {Blocker, func(e *Envelope) { e.Detail = "" }},
		"oversized report":      {Progress, func(e *Envelope) { e.Detail = strings.Repeat("x", 65537) }},
		"irrelevant report":     {Heartbeat, func(e *Envelope) { e.Detail = "ignored?" }},
		"agent assigning role":  {Progress, func(e *Envelope) { e.Role = RolePlanner }},
		"irrelevant identity":   {Heartbeat, func(e *Envelope) { id := testIdentity(); e.Identity = &id }},
		"irrelevant decision":   {Heartbeat, func(e *Envelope) { e.Enrollment = &EnrollmentDecision{State: EnrollmentAdmitted} }},
		"irrelevant assignment": {EnrollmentRequest, func(e *Envelope) { r := testReference(); e.Assignment = &r }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e, origin := testEnvelope(tc.kind)
			tc.change(&e)
			if _, err := Encode(e); err == nil {
				t.Fatal("encoded malformed envelope")
			}
			data, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(data, origin); err == nil {
				t.Fatal("decoded malformed envelope")
			}
		})
	}
}

func TestStrictJSONRejectsAmbiguousAndMalformedMessages(t *testing.T) {
	e, _ := testEnvelope(Result)
	valid, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	base := string(valid)
	cases := []string{
		"", "null", "[]", base[:len(base)-1], base + " {}", base + " garbage",
		strings.Repeat(" ", MaxEnvelopeBytes+1),
		strings.Replace(base, `"type":"result"`, `"type":"assignment","type":"result"`, 1),
		strings.Replace(base, `"type":"result"`, `"TYPE":"assignment","type":"result"`, 1),
		strings.Replace(base, `"detail":`, `"approved":true,"detail":`, 1),
		strings.Replace(base, `"detail":`, `"origin":"ORCHESTRATOR","detail":`, 1),
		strings.Replace(base, `"token":`, `"authority":true,"token":`, 1),
		strings.Replace(base, `"assignment_generation":42`, `"assignment_generation":42,"assignment_generation":43`, 1),
		strings.Replace(base, `"assignment_generation":42`, `"assignment_generation":-1`, 1),
		strings.Replace(base, `"assignment_generation":42`, `"assignment_generation":1.5`, 1),
		strings.Replace(base, `"assignment_generation":42`, `"assignment_generation":18446744073709551616`, 1),
		strings.Replace(base, `"session:run-1"`, `null`, 1),
		strings.Replace(base, `"detail":`, `"extra":`+strings.Repeat("[", 34)+`0`+strings.Repeat("]", 34)+`,"detail":`, 1),
		string([]byte{0xff}),
	}
	for i, data := range cases {
		if _, err := Decode([]byte(data), FromAgent); err == nil {
			t.Fatalf("accepted malformed/ambiguous message %d", i)
		}
	}
	request, _ := testEnvelope(EnrollmentRequest)
	data, err := Encode(request)
	if err != nil {
		t.Fatal(err)
	}
	metadataRole := strings.Replace(string(data), `"provider":"codex"`, `"provider":"codex","role":"PLANNER"`, 1)
	if _, err := Decode([]byte(metadataRole), FromAgent); err == nil {
		t.Fatal("accepted a role hidden inside informational provider metadata")
	}
}

func TestOptionalAssignmentAndRejectedAdmission(t *testing.T) {
	for _, kind := range []MessageType{Heartbeat, Handoff, Disconnect} {
		e, origin := testEnvelope(kind)
		r := testReference()
		e.Assignment = &r
		data, err := Encode(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(data, origin); err != nil {
			t.Fatal(err)
		}
	}
	e, _ := testEnvelope(EnrollmentResponse)
	e.Enrollment = &EnrollmentDecision{State: EnrollmentRejected, Reason: "human authorization required"}
	if err := e.ValidateFrom(FromOrchestrator); err != nil {
		t.Fatal(err)
	}
}
