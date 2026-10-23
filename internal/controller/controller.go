// Package controller composes deterministic ORCHESTRATOR operations. Proposal
// output cannot authorize a run: admission requires explicit human provenance.
package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/providers"
	"github.com/eeyung1/multi-agent-orchestrator/internal/recon"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/review"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

type Decision struct {
	TaskID   string `json:"task_id,omitempty"`
	Kind     string `json:"kind"`
	Question string `json:"question"`
}
type TaskSummary struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Branch      string   `json:"branch,omitempty"`
	HeadSHA     string   `json:"head_sha,omitempty"`
	Validation  string   `json:"validation,omitempty"`
	Artifacts   []string `json:"artifacts,omitempty"`
	Blocker     string   `json:"blocker,omitempty"`
}
type Report struct {
	ObjectiveID  string        `json:"objective_id"`
	Objective    string        `json:"objective"`
	Status       string        `json:"status"`
	ReviewStatus string        `json:"review_status,omitempty"`
	Tasks        []TaskSummary `json:"tasks"`
	NotCompleted []string      `json:"not_completed"`
	Decisions    []Decision    `json:"decisions"`
	Limitations  []string      `json:"limitations"`
	NextAction   string        `json:"recommended_next_action"`
}

// WorkerContext is delivered by a provider-neutral argv/stdin adapter. It is
// internal; consolidated user reports deliberately omit tokens/generations.
type WorkerContext struct {
	Protocol     string               `json:"protocol"`
	Objective    string               `json:"objective"`
	Task         objective.Task       `json:"task"`
	Session      string               `json:"session"`
	Token        string               `json:"token"`
	Generation   uint64               `json:"assignment_generation"`
	Worktree     string               `json:"worktree"`
	Branch       string               `json:"branch"`
	Instructions []recon.File         `json:"repository_instructions"`
	Dependencies []state.ResultRecord `json:"prerequisite_results,omitempty"`
	Feedback     string               `json:"previous_attempt_feedback,omitempty"`
}

func Admit(workspace string, plan objective.Plan, c Config, r recon.Report, human string) error {
	return admitPlan(workspace, plan, c, r, human, nil)
}

// AdmitEnrolled preserves every existing policy/recon/human gate and adds an
// atomic selected-session/response fence inside scheduler registration.
func AdmitEnrolled(workspace string, plan objective.Plan, c Config, r recon.Report, human string, evidence state.PlannerEvidence) error {
	return admitPlan(workspace, plan, c, r, human, &evidence)
}
func admitPlan(workspace string, plan objective.Plan, c Config, r recon.Report, human string, evidence *state.PlannerEvidence) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.ValidateAdapters(workspace); err != nil {
		return err
	}
	if strings.TrimSpace(human) == "" {
		return errors.New("explicit human plan authorization is required")
	}
	if err := verifyAuthority(workspace); err != nil {
		return err
	}
	primary, err := repository.Primary(workspace)
	if err != nil {
		return err
	}
	if err := objective.Admit(primary, plan, c.Policy()); err != nil {
		return err
	}
	current, err := recon.Inspect(primary, plan.Objective)
	if err != nil {
		return err
	}
	if current.Head != r.Head || current.InstructionDigest != r.InstructionDigest {
		return errors.New("repository changed during planning; recon and authorization must be refreshed")
	}
	for _, t := range plan.Tasks {
		for _, scope := range current.ClaimedScopes {
			if overlap(t.Scope, scope) {
				return fmt.Errorf("task %s conflicts with existing ownership of %s", t.ID, scope)
			}
		}
		for _, dirty := range current.DirtyPaths {
			if overlap(t.Scope, dirty) {
				return fmt.Errorf("task %s overlaps uncommitted project work: %s", t.ID, dirty)
			}
		}
		for _, existing := range current.Existing {
			if existing.Status != "PENDING" && overlap(t.Scope, existing.Scope) {
				if existing.Status == "COMPLETE" && existing.ReviewStatus == "APPROVED" && existing.HeadSHA != "" && gitRun(primary, "merge-base", "--is-ancestor", existing.HeadSHA, current.Head) == nil {
					continue
				}
				return fmt.Errorf("task %s overlaps existing task %s; reconcile ownership/review first", t.ID, existing.ID)
			}
		}
	}
	plannerID, workerIDs := c.ProviderSelection()
	return scheduler.Register(primary, plan, state.Objective{PlannerEvidence: evidence, PlanDigest: scheduler.PlanDigest(plan), PolicyDigest: digest(c), ReconDigest: r.InstructionDigest, AuthorizedBy: human, BaseSHA: r.Head, PlannerProvider: plannerID, WorkerProviders: workerIDs})
}

// PolicyDigest returns the deterministic digest fenced by objective admission.
func PolicyDigest(c Config) string { return digest(c) }

// ReauthorizePolicy permits an existing objective to continue under a changed
// trusted execution policy only after explicit human authorization. It does not
// change the plan, task state, ownership, results, or repository recon boundary.
func ReauthorizePolicy(workspace, objectiveID string, c Config, human string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.ValidateAdapters(workspace); err != nil {
		return err
	}
	if strings.TrimSpace(human) == "" {
		return errors.New("explicit human policy reauthorization is required")
	}
	if err := verifyAuthority(workspace); err != nil {
		return err
	}
	primary, err := repository.Primary(workspace)
	if err != nil {
		return err
	}
	plan, authorized, err := scheduler.Restore(primary, objectiveID)
	if err != nil {
		return err
	}
	planDigest := scheduler.PlanDigest(plan)
	if authorized.AuthorizedBy == "" || authorized.PlanDigest != planDigest {
		return errors.New("saved objective plan lacks matching human authorization")
	}
	current, err := recon.Inspect(primary, plan.Objective)
	if err != nil {
		return err
	}
	if current.InstructionDigest != authorized.ReconDigest {
		return errors.New("project instructions changed; policy reauthorization requires fresh human reconciliation")
	}
	return scheduler.ReauthorizePolicy(primary, objectiveID, planDigest, authorized.PolicyDigest, authorized.ReconDigest, digest(c), human)
}

func Run(ctx context.Context, workspace, objectiveID string, c Config) (Report, error) {
	if err := c.Validate(); err != nil {
		return Report{}, err
	}
	if err := c.ValidateAdapters(workspace); err != nil {
		return Report{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	if err := verifyAuthority(workspace); err != nil {
		return Report{}, err
	}
	primary, err := repository.Primary(workspace)
	if err != nil {
		return Report{}, err
	}
	plan, authorized, err := scheduler.Restore(primary, objectiveID)
	if err != nil {
		return Report{}, err
	}
	if authorized.AuthorizedBy == "" || authorized.PlanDigest != scheduler.PlanDigest(plan) || authorized.PolicyDigest != digest(c) {
		return Report{}, errors.New("objective/configuration lacks matching human authorization")
	}
	if err := objective.Admit(primary, plan, c.Policy()); err != nil {
		return Report{}, err
	}
	r, err := recon.Inspect(primary, plan.Objective)
	if err != nil {
		return Report{}, err
	}
	if r.InstructionDigest != authorized.ReconDigest {
		return Report{}, errors.New("project instructions changed; a human reconciliation decision is required")
	}
	// Recover only already-published session completion, never a disappeared
	// process or stale heartbeat. All other assignments retain their ownership.
	s, err := scheduler.Read(primary)
	if err != nil {
		return Report{}, err
	}
	for _, t := range s.Tasks {
		if t.ObjectiveID == objectiveID && t.WorkerDispatch != nil {
			opStatus, _ := coordination.SessionStatus(primary, t.SessionID)
			if t.Status == "ASSIGNED" && c.Runtime != nil && !(t.ValidatedHead != "" && opStatus == "COMPLETE") {
				d := t.WorkerDispatch
				if e := c.Runtime.Registry.Check(workerRef(*d)); e != nil {
					a := assignmentResult(t)
					if e := scheduler.Block(primary, objectiveID, a, "enrolled worker session lost; explicit human recovery required"); e != nil {
						return Report{}, e
					}
					continue
				}
			}
		}
		if t.ObjectiveID != objectiveID || t.Status != "ASSIGNED" || t.ValidatedHead == "" {
			continue
		}
		status, e := coordination.SessionStatus(primary, t.SessionID)
		if e == nil && status == "COMPLETE" {
			_, e = review.FinalizeRecorded(input(primary, objectiveID, t))
			if e != nil {
				return Report{}, e
			}
		}
	}
	type outcome struct {
		id  string
		err error
	}
	done := make(chan outcome, c.MaxWorkers)
	running := map[string]bool{}
	attempted := map[string]bool{}
	var decisions []Decision
	for {
		s, err = scheduler.Read(primary)
		if err != nil {
			return Report{}, err
		}
		completed := map[string]bool{}
		for _, t := range s.Tasks {
			if t.ObjectiveID == objectiveID && t.Status == "COMPLETE" {
				completed[t.ID] = true
			}
		}
		ready := objective.Eligible(plan, completed, nil)
		for _, task := range ready {
			if len(running) >= c.MaxWorkers || ctx.Err() != nil {
				break
			}
			t := s.Tasks[objectiveID+"::"+task.ID]
			if t.Status != "PENDING" || attempted[t.ID] {
				continue
			}
			base, why := dependencyBase(primary, objectiveID, authorized.BaseSHA, task, s)
			if why != "" {
				attempted[t.ID] = true
				decisions = append(decisions, Decision{TaskID: t.ID, Kind: "dependency_integration", Question: why})
				continue
			}
			request := scheduler.Request{MaxWorkers: c.MaxWorkers, Workspace: primary, Agent: "worker-" + task.Capability, Role: task.Capability, Base: base, Plan: plan, TaskID: task.ID}
			if _, managed := c.Workers[task.Capability]; !managed {
				ref, e := c.selectWorker(task.Capability)
				if e != nil {
					decisions = append(decisions, Decision{TaskID: t.ID, Kind: "worker_unavailable", Question: e.Error()})
					continue
				}
				request.Worker = &ref
				request.Registry = c.Runtime.Registry
				request.Agent = "enrolled-worker"
			}
			a, e := scheduler.Acquire(request)
			if e != nil {
				if a.AssignmentGeneration != 0 || len(running) == 0 {
					attempted[t.ID] = true
				}
				decisions = append(decisions, Decision{TaskID: t.ID, Kind: "ownership_or_startup", Question: e.Error()})
				continue
			}
			running[t.ID] = true
			workers.Add(1)
			go func(a scheduler.Result) {
				defer workers.Done()
				e := runAssignment(ctx, primary, plan, a, c, r, authorized)
				if e != nil {
					// Published COMPLETE plus durable validation is recoverable
					// metadata collection, not permission to retry implementation.
					current, _ := scheduler.Read(primary)
					t := current.Tasks[objectiveID+"::"+a.Task.ID]
					status, _ := coordination.SessionStatus(primary, a.SessionID)
					var blockErr error
					if t.ValidatedHead == "" || status != "COMPLETE" {
						blockErr = scheduler.Block(primary, objectiveID, a, e.Error())
					}
					if blockErr != nil {
						e = fmt.Errorf("%v; blocker persistence: %w", e, blockErr)
					}
				}
				done <- outcome{id: a.Task.ID, err: e}
			}(a)
		}
		if len(running) == 0 {
			break
		}
		o := <-done
		delete(running, o.id)
		if o.err != nil {
			decisions = append(decisions, Decision{TaskID: o.id, Kind: "recovery_required", Question: o.err.Error()})
		}
	}
	return BuildReport(primary, objectiveID, decisions)
}

func runAssignment(ctx context.Context, primary string, plan objective.Plan, a scheduler.Result, c Config, r recon.Report, authorized state.Objective) error {
	// Prepare once per new assignment, before any worker or bounded repair. A
	// preparation failure is a blocker, never a reason to launch a repair worker.
	guard := assignmentGuard(primary, plan, a, c, authorized)
	preparationGuard := func() error {
		if err := guard(); err != nil {
			return err
		}
		return checkWorktreeInstructions(a.Worktree, r)
	}
	for i, command := range c.WorktreePreparation {
		program, err := providers.ResolveTrustedExecutable(command.Program, primary)
		if err != nil {
			return fmt.Errorf("worktree preparation command %d: %w", i+1, err)
		}
		command.Program = program
		result, err := execution.Run(execution.Request{
			Primary: primary, Worktree: a.Worktree, SessionID: a.SessionID,
			Command: command, Context: ctx,
			Guard:             preparationGuard,
			Timeout:           time.Duration(c.TimeoutSeconds) * time.Second,
			HeartbeatInterval: time.Duration(c.HeartbeatSeconds) * time.Second,
			Environment:       providers.Environment(primary),
		})
		if err != nil {
			return fmt.Errorf("worktree preparation command %d (%s) failed: %w; output: %s", i+1, commandDisplay(command), err, result.Output)
		}
	}
	feedback := ""
	for attempt := 0; ; attempt++ {
		err := execute(ctx, primary, plan, a, c, r, authorized, feedback)
		if err == nil {
			return nil
		}
		if a.WorkerDispatch != nil || attempt >= c.RepairAttempts || ctx.Err() != nil || errors.Is(err, review.ErrValidationBoundary) {
			return err
		}
		// Only repair our own just-finished child under the same ownership. This
		// cannot recover an absent writer, a stale generation, or a handoff.
		if e := scheduler.Verify(primary, plan.ID, a); e != nil {
			return err
		}
		sum, e := recon.InstructionsDigest(primary)
		if e != nil || sum != authorized.ReconDigest {
			return err
		}
		status, e := coordination.SessionStatus(primary, a.SessionID)
		if e != nil || status != "BLOCKED" {
			return err
		}
		if e := coordination.Transition(primary, a.SessionID, "WORKING"); e != nil {
			return fmt.Errorf("repair transition: %w", e)
		}
		feedback = err.Error()
	}
}

func assignmentGuard(primary string, plan objective.Plan, a scheduler.Result, c Config, authorized state.Objective) func() error {
	return func() error {
		if err := c.ValidateAdapters(primary); err != nil {
			return err
		}
		if err := enrollment.RequireEnrolled(primary); err != nil {
			return err
		}
		if err := scheduler.Verify(primary, plan.ID, a); err != nil {
			return err
		}
		if a.WorkerDispatch != nil {
			if c.Runtime == nil || !c.Runtime.Server.Connected(workerRef(*a.WorkerDispatch)) {
				return errors.New("enrolled worker disconnected")
			}
			if err := scheduler.VerifyWorker(primary, plan.ID, a); err != nil {
				return err
			}
		}
		sum, err := recon.InstructionsDigest(primary)
		if err != nil {
			return err
		}
		if sum != authorized.ReconDigest {
			return errors.New("project instructions changed during execution")
		}
		return nil
	}
}

func checkWorktreeInstructions(worktree string, r recon.Report) error {
	for _, f := range r.Instructions {
		if !strings.HasPrefix(f.Path, "/") {
			b, e := readInstruction(worktree, f.Path)
			if e != nil || b != f.Content {
				return fmt.Errorf("assigned worktree instructions differ: %s", f.Path)
			}
		}
	}
	return nil
}

func execute(ctx context.Context, primary string, plan objective.Plan, a scheduler.Result, c Config, r recon.Report, authorized state.Objective, feedback string) error {
	guard := assignmentGuard(primary, plan, a, c, authorized)
	if err := guard(); err != nil {
		return err
	}
	// Instructions must match before the worker starts; the task may itself be an
	// authorized edit to those instructions, subject to normal owned-output review.
	if err := checkWorktreeInstructions(a.Worktree, r); err != nil {
		return err
	}
	s, err := scheduler.Read(primary)
	if err != nil {
		return err
	}
	wc := WorkerContext{Protocol: "ORCHESTRATOR is the sole authority. Execute only this task in this worktree/scope. Do not edit coordination state, acquire other work, merge, push, deploy, or self-authorize. Follow supplied instructions. Commit only owned product changes locally without co-author trailers. Return blockers for missing permission/dependencies; never release or transfer ownership yourself. The controller runs configured validation and collects results.", Objective: plan.Objective, Task: a.Task, Session: a.SessionID, Token: a.Token, Generation: a.AssignmentGeneration, Worktree: a.Worktree, Branch: a.Branch, Instructions: r.Instructions}
	wc.Feedback = feedback
	w := c.Workers[a.Task.Capability]
	if w.Adapter != "" || a.WorkerDispatch != nil {
		wc.Protocol = "ORCHESTRATOR is the sole authority. Implement only the assigned task inside the exact worktree/scope. Follow supplied project instructions. Leave changes uncommitted: the Go controller checks owned paths and makes the local feature commit. Do not stage, commit, push, merge, deploy, edit shared coordination state, acquire tasks, or grant yourself permissions. Report blockers rather than bypassing denied operations."
	}
	for _, dep := range a.Task.DependsOn {
		t := s.Tasks[plan.ID+"::"+dep]
		if t.ResultID != "" {
			wc.Dependencies = append(wc.Dependencies, s.Results[t.ResultID])
		}
	}
	b, _ := json.Marshal(wc)
	args := make([]string, len(w.Args))
	for i, arg := range w.Args {
		args[i] = strings.ReplaceAll(arg, "{prompt}", string(b))
	}
	if w.Adapter != "" {
		var err error
		args, err = providers.WorkerArgs(w.Adapter)
		if err != nil {
			return err
		}
	}
	req := execution.Request{Primary: primary, Worktree: a.Worktree, SessionID: a.SessionID, Command: execution.Command{Program: w.Program, Args: args}, Context: ctx, Guard: guard, Input: string(b), Timeout: time.Duration(c.TimeoutSeconds) * time.Second, HeartbeatInterval: time.Duration(c.HeartbeatSeconds) * time.Second, RedactArguments: true}
	if w.Adapter != "" {
		req.Environment = providers.Environment(primary)
	}
	if a.WorkerDispatch != nil {
		if err := c.executeEnrolled(ctx, primary, plan, a, wc, req); err != nil {
			return err
		}
	} else if _, err := execution.Run(req); err != nil {
		return err
	}
	s, err = scheduler.Read(primary)
	if err != nil {
		return err
	}
	task := s.Tasks[plan.ID+"::"+a.Task.ID]
	gitProgram, err := providers.ResolveExecutable("git", primary)
	if err != nil {
		return err
	}
	// Snapshot the request before installing validator callbacks: cleanup Git
	// commands must not recursively invoke the validation boundary.
	gitReq := req
	gitReq.Input = ""
	gitReq.RedactArguments = false
	runGit := func(args ...string) error {
		gitReq.Command = execution.Command{Program: gitProgram, Args: args}
		result, err := execution.Run(gitReq)
		if err != nil {
			return fmt.Errorf("%w: %s", err, result.Output)
		}
		return nil
	}
	if w.Adapter != "" || a.WorkerDispatch != nil {
		if _, err := review.CommitOwned(task, guard, runGit); err != nil {
			return err
		}
	}
	boundary, err := review.BeginValidation(task, guard)
	if err != nil {
		return err
	}
	var evidence []string
	for index, name := range a.Task.Validation {
		req.Command = c.Validators[name]
		req.RedactArguments = false
		req.Input = ""
		restored := ""
		req.AfterExit = func(result execution.Result) error {
			if err := boundary.Verify(guard); err != nil {
				return err
			}
			// Preserve intermediates between checks, then clean on the final
			// check or first failure, before execution marks the session BLOCKED.
			if result.ExitCode != 0 || index == len(a.Task.Validation)-1 {
				var err error
				restored, err = boundary.Restore(guard, runGit)
				return err
			}
			return nil
		}
		result, err := execution.Run(req)
		if err != nil {
			if restored != "" {
				return fmt.Errorf("validation %s failed: %w; %s", name, err, restored)
			}
			return fmt.Errorf("validation %s failed: %w", name, err)
		}
		evidence = append(evidence, fmt.Sprintf("%s %s: PASS\n%s", name, commandDisplay(req.Command), result.Output))
		if restored != "" {
			evidence = append(evidence, restored)
		}
	}
	if err := guard(); err != nil {
		return err
	}
	in := input(primary, plan.ID, task)
	in.ValidatedHead = boundary.Head
	in.Validation = strings.Join(evidence, "\n")
	if a.WorkerDispatch != nil {
		s, err := scheduler.Read(primary)
		if err != nil {
			return err
		}
		d := s.Tasks[plan.ID+"::"+a.Task.ID].WorkerDispatch
		in.Validation += "\nEnrolled worker terminal report: " + d.Detail
	}
	in.Artifacts = []string{a.Worktree}
	if err := review.RecordValidation(in); err != nil {
		return err
	}
	_, err = review.FinalizeRecorded(in)
	return err
}

func BuildReport(workspace, objectiveID string, decisions []Decision) (Report, error) {
	p, err := review.Build(workspace, objectiveID)
	if err != nil {
		return Report{}, err
	}
	r := Report{ObjectiveID: objectiveID, Objective: p.Objective.Description, Status: p.Objective.Status, ReviewStatus: p.Review.Status, Decisions: decisions, Limitations: []string{"Local orchestration only; child processes are not OS/container sandboxed.", "No remote push, PR creation, merge or deployment was performed by the Planner.", "Passing configured checks does not prove full product acceptance."}, NextAction: "Review blockers and decide the listed authorization/recovery/integration questions."}
	results := map[string]state.ResultRecord{}
	for _, result := range p.Results {
		results[result.TaskID] = result
	}
	for _, t := range p.Tasks {
		result := results[t.ID]
		r.Tasks = append(r.Tasks, TaskSummary{ID: t.ID, Description: t.Description, Status: t.Status, Branch: t.Branch, HeadSHA: result.HeadSHA, Validation: result.Validation, Artifacts: result.Artifacts, Blocker: t.Blocker})
		if t.Status != "COMPLETE" {
			r.NotCompleted = append(r.NotCompleted, t.ID)
		}
		if t.Status == "ASSIGNED" {
			r.Decisions = append(r.Decisions, Decision{TaskID: t.ID, Kind: "existing_assignment", Question: "An existing assignment retains ownership. Reconcile its writer/checkpoint before authorizing recovery; no worker was relaunched."})
		}
		if t.Status == "BLOCKED" && t.Blocker != "" {
			r.Decisions = append(r.Decisions, Decision{TaskID: t.ID, Kind: "blocked", Question: t.Blocker})
		}
	}
	if p.Review.Status == "AWAITING_REVIEW" {
		r.NextAction = "Review the exact branch heads and evidence; record APPROVED or CHANGES_REQUESTED as the human reviewer. Integration/merge/deployment remain separate human-authorized actions."
	}
	sort.Slice(r.Decisions, func(i, j int) bool {
		if r.Decisions[i].TaskID == r.Decisions[j].TaskID {
			return r.Decisions[i].Kind < r.Decisions[j].Kind
		}
		return r.Decisions[i].TaskID < r.Decisions[j].TaskID
	})
	return r, nil
}

func dependencyBase(primary, objectiveID, base string, t objective.Task, s state.Snapshot) (string, string) {
	var heads []string
	for _, dep := range t.DependsOn {
		found := s.Tasks[objectiveID+"::"+dep]
		r, ok := s.Results[found.ResultID]
		if found.Status != "COMPLETE" || !ok {
			return "", "A prerequisite has no collected result evidence."
		}
		heads = append(heads, r.HeadSHA)
	}
	for _, candidate := range append([]string{base}, heads...) {
		all := true
		for _, head := range heads {
			if gitRun(primary, "merge-base", "--is-ancestor", head, candidate) != nil {
				all = false
				break
			}
		}
		if all {
			return candidate, ""
		}
	}
	return "", "Prerequisite branches are divergent. The human integration authority must reconcile them before this task can run; Planner will not merge them."
}
func input(workspace, objectiveID string, t state.Task) review.ResultInput {
	return review.ResultInput{Workspace: workspace, ObjectiveID: objectiveID, TaskID: t.ID, SessionID: t.SessionID, Token: t.Token, Branch: t.Branch, Worktree: t.Worktree, AssignmentGeneration: t.AssignmentGeneration}
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func overlap(a, b string) bool {
	return a == "." || b == "." || a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func commandDisplay(c execution.Command) string { b, _ := json.Marshal(c); return string(b) }
func gitRun(dir string, args ...string) error {
	c := exec.Command("git", args...)
	c.Dir = dir
	return c.Run()
}
func readInstruction(worktree, path string) (string, error) {
	full := filepath.Join(worktree, filepath.FromSlash(path))
	if _, err := state.CanonicalScope(worktree, path); err != nil {
		return "", err
	}
	b, err := os.ReadFile(full)
	return string(b), err
}

func verifyAuthority(workspace string) error {
	if err := enrollment.RequireEnrolled(workspace); err != nil {
		return err
	}
	r, err := enrollment.Preflight(workspace)
	if err != nil {
		return err
	}
	s, err := scheduler.Read(workspace)
	if err != nil {
		return err
	}
	if s.Authority.Enrollment == string(enrollment.Native) && r.Disposition != enrollment.Native {
		return errors.New("competing authority appeared after native enrollment; human reconciliation required")
	}
	return nil
}
