package agentprotocol

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
)

func TestWorkerV4Contract(t *testing.T) {
	corr := WorkerCorrelation{ObjectiveID: "objective", TaskID: "task", Generation: 42, DispatchID: "go-nonce"}
	yes := true
	examples := []Envelope{
		{Type: WorkerAssignment, Work: &WorkerMessage{Correlation: corr, Assignment: &WorkerAssignmentData{Objective: "write doc", Task: planning.Task{ID: "task", Scope: "docs", Description: "Write doc", Validation: []string{"check"}}, Worktree: "/tmp/assigned", Branch: "agent/task", Rules: "Go owns authority; leave uncommitted"}}},
		{Type: WorkerAcceptance, Work: &WorkerMessage{Correlation: corr, Accepted: &yes}},
		{Type: WorkerProgress, Work: &WorkerMessage{Correlation: corr, Detail: "writing"}},
		{Type: WorkerBlocker, Work: &WorkerMessage{Correlation: corr, Detail: "needs human"}},
		{Type: WorkerResult, Work: &WorkerMessage{Correlation: corr, Detail: "all writers stopped"}},
	}
	for _, e := range examples {
		e.ProtocolVersion = Version4
		e.AgentID = "future-agent"
		e.SessionID = "unique-enrolled-session"
		origin := FromAgent
		if e.Type == WorkerAssignment {
			origin = FromOrchestrator
		}
		b, err := Encode(e)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(b, origin)
		if err != nil || got.Work.Correlation != corr || got.AgentID != e.AgentID || got.SessionID != e.SessionID {
			t.Fatalf("roundtrip: %+v %v", got, err)
		}
		if _, err := Decode(b, map[Origin]Origin{FromAgent: FromOrchestrator, FromOrchestrator: FromAgent}[origin]); err == nil {
			t.Fatal("wrong origin accepted")
		}
		e.ProtocolVersion = Version3
		if e.Validate() == nil {
			t.Fatal("v3 worker payload accepted")
		}
	}
}
func TestWorkerMalformedRejected(t *testing.T) {
	yes := true
	e := Envelope{ProtocolVersion: Version4, Type: WorkerAcceptance, AgentID: "worker", SessionID: "enrolled", Work: &WorkerMessage{Correlation: WorkerCorrelation{ObjectiveID: "o", TaskID: "t", Generation: 1, DispatchID: "go-id"}, Accepted: &yes}}
	b, _ := Encode(e)
	for _, bad := range [][]byte{
		bytes.Replace(b, []byte(`"assignment_generation":1`), []byte(`"assignment_generation":0`), 1),
		bytes.Replace(b, []byte(`"dispatch_id":"go-id"`), []byte(`"dispatch_id":""`), 1),
		bytes.Replace(b, []byte(`"accepted":true`), []byte(`"accepted":true,"role":"PLANNER"`), 1),
		bytes.Replace(b, []byte(`"accepted":true`), []byte(`"accepted":true,"incarnation":999`), 1),
		bytes.Replace(b, []byte(`"accepted":true`), []byte(`"accepted":true,"accepted":false`), 1),
	} {
		if _, err := Decode(bad, FromAgent); err == nil {
			t.Fatalf("malformed worker frame accepted: %s", bad)
		}
	}
	for _, v := range []ProtocolVersion{Version1, Version2, Version3} {
		raw, _ := json.Marshal(Envelope{ProtocolVersion: v, Type: Heartbeat, AgentID: "a", SessionID: "s"})
		raw = append(raw[:len(raw)-1], []byte(`,"work":null}`)...)
		if _, err := Decode(raw, FromAgent); err == nil {
			t.Fatal("older version accepted v4 field")
		}
	}
}

func TestV4PreservesStructuredPlannerExchange(t *testing.T) {
	request := planningEnvelope()
	request.ProtocolVersion = Version4
	response := Envelope{ProtocolVersion: Version4, Type: PlannerResponse, AgentID: request.AgentID, SessionID: request.SessionID, PlanningResponse: &PlanningResponse{Correlation: request.PlanningRequest.Correlation, Proposal: planning.Proposal{Questions: []string{"Which acceptance outcome?"}}}}
	for _, e := range []Envelope{request, response} {
		origin := FromOrchestrator
		if e.Type == PlannerResponse {
			origin = FromAgent
		}
		b, err := Encode(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(b, origin); err != nil {
			t.Fatal("v4 broke Planner compatibility", err)
		}
	}
}
