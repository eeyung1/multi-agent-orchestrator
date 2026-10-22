package agentprotocol

import (
	"bytes"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
)

func planningEnvelope() Envelope {
	return Envelope{ProtocolVersion: Version3, Type: PlannerRequest, AgentID: "future-agent", SessionID: "session", PlanningRequest: &PlanningRequest{
		Correlation: PlannerCorrelation{RunID: "go-run", BindingID: "go-selection", RequestID: "go-request"}, Request: planning.Request{Objective: "implement feature", Recon: planning.Report{Workspace: "/workspace", Head: "abc"}, Capabilities: []string{"coding"}, Validators: []string{"test"}, AllowedScopes: []string{"src"}},
	}}
}
func TestPlannerProtocolStrictDirectionAndRoundTrip(t *testing.T) {
	e := planningEnvelope()
	b, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(b, FromOrchestrator)
	if err != nil || got.PlanningRequest.Correlation != e.PlanningRequest.Correlation {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := Decode(b, FromAgent); err == nil {
		t.Fatal("agent originated Planner request")
	}
	response := Envelope{ProtocolVersion: Version3, Type: PlannerResponse, AgentID: e.AgentID, SessionID: e.SessionID, PlanningResponse: &PlanningResponse{Correlation: e.PlanningRequest.Correlation, Proposal: planning.Proposal{Questions: []string{"Which acceptance criterion?"}}}}
	b, err = Encode(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(b, FromAgent); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func([]byte) []byte{
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"proposal":{`), []byte(`"proposal":{"authorized":true,`), 1)
		},
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"correlation":{`), []byte(`"correlation":{"session_generation":999,`), 1)
		},
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"request_id":"go-request"`), []byte(`"request_id":""`), 1)
		},
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"questions":["Which acceptance criterion?"]`), []byte(`"questions":[]`), 1)
		},
		func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(Version3), []byte(Version2)) },
	} {
		if _, err := Decode(mutation(b), FromAgent); err == nil {
			t.Fatal("accepted malformed/authority-bearing Planner message")
		}
	}
}
func TestLegacyVersionsRejectPlannerFieldPresence(t *testing.T) {
	for _, version := range []ProtocolVersion{Version1, Version2} {
		b := []byte(`{"protocol_version":"` + string(version) + `","type":"heartbeat","agent_id":"a","session_id":"s","planning_response":null}`)
		if _, err := Decode(b, FromAgent); err == nil {
			t.Fatal("legacy wire accepted new field")
		}
	}
}
