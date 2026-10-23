package agentregistry

import (
	"errors"
	"sync"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

func worker(t *testing.T, f fixture, agent, session string) Ref {
	t.Helper()
	id := identity(agent, session)
	id.ProtocolVersion = agentprotocol.Version4
	v := admit(t, f, id)
	if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, "human authorized worker"); err != nil {
		t.Fatal(err)
	}
	return v.Ref()
}
func TestWorkerReservationIsAtomicAndFenced(t *testing.T) {
	f := newFixture(t)
	ref := worker(t, f, "future-runtime", "enrolled-session")
	plan := objective.Plan{ID: "o", Objective: "parallel", Tasks: []objective.Task{{ID: "a", Feature: "a", Scope: "a", Capability: "coding"}, {ID: "b", Feature: "b", Scope: "b", Capability: "coding"}}}
	other, err := Open(f.root, f.policy, f.clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, r := range []*Registry{f.registry, other} {
		wg.Add(1)
		go func(i int, r *Registry) {
			defer wg.Done()
			<-start
			_, err := scheduler.Acquire(scheduler.Request{Registry: r, Worker: &ref, Workspace: f.root, Agent: "enrolled-worker", Role: "coding", Plan: plan, Base: "main", TaskID: plan.Tasks[i].ID})
			results <- err
		}(i, r)
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("worker reserved %d times", success)
	}
	s, err := scheduler.Read(f.root)
	if err != nil {
		t.Fatal(err)
	}
	assignments := 0
	for _, task := range s.Tasks {
		if task.Status == "ASSIGNED" {
			assignments++
			d := task.WorkerDispatch
			if d == nil || d.Incarnation != ref.Generation || d.Correlation.Generation != task.AssignmentGeneration || d.SessionID == agentprotocol.SessionID(task.SessionID) {
				t.Fatal("assignment competes with enrollment/operational identity")
			}
		}
	}
	if assignments != 1 {
		t.Fatal("duplicate ownership")
	}
	v, err := f.registry.Get(ref.SessionID)
	if err != nil || v.Session.Status != Busy {
		t.Fatal("reservation not BUSY")
	}
}
func TestWorkerReservationFailureCannotLeakTaskOrBusy(t *testing.T) {
	f := newFixture(t)
	ref := worker(t, f, "unknown", "runtime")
	err := f.registry.ReserveWorker(ref, "coding", func(s *state.Snapshot) error {
		s.Tasks["injected"] = state.Task{}
		s.Objectives["injected"] = state.Objective{}
		return errors.New("reservation rejected")
	})
	if err == nil {
		t.Fatal("reservation passed")
	}
	s, err := scheduler.Read(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tasks) != 0 || len(s.Objectives) != 0 || s.AgentRegistry.Sessions[ref.SessionID].Status != Ready {
		t.Fatal("partial reservation published")
	}
	for _, change := range []func(){
		func() {
			if err := f.registry.ClearRole(ref, "human removed role"); err != nil {
				t.Fatal(err)
			}
		},
		func() {
			if err := f.registry.AssignRole(ref, agentprotocol.RoleWorker, "human worker role"); err != nil {
				t.Fatal(err)
			}
			if err := f.registry.Transition(ref, Busy, "external busy"); err != nil {
				t.Fatal(err)
			}
		},
		func() {
			if err := f.registry.Disconnect(ref, "closed"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		change()
		if f.registry.ReserveWorker(ref, "coding", func(*state.Snapshot) error { return nil }) == nil {
			t.Fatal("ineligible reservation accepted")
		}
	}
}
func TestWorkerReservationRejectsCapabilitiesAndStaleness(t *testing.T) {
	f := newFixture(t)
	ref := worker(t, f, "unknown", "runtime")
	if f.registry.ReserveWorker(ref, "not-advertised", func(*state.Snapshot) error { return nil }) == nil {
		t.Fatal("capability mismatch accepted")
	}
	f.clock.Advance(f.policy.StaleAfter + 1)
	if f.registry.ReserveWorker(ref, "coding", func(*state.Snapshot) error { return nil }) == nil {
		t.Fatal("stale session reserved")
	}
	v, err := f.registry.Get(ref.SessionID)
	if err != nil || v.Session.Status != Disconnected {
		t.Fatal("expiry not durable")
	}
}

func TestEnrolledReservationHonorsConcurrencyDependenciesAndScopes(t *testing.T) {
	for _, mode := range []string{"max-workers", "dependency", "scope"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			one := worker(t, f, "one", "s1")
			two := worker(t, f, "two", "s2")
			a := objective.Task{ID: "a", Feature: "a", Scope: "a", Capability: "coding"}
			b := objective.Task{ID: "b", Feature: "b", Scope: "b", Capability: "coding"}
			if mode == "dependency" {
				b.DependsOn = []string{"a"}
			}
			if mode == "scope" {
				b.Scope = "a/nested"
			}
			plan := objective.Plan{ID: "o", Objective: "bounded concurrency", Tasks: []objective.Task{a, b}}
			first := scheduler.Request{Registry: f.registry, Worker: &one, Workspace: f.root, Agent: "external-one", Role: "coding", Plan: plan, TaskID: "a", Base: "main"}
			if mode == "max-workers" {
				first.MaxWorkers = 1
			}
			if _, err := scheduler.Acquire(first); err != nil {
				t.Fatal(err)
			}
			second := first
			second.Worker = &two
			second.TaskID = "b"
			if _, err := scheduler.Acquire(second); err == nil {
				t.Fatal("concurrency/dependency/scope gate bypassed")
			}
			v, err := f.registry.Get(two.SessionID)
			if err != nil || v.Session.Status != Ready {
				t.Fatal("failed reservation consumed second worker")
			}
			snapshot, err := scheduler.Read(f.root)
			if err != nil || snapshot.Tasks["o::b"].Status != "PENDING" || snapshot.Tasks["o::b"].Attempt != 0 {
				t.Fatal("failed reservation created partial ownership")
			}
		})
	}
}

type rejectedBinding struct{ *Registry }

func (rejectedBinding) CheckWorkerReservation(state.Snapshot, state.WorkerDispatch) error {
	return errors.New("injected binding failure")
}
func TestWorkerStartupFailuresPreserveEvidence(t *testing.T) {
	for _, mode := range []string{"worktree", "binding"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			ref := worker(t, f, "agent", "enrolled")
			plan := objective.Plan{ID: "o", Objective: "startup", Tasks: []objective.Task{{ID: "t", Feature: "t", Scope: "owned", Capability: "coding"}}}
			req := scheduler.Request{Registry: f.registry, Worker: &ref, Workspace: f.root, Agent: "enrolled-worker", Role: "coding", Plan: plan, TaskID: "t", Base: "main"}
			if mode == "worktree" {
				req.Base = "missing-reference"
			} else {
				req.Registry = rejectedBinding{f.registry}
			}
			if _, err := scheduler.Acquire(req); err == nil {
				t.Fatal("startup failure accepted")
			}
			s, err := scheduler.Read(f.root)
			if err != nil {
				t.Fatal(err)
			}
			task := s.Tasks["o::t"]
			if task.Status != "BLOCKED" || task.WorkerDispatch.Phase != "FAILED" || task.AssignmentGeneration == 0 || task.SessionID == "" {
				t.Fatal("startup lost ownership/failure evidence")
			}
			if mode == "binding" && (task.Worktree == "" || task.Branch == "") {
				t.Fatal("binding compensation lost created worktree")
			}
			if s.AgentRegistry.Sessions[ref.SessionID].Status != state.AgentBlocked {
				t.Fatal("failed worker still BUSY/READY")
			}
		})
	}
}
