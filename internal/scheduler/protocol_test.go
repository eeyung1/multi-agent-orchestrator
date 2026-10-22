package scheduler

import (
	"reflect"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
)

func TestProtocolReferenceUsesExistingSchedulerOwnership(t *testing.T) {
	d := initRepo(t)
	p := objective.Plan{ID: "objective", Objective: "Implement fixture", Tasks: []objective.Task{{ID: "task", Feature: "fixture", Scope: "fixture"}}}
	a, err := Acquire(Request{Workspace: d, Agent: "worker-unknown-provider", Role: "coding", Base: "main", Plan: p})
	if err != nil {
		t.Fatal(err)
	}
	before, err := Read(d)
	if err != nil {
		t.Fatal(err)
	}
	task := before.Tasks[p.ID+"::"+a.Task.ID]
	expected := agentprotocol.AssignmentRef{
		ObjectiveID: task.ObjectiveID, TaskID: task.ID,
		AssignmentGeneration: task.AssignmentGeneration, Token: task.Token,
		SessionID: agentprotocol.SessionID(task.SessionID),
	}
	reference := agentprotocol.AssignmentRef{
		ObjectiveID: p.ID, TaskID: a.Task.ID, AssignmentGeneration: a.AssignmentGeneration,
		Token: a.Token, SessionID: agentprotocol.SessionID(a.SessionID),
	}
	e := agentprotocol.Envelope{
		ProtocolVersion: agentprotocol.Version1, Type: agentprotocol.Result,
		AgentID: "logical-agent-not-provider", SessionID: reference.SessionID,
		Assignment: &reference, Detail: "untrusted worker result summary",
	}
	data, err := agentprotocol.Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := agentprotocol.Decode(data, agentprotocol.FromAgent)
	if err != nil {
		t.Fatal(err)
	}
	if err := decoded.Assignment.ValidateAgainst(expected); err != nil {
		t.Fatal(err)
	}
	// Shape-valid agent claims cannot mint or advance the scheduler's generation.
	forged := *decoded.Assignment
	forged.AssignmentGeneration++
	if err := forged.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := forged.ValidateAgainst(expected); err == nil {
		t.Fatal("protocol comparison accepted an agent-supplied generation")
	}
	if err := Complete(d, p.ID, a.Task.ID, a.Token, a.SessionID, forged.AssignmentGeneration); err == nil {
		t.Fatal("scheduler accepted an agent-supplied generation")
	}
	if err := Verify(d, p.ID, a); err != nil {
		t.Fatalf("protocol parsing replaced existing ownership: %v", err)
	}
	after, err := Read(d)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("protocol processing mutated scheduler state or accepted a result")
	}
}
