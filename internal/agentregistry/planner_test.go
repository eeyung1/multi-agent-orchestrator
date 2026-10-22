package agentregistry

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

func plannerIdentity(agent, session string) agentprotocol.Identity {
	id := identity(agent, session)
	id.ProtocolVersion = agentprotocol.Version3
	id.Capabilities = []agentprotocol.Capability{"planning", "coding"}
	id.SupportedRoles = []agentprotocol.Role{agentprotocol.RolePlanner, agentprotocol.RoleWorker}
	return id
}
func selectPlanner(t *testing.T, f fixture, ref Ref) state.PlannerBinding {
	t.Helper()
	b, err := f.registry.SelectPlanner("run", ref, "", true, "human selected exact session")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func proposal() planning.Proposal {
	return planning.Proposal{Plan: planning.Plan{ID: "suggested", Objective: "test objective", Tasks: []planning.Task{{ID: "task", Feature: "feature", Scope: "src", Capability: "coding", Validation: []string{"check"}}}}}
}

func TestPlannerCandidatesAndSelection(t *testing.T) {
	for _, test := range []struct {
		name       string
		change     func(*agentprotocol.Identity)
		transition Status
		advance    time.Duration
		eligible   bool
	}{
		{name: "future provider", eligible: true}, {name: "worker only", change: func(id *agentprotocol.Identity) { id.SupportedRoles = []agentprotocol.Role{agentprotocol.RoleWorker} }},
		{name: "missing planning", change: func(id *agentprotocol.Identity) { id.Capabilities = []agentprotocol.Capability{"coding"} }},
		{name: "old protocol", change: func(id *agentprotocol.Identity) { id.ProtocolVersion = agentprotocol.Version2 }},
		{name: "disconnected", transition: Disconnected}, {name: "stale", advance: 11 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			id := plannerIdentity("unknown-provider", "unique-session")
			if test.change != nil {
				test.change(&id)
			}
			v := admit(t, f, id)
			if test.transition != "" {
				if err := f.registry.Transition(v.Ref(), test.transition, "test transition"); err != nil {
					t.Fatal(err)
				}
			}
			f.clock.Advance(test.advance)
			candidates, err := f.registry.PlannerCandidates()
			if err != nil {
				t.Fatal(err)
			}
			if (len(candidates) == 1) != test.eligible {
				t.Fatalf("candidates=%v", candidates)
			}
			if _, err := f.registry.SelectPlanner("run", v.Ref(), "", false, "human"); err == nil {
				t.Fatal("selection without explicit human authorization")
			}
			b, err := f.registry.SelectPlanner("run", v.Ref(), "", true, "human selected session")
			if (err == nil) != test.eligible {
				t.Fatalf("selection: %v", err)
			}
			if test.eligible {
				if b.SessionID != v.Ref().SessionID || b.Generation != v.Ref().Generation || b.AgentID != v.Ref().AgentID {
					t.Fatal("binding lost exact identity")
				}
				after, _ := f.registry.Get(v.Ref().SessionID)
				if after.Session.AssignedRole != agentprotocol.RolePlanner {
					t.Fatal("no explicit role assignment")
				}
				if err := f.registry.Heartbeat(v.Ref()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPlannerCorrelationReplayAndAdoptionFence(t *testing.T) {
	f := newFixture(t)
	v := admit(t, f, plannerIdentity("agent", "session"))
	other := admit(t, f, plannerIdentity("other", "other-session"))
	b := selectPlanner(t, f, v.Ref())
	c, err := f.registry.BeginPlannerRequest(b.RunID, b.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.BeginPlannerRequest(b.RunID, b.BindingID); err == nil {
		t.Fatal("two requests pending")
	}
	response := agentprotocol.PlanningResponse{Correlation: c, Proposal: proposal()}
	wrong := c
	wrong.RequestID = "agent-generated"
	if err := f.registry.AcceptPlannerResponse(v.Ref(), agentprotocol.PlanningResponse{Correlation: wrong, Proposal: proposal()}); err == nil {
		t.Fatal("unknown request accepted")
	}
	if err := f.registry.AcceptPlannerResponse(other.Ref(), response); err == nil {
		t.Fatal("wrong session accepted")
	}
	wrongRef := v.Ref()
	wrongRef.Generation++
	if err := f.registry.AcceptPlannerResponse(wrongRef, response); err == nil {
		t.Fatal("wrong incarnation accepted")
	}
	if err := f.registry.AcceptPlannerResponse(v.Ref(), agentprotocol.PlanningResponse{Correlation: c}); err == nil {
		t.Fatal("malformed proposal accepted")
	}
	if err := f.registry.AcceptPlannerResponse(v.Ref(), response); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.AcceptPlannerResponse(v.Ref(), response); err == nil {
		t.Fatal("replayed completed response accepted")
	}
	s, err := f.registry.store.Read()
	if err != nil {
		t.Fatal(err)
	}
	evidence := state.PlannerEvidence{Correlation: c, ProposalDigest: planning.PlanDigest(response.Proposal.Plan)}
	plan := response.Proposal.Plan
	plan.ID = "go-issued-objective"
	if err := s.CheckPlannerEvidence(evidence, plan, f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	plan.Tasks[0].Scope = "elsewhere"
	if err := s.CheckPlannerEvidence(evidence, plan, f.clock.Now()); err == nil {
		t.Fatal("altered proposal accepted")
	}
	if len(s.Tasks) != 0 || len(s.Claims) != 0 {
		t.Fatal("planning minted task/claim")
	}
	c2, err := f.registry.BeginPlannerRequest(b.RunID, b.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	if c == c2 {
		t.Fatal("request reused")
	}
	if err := f.registry.AcceptPlannerResponse(v.Ref(), response); err == nil {
		t.Fatal("prior request replay accepted")
	}
}

func TestPlannerDisconnectSupersessionAndExplicitReplacement(t *testing.T) {
	for _, supersede := range []bool{false, true} {
		t.Run(map[bool]string{false: "disconnect", true: "supersede"}[supersede], func(t *testing.T) {
			f := newFixture(t)
			old := admit(t, f, plannerIdentity("agent", "old-session"))
			b := selectPlanner(t, f, old.Ref())
			c, err := f.registry.BeginPlannerRequest(b.RunID, b.BindingID)
			if err != nil {
				t.Fatal(err)
			}
			if !supersede {
				if err := f.registry.Disconnect(old.Ref(), "connection closed"); err != nil {
					t.Fatal(err)
				}
			}
			ref := old.Ref()
			fresh, err := f.registry.Enroll(Enrollment{Identity: plannerIdentity("agent", "new-session"), Supersedes: &ref, Decision: "human admitted replacement"})
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Session.AssignedRole != "" {
				t.Fatal("replacement inherited role")
			}
			if err := f.registry.AcceptPlannerResponse(old.Ref(), agentprotocol.PlanningResponse{Correlation: c, Proposal: proposal()}); err == nil {
				t.Fatal("stale result accepted")
			}
			if err := f.registry.Heartbeat(old.Ref()); !errors.Is(err, ErrFenced) {
				t.Fatal(err)
			}
			s, _ := f.registry.store.Read()
			if s.AgentRegistry.Planners[b.RunID].Active || s.AgentRegistry.Planners[b.RunID].RequestStatus != "FAILED" {
				t.Fatal("binding not invalidated")
			}
			if _, err := f.registry.SelectPlanner(b.RunID, fresh.Ref(), "", true, "human"); err == nil {
				t.Fatal("implicit replacement")
			}
			replacement, err := f.registry.SelectPlanner(b.RunID, fresh.Ref(), b.BindingID, true, "human explicitly replaced Planner")
			if err != nil {
				t.Fatal(err)
			}
			if replacement.BindingID == b.BindingID {
				t.Fatal("binding identity reused")
			}
			if err := f.registry.CheckPlanner(fresh.Ref(), c, false); err == nil {
				t.Fatal("previous-run correlation accepted")
			}
		})
	}
}

func TestPlannerConcurrentSelectionPersistsSingleWinner(t *testing.T) {
	f := newFixture(t)
	other, err := Open(f.root, f.policy, f.clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	a := admit(t, f, plannerIdentity("a", "a-session"))
	b := admit(t, f, plannerIdentity("b", "b-session"))
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	for i, ref := range []Ref{a.Ref(), b.Ref()} {
		registry := f.registry
		if i == 1 {
			registry = other
		}
		wg.Add(1)
		go func(registry *Registry, ref Ref) {
			defer wg.Done()
			<-start
			_, err := registry.SelectPlanner("same-run", ref, "", true, "human selection")
			errs <- err
		}(registry, ref)
	}
	close(start)
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("winners=%d", success)
	}
	reopened, err := Open(f.root, f.policy, f.clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reopened.store.Read()
	if err != nil {
		t.Fatal(err)
	}
	bound := snapshot.AgentRegistry.Planners["same-run"]
	if !bound.Active {
		t.Fatal("selection lost on reopen")
	}
	c, err := reopened.BeginPlannerRequest(bound.RunID, bound.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.registry.StopPlanner(bound.RunID, bound.BindingID, "human cancelled request"); err != nil {
		t.Fatal(err)
	}
	if err := reopened.AcceptPlannerResponse(Ref{bound.AgentID, bound.SessionID, bound.Generation}, agentprotocol.PlanningResponse{Correlation: c, Proposal: proposal()}); err == nil {
		t.Fatal("cancelled request accepted")
	}
}

func TestPlannerStalenessAndRoleWithdrawalInvalidateBinding(t *testing.T) {
	for _, expire := range []bool{false, true} {
		f := newFixture(t)
		v := admit(t, f, plannerIdentity("agent", "session"))
		b := selectPlanner(t, f, v.Ref())
		if expire {
			f.clock.Advance(11 * time.Second)
			_, _ = f.registry.List()
		} else {
			if err := f.registry.ClearRole(v.Ref(), "human removed role"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.registry.BeginPlannerRequest(b.RunID, b.BindingID); err == nil {
			t.Fatal("ineligible Planner started request")
		}
		s, _ := f.registry.store.Read()
		if s.AgentRegistry.Planners[b.RunID].Active {
			t.Fatal("inactive session retained selection")
		}
	}
}

func TestPlannerAssignedWorkerAndBusyAreExcluded(t *testing.T) {
	f := newFixture(t)
	id := plannerIdentity("agent", "session")
	id.Metadata.Provider = "PLANNER"
	v := admit(t, f, id)
	if err := f.registry.AssignRole(v.Ref(), agentprotocol.RoleWorker, "human selected Worker role"); err != nil {
		t.Fatal(err)
	}
	candidates, err := f.registry.PlannerCandidates()
	if err != nil || len(candidates) != 0 {
		t.Fatal("assigned Worker became Planner candidate", err)
	}
	if _, err := f.registry.SelectPlanner("run", v.Ref(), "", true, "human"); err == nil {
		t.Fatal("implicit role reassignment")
	}
	if err := f.registry.ClearRole(v.Ref(), "human removed Worker role"); err != nil {
		t.Fatal(err)
	}
	b := selectPlanner(t, f, v.Ref())
	if _, err := f.registry.BeginPlannerRequest(b.RunID, b.BindingID); err != nil {
		t.Fatal(err)
	}
	candidates, err = f.registry.PlannerCandidates()
	if err != nil || len(candidates) != 0 {
		t.Fatal("BUSY candidate", err)
	}
}

func TestCorruptOrMissingPlannerPersistenceFailsClosed(t *testing.T) {
	for _, change := range []func(*state.Snapshot){
		func(s *state.Snapshot) { s.AgentRegistry.Planners = nil },
		func(s *state.Snapshot) {
			b := s.AgentRegistry.Planners["run"]
			b.Generation++
			s.AgentRegistry.Planners["run"] = b
		},
		func(s *state.Snapshot) {
			b := s.AgentRegistry.Planners["run"]
			b.RequestStatus = "UNKNOWN"
			s.AgentRegistry.Planners["run"] = b
		},
		func(s *state.Snapshot) { s.Authority.Schema = 2 },
	} {
		f := newFixture(t)
		v := admit(t, f, plannerIdentity("agent", "session"))
		selectPlanner(t, f, v.Ref())
		s, err := f.registry.store.Read()
		if err != nil {
			t.Fatal(err)
		}
		change(&s)
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.root, "multi-agent-orchestrator", "state.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(f.root, f.policy, f.clock.Now); err == nil {
			t.Fatal("corrupt/downgraded Planner state accepted")
		}
	}
}
