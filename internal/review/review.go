package review

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

type ResultInput struct {
	Workspace, ObjectiveID, TaskID, SessionID, Token, Branch, Worktree, Validation string
	ValidatedHead                                                                  string
	AssignmentGeneration                                                           uint64
	Artifacts, Blockers, Limitations                                               []string
}
type Package struct {
	Objective state.Objective      `json:"objective"`
	Tasks     []state.Task         `json:"tasks"`
	Results   []state.ResultRecord `json:"results"`
	Review    state.Review         `json:"review"`
}

func Collect(in ResultInput) (state.ResultRecord, error) {
	if err := enrollment.RequireEnrolled(in.Workspace); err != nil {
		return state.ResultRecord{}, err
	}
	if strings.TrimSpace(in.Validation) == "" {
		return state.ResultRecord{}, errors.New("validation evidence is required")
	}
	primary, err := repository.Primary(in.Workspace)
	if err != nil {
		return state.ResultRecord{}, err
	}
	head, err := git(in.Worktree, "rev-parse", "HEAD")
	if err != nil {
		return state.ResultRecord{}, fmt.Errorf("resolve result head: %w", err)
	}
	branch := in.Branch
	if branch == "" {
		branch, _ = git(in.Worktree, "branch", "--show-current")
	}
	id, err := repository.Identity(primary)
	if err != nil {
		return state.ResultRecord{}, err
	}
	store, err := state.Open(primary, id)
	if err != nil {
		return state.ResultRecord{}, err
	}
	if in.ValidatedHead != "" {
		status, err := coordination.SessionStatus(primary, in.SessionID)
		if err != nil {
			return state.ResultRecord{}, err
		}
		if status != "COMPLETE" {
			return state.ResultRecord{}, errors.New("validated result session has not completed")
		}
	}
	rec := state.ResultRecord{ID: fmt.Sprintf("%s::%s::%d", in.ObjectiveID, in.TaskID, in.AssignmentGeneration), ObjectiveID: in.ObjectiveID, TaskID: in.TaskID, SessionID: in.SessionID, AssignmentGeneration: in.AssignmentGeneration, Branch: branch, HeadSHA: head, Validation: in.Validation, Artifacts: in.Artifacts, Blockers: in.Blockers, Limitations: in.Limitations, RecordedUTC: time.Now().UTC().Format(time.RFC3339)}
	err = store.Update("ORCHESTRATOR", "collect-result", func(s *state.Snapshot) error {
		if s.Objectives[in.ObjectiveID].AuthorizedBy != "" && in.ValidatedHead == "" {
			return errors.New("managed objectives require controller-recorded exact-head validation")
		}
		key := in.ObjectiveID + "::" + in.TaskID
		t, ok := s.Tasks[key]
		if !ok {
			return errors.New("task not found")
		}
		if t.WorkerDispatch != nil && in.ValidatedHead == "" {
			return errors.New("enrolled results require exact-head validation")
		}
		if t.Status != "ASSIGNED" || t.Token != in.Token || t.SessionID != in.SessionID || t.AssignmentGeneration != in.AssignmentGeneration {
			return errors.New("stale or unauthorized result")
		}
		if t.Worktree != "" && (t.Worktree != in.Worktree || t.Branch != branch) {
			return errors.New("result does not match the recorded worktree/branch")
		}
		if in.ValidatedHead != "" && (t.ValidatedHead != in.ValidatedHead || head != in.ValidatedHead || t.ValidationEvidence != in.Validation) {
			return errors.New("result differs from validated evidence")
		}
		if old, ok := s.Results[rec.ID]; ok && old.HeadSHA != rec.HeadSHA {
			return errors.New("result generation already recorded with different head")
		}
		if err := s.CheckWorkerResult(t, time.Now().UTC()); err != nil {
			return err
		}
		s.Results[rec.ID] = rec
		t.ResultID = rec.ID
		t.Status = "COMPLETE"
		t.Token = ""
		if t.WorkerDispatch != nil {
			d := *t.WorkerDispatch
			d.Phase = "FINISHED"
			t.WorkerDispatch = &d
			if s.CheckWorkerSession(d, time.Now().UTC(), state.AgentBusy) == nil {
				a := s.AgentRegistry.Sessions[d.SessionID]
				a.Status, a.StateReason, a.StateChangedAt = state.AgentReady, "worker_result_validated", time.Now().UTC()
				s.AgentRegistry.ObservedAt = a.StateChangedAt
				s.AgentRegistry.Sessions[d.SessionID] = a
			}
		}
		s.Tasks[key] = t
		all := true
		ids := []string{}
		for _, task := range s.Tasks {
			if task.ObjectiveID != in.ObjectiveID {
				continue
			}
			if task.Status != "COMPLETE" {
				all = false
			}
			if task.ResultID != "" {
				ids = append(ids, task.ResultID)
			}
		}
		if all {
			sort.Strings(ids)
			o := s.Objectives[in.ObjectiveID]
			o.Status = "AWAITING_REVIEW"
			s.Objectives[in.ObjectiveID] = o
			s.Reviews[in.ObjectiveID] = state.Review{ObjectiveID: in.ObjectiveID, Status: "AWAITING_REVIEW", ResultIDs: ids}
		}
		return nil
	})
	if err != nil {
		return state.ResultRecord{}, err
	}
	return rec, nil
}
func Build(workspace, objectiveID string) (Package, error) {
	primary, err := repository.Primary(workspace)
	if err != nil {
		return Package{}, err
	}
	id, err := repository.Identity(primary)
	if err != nil {
		return Package{}, err
	}
	store, err := state.Open(primary, id)
	if err != nil {
		return Package{}, err
	}
	s, err := store.Read()
	if err != nil {
		return Package{}, err
	}
	o, ok := s.Objectives[objectiveID]
	if !ok {
		return Package{}, errors.New("objective not found")
	}
	p := Package{Objective: o, Review: s.Reviews[objectiveID]}
	for _, t := range s.Tasks {
		if t.ObjectiveID == objectiveID {
			p.Tasks = append(p.Tasks, t)
			if t.ResultID != "" {
				p.Results = append(p.Results, s.Results[t.ResultID])
			}
		}
	}
	sort.Slice(p.Tasks, func(i, j int) bool { return p.Tasks[i].ID < p.Tasks[j].ID })
	sort.Slice(p.Results, func(i, j int) bool { return p.Results[i].TaskID < p.Results[j].TaskID })
	return p, nil
}
func Decide(workspace, objectiveID, decision, actor string) error {
	decision = strings.ToUpper(strings.TrimSpace(decision))
	if decision != "APPROVED" && decision != "CHANGES_REQUESTED" {
		return errors.New("decision must be APPROVED or CHANGES_REQUESTED")
	}
	if strings.TrimSpace(actor) == "" {
		return errors.New("review actor is required")
	}
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
	return store.Update(actor, "review-decision", func(s *state.Snapshot) error {
		r, ok := s.Reviews[objectiveID]
		if !ok || r.Status != "AWAITING_REVIEW" {
			return errors.New("objective is not awaiting review")
		}
		r.Status = decision
		r.Decision = decision
		r.DecidedBy = actor
		r.DecidedUTC = time.Now().UTC().Format(time.RFC3339)
		s.Reviews[objectiveID] = r
		o := s.Objectives[objectiveID]
		o.Status = decision
		s.Objectives[objectiveID] = o
		return nil
	})
}
func git(dir string, args ...string) (string, error) {
	c := exec.Command("git", args...)
	c.Dir = dir
	b, err := c.Output()
	return strings.TrimSpace(string(b)), err
}
