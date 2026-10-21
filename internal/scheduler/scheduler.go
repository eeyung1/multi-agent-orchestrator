package scheduler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
	"github.com/eeyung1/multi-agent-orchestrator/internal/workflow"
)

type Request struct {
	Workspace, Agent, Role, Base, TaskID string
	Plan                                 objective.Plan
	Completed                            map[string]bool
}
type Result struct {
	Task                                      objective.Task
	SessionID, State, Branch, Worktree, Token string
	Attempt, AssignmentGeneration             uint64
}

func Acquire(req Request) (Result, error) {
	if err := enrollment.RequireEnrolled(req.Workspace); err != nil {
		return Result{}, err
	}
	primary, err := repository.Primary(req.Workspace)
	if err != nil {
		return Result{}, err
	}
	if err := objective.Validate(primary, req.Plan); err != nil {
		return Result{}, err
	}
	id, err := repository.Identity(primary)
	if err != nil {
		return Result{}, err
	}
	store, err := state.Open(primary, id)
	if err != nil {
		return Result{}, err
	}
	token, err := randomToken()
	if err != nil {
		return Result{}, err
	}
	var selected objective.Task
	var attempt, generation uint64
	err = store.Update("ORCHESTRATOR", "acquire-next", func(s *state.Snapshot) error {
		if existing, ok := s.Objectives[req.Plan.ID]; ok && existing.AuthorizedBy != "" {
			if existing.PlanDigest != PlanDigest(req.Plan) || len(req.Completed) > 0 {
				return errors.New("acquisition differs from the human-authorized plan/evidence")
			}
		}
		if existing, ok := s.Objectives[req.Plan.ID]; ok && existing.Description != req.Plan.Objective {
			return fmt.Errorf("objective %s definition changed", req.Plan.ID)
		}
		if _, ok := s.Objectives[req.Plan.ID]; !ok {
			s.Objectives[req.Plan.ID] = state.Objective{ID: req.Plan.ID, Description: req.Plan.Objective, Status: "ACTIVE"}
		}
		for _, t := range req.Plan.Tasks {
			key := taskKey(req.Plan.ID, t.ID)
			if old, ok := s.Tasks[key]; ok {
				if !sameDefinition(old, t) {
					return fmt.Errorf("task %s definition changed", t.ID)
				}
				continue
			}
			s.Tasks[key] = newTask(req.Plan.ID, t)
		}
		completed := map[string]bool{}
		for id, v := range req.Completed {
			if v {
				completed[id] = true
			}
		}
		for _, st := range s.Tasks {
			if st.ObjectiveID == req.Plan.ID && st.Status == "COMPLETE" {
				completed[st.ID] = true
			}
		}
		active := []string{}
		for _, st := range s.Tasks {
			if st.Status == "ASSIGNED" || st.Status == "BLOCKED" {
				active = append(active, st.Scope)
			}
		}
		for _, claim := range s.Claims {
			active = append(active, claim.Scope)
		}
		eligible := objective.Eligible(req.Plan, completed, active)
		for _, candidate := range eligible {
			if req.TaskID != "" && candidate.ID != req.TaskID {
				continue
			}
			st := s.Tasks[taskKey(req.Plan.ID, candidate.ID)]
			if st.Status != "PENDING" {
				continue
			}
			selected = candidate
			st.Status = "ASSIGNED"
			st.Attempt++
			st.AssignmentGeneration = s.Authority.Generation + 1
			st.Token = token
			st.BaseSHA = req.Base
			attempt = st.Attempt
			generation = st.AssignmentGeneration
			s.Tasks[taskKey(req.Plan.ID, candidate.ID)] = st
			return nil
		}
		return errors.New("no eligible task")
	})
	if err != nil {
		return Result{}, err
	}

	started, err := workflow.Start(workflow.Request{Workspace: req.Workspace, Agent: req.Agent, Role: req.Role, Feature: selected.Feature, Scope: selected.Scope, Token: token, Base: req.Base, IsolationID: selected.Feature + "-" + token[len(token)-8:]})
	if err != nil {
		rollbackErr := store.Update("ORCHESTRATOR", "acquire-rollback", func(s *state.Snapshot) error {
			st := s.Tasks[taskKey(req.Plan.ID, selected.ID)]
			if st.Token == token {
				st.Status = "BLOCKED"
				st.SessionID = started.SessionID
				st.Branch = started.Branch
				st.Worktree = started.Worktree
				st.Blocker = err.Error()
				s.Tasks[taskKey(req.Plan.ID, selected.ID)] = st
			}
			return nil
		})
		if rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("persist failed acquisition: %w", rollbackErr))
		}
		return Result{Task: selected, SessionID: started.SessionID, State: started.State, Token: token, Attempt: attempt, AssignmentGeneration: generation}, fmt.Errorf("acquire task %s: %w", selected.ID, err)
	}
	if err := store.Update("ORCHESTRATOR", "bind-assignment", func(s *state.Snapshot) error {
		st := s.Tasks[taskKey(req.Plan.ID, selected.ID)]
		if st.Status != "ASSIGNED" || st.Token != token || st.AssignmentGeneration != generation {
			return errors.New("task assignment generation changed")
		}
		st.SessionID = started.SessionID
		st.Branch = started.Branch
		st.Worktree = started.Worktree
		s.Tasks[taskKey(req.Plan.ID, selected.ID)] = st
		return nil
	}); err != nil {
		return Result{}, fmt.Errorf("bind task assignment: %w", err)
	}
	return Result{Task: selected, SessionID: started.SessionID, State: started.State, Branch: started.Branch, Worktree: started.Worktree, Token: token, Attempt: attempt, AssignmentGeneration: generation}, nil
}

func Complete(workspace, objectiveID, taskID, token, sessionID string, generation uint64) error {
	primary, err := repository.Primary(workspace)
	if err != nil {
		return err
	}
	id, err := repository.Identity(primary)
	if err != nil {
		return err
	}
	store, err := state.Open(primary, id)
	if err != nil {
		return err
	}
	return store.Update("ORCHESTRATOR", "task-complete", func(s *state.Snapshot) error {
		if s.Objectives[objectiveID].AuthorizedBy != "" {
			return errors.New("managed objective completion requires validated result collection")
		}
		key := taskKey(objectiveID, taskID)
		st, ok := s.Tasks[key]
		if !ok {
			return errors.New("task not found")
		}
		if st.Status != "ASSIGNED" || st.Token != token || st.SessionID != sessionID || st.AssignmentGeneration != generation {
			return errors.New("stale or unauthorized task completion")
		}
		st.Status = "COMPLETE"
		st.Token = ""
		s.Tasks[key] = st
		all := true
		for _, t := range s.Tasks {
			if t.ObjectiveID == objectiveID && t.Status != "COMPLETE" {
				all = false
				break
			}
		}
		if all {
			o := s.Objectives[objectiveID]
			o.Status = "COMPLETE"
			s.Objectives[objectiveID] = o
		}
		return nil
	})
}
func taskKey(objectiveID, taskID string) string { return objectiveID + "::" + taskID }
func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "claim-" + hex.EncodeToString(b), nil
}

func newTask(objectiveID string, t objective.Task) state.Task {
	return state.Task{ID: t.ID, ObjectiveID: objectiveID, Feature: t.Feature, Scope: t.Scope, DependsOn: append([]string(nil), t.DependsOn...), Priority: t.Priority, Description: t.Description, AcceptanceCriteria: t.AcceptanceCriteria, Validation: t.Validation, Capability: t.Capability, Status: "PENDING"}
}
func sameDefinition(old state.Task, t objective.Task) bool {
	return old.Feature == t.Feature && old.Scope == t.Scope && old.Priority == t.Priority && strings.Join(old.DependsOn, "\x00") == strings.Join(t.DependsOn, "\x00") && old.Description == t.Description && reflect.DeepEqual(old.AcceptanceCriteria, t.AcceptanceCriteria) && reflect.DeepEqual(old.Validation, t.Validation) && old.Capability == t.Capability
}

// Register admits a human-confirmed plan before any worker can be scheduled.
// Digest/provenance are supplied by the deterministic controller, not an LLM.
func Register(workspace string, plan objective.Plan, authority state.Objective) error {
	if err := enrollment.RequireEnrolled(workspace); err != nil {
		return err
	}
	primary, err := repository.Primary(workspace)
	if err != nil {
		return err
	}
	if err := objective.Validate(primary, plan); err != nil {
		return err
	}
	id, err := repository.Identity(primary)
	if err != nil {
		return err
	}
	store, err := state.Open(primary, id)
	if err != nil {
		return err
	}
	return store.Update("ORCHESTRATOR", "admit-objective", func(s *state.Snapshot) error {
		if old, ok := s.Objectives[plan.ID]; ok {
			if old.PlanDigest != authority.PlanDigest || old.PolicyDigest != authority.PolicyDigest || old.ReconDigest != authority.ReconDigest || old.Description != plan.Objective {
				return errors.New("persisted objective differs; a new human decision is required")
			}
			return nil
		}
		authority.ID = plan.ID
		authority.Description = plan.Objective
		authority.Status = "ACTIVE"
		s.Objectives[plan.ID] = authority
		for _, t := range plan.Tasks {
			s.Tasks[taskKey(plan.ID, t.ID)] = newTask(plan.ID, t)
		}
		return nil
	})
}

func Verify(workspace, objectiveID string, a Result) error {
	s, err := Read(workspace)
	if err != nil {
		return err
	}
	t, ok := s.Tasks[taskKey(objectiveID, a.Task.ID)]
	if !ok || t.Status != "ASSIGNED" || t.Token != a.Token || t.SessionID != a.SessionID || t.AssignmentGeneration != a.AssignmentGeneration || t.Worktree != a.Worktree || t.Branch != a.Branch {
		return errors.New("stale or unauthorized assignment")
	}
	return nil
}
func Block(workspace, objectiveID string, a Result, reason string) error {
	primary, err := repository.Primary(workspace)
	if err != nil {
		return err
	}
	id, err := repository.Identity(primary)
	if err != nil {
		return err
	}
	store, err := state.Open(primary, id)
	if err != nil {
		return err
	}
	return store.Update("ORCHESTRATOR", "task-blocked", func(s *state.Snapshot) error {
		key := taskKey(objectiveID, a.Task.ID)
		t, ok := s.Tasks[key]
		if !ok || t.Status != "ASSIGNED" || t.Token != a.Token || t.SessionID != a.SessionID || t.AssignmentGeneration != a.AssignmentGeneration {
			return errors.New("stale blocker rejected")
		}
		t.Status = "BLOCKED"
		t.Blocker = reason
		s.Tasks[key] = t
		return nil
	})
}
func Read(workspace string) (state.Snapshot, error) {
	primary, err := repository.Primary(workspace)
	if err != nil {
		return state.Snapshot{}, err
	}
	id, err := repository.Identity(primary)
	if err != nil {
		return state.Snapshot{}, err
	}
	store, err := state.Open(primary, id)
	if err != nil {
		return state.Snapshot{}, err
	}
	return store.Read()
}

func Restore(workspace, objectiveID string) (objective.Plan, state.Objective, error) {
	s, err := Read(workspace)
	if err != nil {
		return objective.Plan{}, state.Objective{}, err
	}
	o, ok := s.Objectives[objectiveID]
	if !ok {
		return objective.Plan{}, o, errors.New("objective not found")
	}
	p := objective.Plan{ID: o.ID, Objective: o.Description}
	for _, t := range s.Tasks {
		if t.ObjectiveID == o.ID {
			p.Tasks = append(p.Tasks, objective.Task{ID: t.ID, Feature: t.Feature, Scope: t.Scope, DependsOn: t.DependsOn, Priority: t.Priority, Description: t.Description, AcceptanceCriteria: t.AcceptanceCriteria, Validation: t.Validation, Capability: t.Capability})
		}
	}
	// Stable ordering makes restart digests independent of map iteration.
	sort.Slice(p.Tasks, func(i, j int) bool { return p.Tasks[i].ID < p.Tasks[j].ID })
	return p, o, nil
}

// EncodeDefinition is shared by admission and restart verification.
func EncodeDefinition(p objective.Plan) []byte {
	sort.Slice(p.Tasks, func(i, j int) bool { return p.Tasks[i].ID < p.Tasks[j].ID })
	b, _ := json.Marshal(p)
	return b
}
func PlanDigest(p objective.Plan) string {
	sum := sha256.Sum256(EncodeDefinition(p))
	return hex.EncodeToString(sum[:])
}
