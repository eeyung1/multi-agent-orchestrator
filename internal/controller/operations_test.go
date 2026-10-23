//go:build unix

package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/operations"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
	"github.com/eeyung1/multi-agent-orchestrator/internal/recon"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
)

// Exercise the operator command parser/renderers while actual local IPC clients
// hold three distinct accepted assignments. Heartbeat acknowledgments and result
// channels establish every ordering; no sleep establishes concurrency or progress.
func TestOperationsCLIParallelAcceptance(t *testing.T) {
	f := enrolledFixture(t, 3)
	workers := append([]*agentattachment.Client{}, f.clients...)
	_, proof, err := f.server.Invite(agentattachment.InviteRequest{AgentID: "operator-planner", Roles: []agentprotocol.Role{agentprotocol.RolePlanner}, Capabilities: []agentprotocol.Capability{"planning"}, TTL: time.Minute}, true)
	if err != nil {
		t.Fatal(err)
	}
	id := agentprotocol.Identity{ProtocolVersion: agentprotocol.Version4, AgentID: "operator-planner", SessionID: "planner-session", Metadata: agentprotocol.Metadata{Provider: "future-planning-runtime", Runtime: "independent-planner"}, Capabilities: []agentprotocol.Capability{"planning"}, SupportedRoles: []agentprotocol.Role{agentprotocol.RolePlanner}}
	pc, err := agentattachment.DialLocal(f.ctx, f.socket, id, proof)
	if err != nil {
		t.Fatal(err)
	}
	f.clients = append(f.clients, pc)
	if err := pc.Heartbeat(f.ctx); err != nil {
		t.Fatal(err)
	}
	binding, err := f.registry.SelectPlanner("operations-run", pc.Binding(), "", true, "human selected operational acceptance Planner")
	if err != nil {
		t.Fatal(err)
	}
	p := objective.Plan{ID: "operations-objective", Objective: "Implement the requested fixture outputs", Tasks: []objective.Task{task("a"), task("b"), task("c")}}
	report, err := recon.Inspect(f.workspace, p.Objective)
	if err != nil {
		t.Fatal(err)
	}
	planningDone := make(chan error, 1)
	go func() {
		request, e := pc.ReceivePlanning(f.ctx)
		if e == nil {
			e = pc.RespondPlanning(f.ctx, request, planning.Proposal{Plan: p})
		}
		planningDone <- e
	}()
	enrolled := &planner.Enrolled{Registry: f.registry, Server: f.server, Binding: binding, Timeout: time.Minute}
	proposal, err := enrolled.Propose(f.ctx, planner.Request{Objective: p.Objective, Recon: report, Capabilities: f.config.Capabilities(), Validators: f.config.Checks(), AllowedScopes: f.config.AllowedScopes})
	if err != nil {
		t.Fatal(err)
	}
	if err := receive(t, f.ctx, planningDone); err != nil {
		t.Fatal(err)
	}
	if err := AdmitEnrolled(f.workspace, proposal.Plan, f.config, report, "human approved exact operational plan", enrolled.Evidence()); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		report Report
		err    error
	}
	done := make(chan outcome, 1)
	go func() { r, e := Run(f.ctx, f.workspace, p.ID, f.config); done <- outcome{r, e} }()
	assignments := make([]agentprotocol.WorkerMessage, 3)
	for i, c := range workers {
		w, e := c.ReceiveWork(f.ctx)
		if e != nil {
			t.Fatal(e)
		}
		assignments[i] = w
		if e := reportWorker(c, f, w, agentprotocol.WorkerAcceptance, ""); e != nil {
			t.Fatal(e)
		}
		if e := c.Heartbeat(f.ctx); e != nil {
			t.Fatal(e)
		}
	}
	opt := operations.Options{Connected: f.server.Connected, MaxWorkers: &f.config.MaxWorkers, ConfigurationSource: "owning_host_policy"}
	query := func() operations.Snapshot {
		t.Helper()
		var jsonOut, human bytes.Buffer
		if e := operations.RunWithOptions(f.ctx, []string{"status", "--workspace", f.workspace, "--json"}, &jsonOut, opt); e != nil {
			t.Fatal(e)
		}
		var v operations.Snapshot
		if e := json.Unmarshal(jsonOut.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
		if e := operations.RunWithOptions(f.ctx, []string{"status", "--workspace", f.workspace}, &human, opt); e != nil {
			t.Fatal(e)
		}
		for _, task := range v.Tasks {
			if !strings.Contains(human.String(), task.Assignment.TaskID) {
				t.Fatal("human/JSON status disagree")
			}
		}
		return v
	}
	inFlight := query()
	if inFlight.System.ActiveAssignments != 3 || len(inFlight.Planners) != 1 || inFlight.Planners[0].BindingID != binding.BindingID || inFlight.Planners[0].ObjectiveID != p.ID {
		t.Fatalf("in-flight projection: %+v", inFlight)
	}
	tasks, trees, sessions, gens := map[string]bool{}, map[string]bool{}, map[agentprotocol.SessionID]bool{}, map[uint64]bool{}
	for _, task := range inFlight.Tasks {
		if task.Status != "ASSIGNED" || task.DispatchPhase != "ACCEPTED" || task.Claim != "HELD" || task.Owner == nil {
			t.Fatal("not three simultaneous accepted owners")
		}
		tasks[task.Assignment.TaskID] = true
		trees[task.Worktree] = true
		sessions[task.Owner.SessionID] = true
		gens[task.Assignment.Generation] = true
		busy := false
		for _, a := range inFlight.Agents {
			if a.SessionID == task.Owner.SessionID {
				busy = a.Status == agentregistry.Busy && a.Incarnation == task.Owner.Incarnation && a.Connection == "OBSERVED_CONNECTED"
			}
		}
		if !busy {
			t.Fatal("snapshot does not show exact BUSY session")
		}
	}
	if len(tasks) != 3 || len(trees) != 3 || len(sessions) != 3 || len(gens) != 3 {
		t.Fatal("operator view collapsed concurrent assignments")
	}
	if err := reportWorker(workers[1], f, assignments[1], agentprotocol.WorkerProgress, "operator-visible bounded progress"); err != nil {
		t.Fatal(err)
	}
	if err := workers[1].Heartbeat(f.ctx); err != nil {
		t.Fatal(err)
	}
	progress := query()
	found := false
	for _, task := range progress.Tasks {
		found = found || task.Progress == "operator-visible bounded progress"
	}
	if !found {
		t.Fatal("progress not visible")
	}
	if err := workers[0].Disconnect(f.ctx); err != nil {
		t.Fatal(err)
	}
	lost := query()
	attention := false
	for _, item := range lost.Attention {
		attention = attention || item.Code == "AGENT_DISCONNECTED" || item.Code == "WORKER_SESSION_LOST" || item.Code == "TASK_BLOCKED"
	}
	if !attention {
		t.Fatal("lost worker needs no attention")
	}
	for i := 1; i < 3; i++ {
		if err := writeWorkerResult(assignments[i]); err != nil {
			t.Fatal(err)
		}
		if err := reportWorker(workers[i], f, assignments[i], agentprotocol.WorkerResult, "all task writers stopped"); err != nil {
			t.Fatal(err)
		}
	}
	first := receive(t, f.ctx, done)
	if first.err != nil {
		t.Fatal(first.err)
	}
	blocked := query()
	if blocked.System.BlockedTasks != 1 || blocked.System.CompletedTasks != 2 || blocked.System.ActiveAssignments != 0 {
		t.Fatalf("completed/blocked view: %+v", blocked)
	}
	for _, task := range blocked.Tasks {
		if task.Status == "COMPLETE" && (task.Result == nil || !task.Result.EvidencePresent || task.ValidatedHead == "") {
			t.Fatal("missing Go-collected evidence")
		}
	}
	var action bytes.Buffer
	if err := operations.Run(f.ctx, []string{"recover-task", "--workspace", f.workspace, "--objective", p.ID, "--task", assignments[0].Correlation.TaskID, "--generation", fmt.Sprint(assignments[0].Correlation.Generation), "--actor", "acceptance-operator", "--reason", "reviewed preserved work and stopped old writer", "--yes", "--writers-stopped"}, &action); err != nil {
		t.Fatal(err)
	}
	go func() { r, e := Run(f.ctx, f.workspace, p.ID, f.config); done <- outcome{r, e} }()
	// The oldest remaining READY worker wins; a disconnect never chooses a new
	// owner. Recovery and deterministic scheduler selection issue a new attempt.
	w, err := workers[1].ReceiveWork(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if w.Correlation.Generation <= assignments[0].Correlation.Generation || w.Assignment.Worktree == assignments[0].Assignment.Worktree {
		t.Fatal("recovery reused authority/isolation")
	}
	if err := reportWorker(workers[1], f, w, agentprotocol.WorkerAcceptance, ""); err != nil {
		t.Fatal(err)
	}
	if err := writeWorkerResult(w); err != nil {
		t.Fatal(err)
	}
	if err := reportWorker(workers[1], f, w, agentprotocol.WorkerResult, "all recovered task writers stopped"); err != nil {
		t.Fatal(err)
	}
	final := receive(t, f.ctx, done)
	if final.err != nil || final.report.Status != "AWAITING_REVIEW" {
		t.Fatalf("final report: %+v %v", final.report, final.err)
	}
	view := query()
	if view.System.CompletedTasks != 3 || view.System.ActiveAssignments != 0 || view.System.BlockedTasks != 0 || view.Objectives[0].ReviewStatus != "AWAITING_REVIEW" {
		t.Fatal("final status lost authoritative review state")
	}
	var inspected bytes.Buffer
	if err := operations.Run(f.ctx, []string{"inspect-task", "--workspace", f.workspace, "--objective", p.ID, "--task", assignments[0].Correlation.TaskID, "--json"}, &inspected); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(inspected.String(), "assignment_history") {
		t.Fatal("recovery history missing from task inspection")
	}
	s, err := scheduler.Read(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range s.Tasks {
		if task.Token != "" && strings.Contains(inspected.String(), task.Token) {
			t.Fatal("private claim leaked")
		}
	}
}

func reportWorker(c *agentattachment.Client, f *workerRuntimeFixture, w agentprotocol.WorkerMessage, kind agentprotocol.MessageType, detail string) error {
	return report(c, f.ctx, w, kind, detail)
}
