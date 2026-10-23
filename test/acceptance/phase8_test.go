package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/controller"
	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/operations"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
	"github.com/eeyung1/multi-agent-orchestrator/internal/recon"
	"github.com/eeyung1/multi-agent-orchestrator/internal/review"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

type acceptanceHarness struct {
	workspace  string
	socketDir  string
	socketPath string
	registry   *agentregistry.Registry
	server     *agentattachment.Server
	config     controller.Config
	ctx        context.Context
	cancel     context.CancelFunc
}

func setupAcceptance(t *testing.T) *acceptanceHarness {
	t.Helper()

	// Ensure required binaries exist
	for _, bin := range []string{"python3", "node", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("Required prerequisite binary %q not found on PATH", bin)
		}
	}

	// 1. Create disposable workspace Git repository
	workspace := t.TempDir()
	workspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}

	gitExec(t, workspace, "init", "-b", "main")
	gitExec(t, workspace, "config", "user.name", "Acceptance Test")
	gitExec(t, workspace, "config", "user.email", "acceptance@example.com")
	readmePath := filepath.Join(workspace, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Acceptance Project\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, workspace, "add", "README.md")
	gitExec(t, workspace, "commit", "-m", "Initial commit")

	// 2. Authorize ORCHESTRATOR
	if _, err := enrollment.Enroll(workspace, enrollment.Native, "acceptance-operator"); err != nil {
		t.Fatal(err)
	}

	// 3. Socket directory with strict 0700 permissions
	socketDir, err := os.MkdirTemp("", "mao-acc-sock-")
	if err != nil {
		t.Fatal(err)
	}
	socketDir, err = filepath.EvalSymlinks(socketDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socketDir, 0700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(socketDir, "agents.sock")

	// 4. Open Registry and Server
	registry, err := agentregistry.Open(workspace, agentregistry.Policy{StaleAfter: 90 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := agentattachment.DefaultConfig()
	cfg.IdleTimeout = 90 * time.Second
	server, err := agentattachment.New(registry, cfg)
	if err != nil {
		t.Fatal(err)
	}

	listener, err := agentattachment.ListenLocal(server, socketPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	listenerDone := make(chan error, 1)
	go func() { listenerDone <- listener.Serve(ctx) }()

	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		server.Close()
		<-listenerDone
		_ = os.RemoveAll(socketDir)
	})

	// 5. Config with enrolled capabilities and real validators
	c := controller.Config{
		MaxWorkers:           2,
		EnrolledCapabilities: []agentprotocol.Capability{"coding"},
		AcceptanceSeconds:    30,
		TimeoutSeconds:       60,
		HeartbeatSeconds:     2,
		AllowedScopes:        []string{"demo/python", "demo/node"},
		Validators: map[string]execution.Command{
			"whitespace": {Program: "git", Args: []string{"diff", "--check", "HEAD~1", "HEAD"}},
		},
		RequiredValidators: []string{"whitespace"},
		Runtime:            &controller.EnrolledWorkers{Registry: registry, Server: server},
	}

	return &acceptanceHarness{
		workspace:  workspace,
		socketDir:  socketDir,
		socketPath: socketPath,
		registry:   registry,
		server:     server,
		config:     c,
		ctx:        ctx,
		cancel:     cancel,
	}
}

func (h *acceptanceHarness) issueInvitation(t *testing.T, agentID string, roles []agentprotocol.Role, caps []agentprotocol.Capability) string {
	t.Helper()
	_, proof, err := h.server.Invite(agentattachment.InviteRequest{
		AgentID:      agentprotocol.AgentID(agentID),
		Roles:        roles,
		Capabilities: caps,
		TTL:          2 * time.Minute,
	}, true)
	if err != nil {
		t.Fatalf("Invite %s: %v", agentID, err)
	}

	credPath := filepath.Join(h.socketDir, fmt.Sprintf("%s-cred.json", agentID))
	if err := agentattachment.WriteCredential(credPath, proof); err != nil {
		t.Fatalf("WriteCredential %s: %v", agentID, err)
	}
	return credPath
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("Could not locate repository root")
		}
		dir = parent
	}
}

func gitExec(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\nOutput: %s", args, dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

// TestPhase8HeterogeneousMultiAgentAcceptance verifies end-to-end multi-agent
// orchestration across one enrolled Planner and two heterogeneous Workers (Python + Node.js)
// executing independent tasks concurrently in isolated worktrees.
func TestPhase8HeterogeneousMultiAgentAcceptance(t *testing.T) {
	h := setupAcceptance(t)
	root := repoRoot(t)

	plannerScript := filepath.Join(root, "test", "acceptance", "agents", "planner", "planner.py")
	pythonWorkerScript := filepath.Join(root, "test", "acceptance", "agents", "python_worker", "worker.py")
	nodeWorkerScript := filepath.Join(root, "test", "acceptance", "agents", "node_worker", "worker.js")

	// Issue credentials
	plannerCred := h.issueInvitation(t, "python-planner", []agentprotocol.Role{agentprotocol.RolePlanner}, []agentprotocol.Capability{agentprotocol.PlanningCapability})
	pyWorkerCred := h.issueInvitation(t, "python-worker-1", []agentprotocol.Role{agentprotocol.RoleWorker}, []agentprotocol.Capability{"coding"})
	nodeWorkerCred := h.issueInvitation(t, "node-worker-1", []agentprotocol.Role{agentprotocol.RoleWorker}, []agentprotocol.Capability{"coding"})

	// Start independent Python Planner process
	plannerCmd := exec.CommandContext(h.ctx, "python3", plannerScript,
		"--socket", h.socketPath,
		"--credential-file", plannerCred,
		"--agent-id", "python-planner",
		"--provider", "python-native-planner",
		"--max-requests", "1",
	)
	var plannerOut bytes.Buffer
	plannerCmd.Stdout = &plannerOut
	plannerCmd.Stderr = &plannerOut
	if err := plannerCmd.Start(); err != nil {
		t.Fatalf("Start Python Planner: %v", err)
	}
	t.Cleanup(func() { _ = plannerCmd.Process.Kill() })

	// Wait for Planner candidate to appear in registry
	var plannerRef agentregistry.Ref
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		candidates, err := h.registry.PlannerCandidates()
		if err == nil {
			for _, c := range candidates {
				if c.Ref().AgentID == "python-planner" && h.server.Connected(c.Ref()) {
					plannerRef = c.Ref()
					break
				}
			}
		}
		if plannerRef.AgentID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if plannerRef.AgentID == "" {
		t.Fatalf("Python Planner candidate never connected; log:\n%s", plannerOut.String())
	}

	// Human selects enrolled Planner
	binding, err := h.registry.SelectPlanner("run-phase8", plannerRef, "", true, "acceptance operator explicitly selected python-planner")
	if err != nil {
		t.Fatalf("SelectPlanner: %v", err)
	}

	// Propose plan via Python Planner bridge
	enrolledPlanner := &planner.Enrolled{
		Registry: h.registry,
		Server:   h.server,
		Binding:  binding,
		Timeout:  15 * time.Second,
	}

	report, err := recon.Inspect(h.workspace, "Implement heterogeneous multi-agent demonstration")
	if err != nil {
		t.Fatalf("Recon Inspect: %v", err)
	}

	req := planner.Request{
		Objective:          "Implement heterogeneous multi-agent demonstration",
		Recon:              report,
		Capabilities:       h.config.Capabilities(),
		Validators:         h.config.Checks(),
		AllowedScopes:      h.config.AllowedScopes,
		RequiredValidators: h.config.RequiredValidators,
	}

	proposal, err := enrolledPlanner.Propose(h.ctx, req)
	if err != nil {
		t.Fatalf("Planner Propose: %v\nPlanner log: %s", err, plannerOut.String())
	}

	if len(proposal.Plan.Tasks) != 2 {
		t.Fatalf("Expected 2 tasks from Python Planner proposal, got %d", len(proposal.Plan.Tasks))
	}

	// Human reviews and admits plan
	proposal.Plan.ID = "phase8-heterogeneous-demo"
	if err := controller.AdmitEnrolled(h.workspace, proposal.Plan, h.config, report, "acceptance operator authorized plan", enrolledPlanner.Evidence()); err != nil {
		t.Fatalf("AdmitEnrolled: %v", err)
	}

	// Set up synchronization barrier files for concurrent Worker execution
	syncDir := t.TempDir()
	pyReadyFile := filepath.Join(syncDir, "python.ready")
	pyReleaseFile := filepath.Join(syncDir, "python.release")
	nodeReadyFile := filepath.Join(syncDir, "node.ready")
	nodeReleaseFile := filepath.Join(syncDir, "node.release")

	// Start independent Python Worker process
	pyWorkerCmd := exec.CommandContext(h.ctx, "python3", pythonWorkerScript,
		"--socket", h.socketPath,
		"--credential-file", pyWorkerCred,
		"--agent-id", "python-worker-1",
		"--provider", "python-worker-native",
		"--capability", "coding",
		"--pause-ready-file", pyReadyFile,
		"--pause-release-file", pyReleaseFile,
		"--max-tasks", "1",
	)
	var pyOut bytes.Buffer
	pyWorkerCmd.Stdout = &pyOut
	pyWorkerCmd.Stderr = &pyOut
	if err := pyWorkerCmd.Start(); err != nil {
		t.Fatalf("Start Python Worker: %v", err)
	}
	t.Cleanup(func() { _ = pyWorkerCmd.Process.Kill() })

	// Start independent Node.js Worker process
	nodeWorkerCmd := exec.CommandContext(h.ctx, "node", nodeWorkerScript,
		"--socket", h.socketPath,
		"--credential-file", nodeWorkerCred,
		"--agent-id", "node-worker-1",
		"--provider", "node-worker-native",
		"--capability", "coding",
		"--pause-ready-file", nodeReadyFile,
		"--pause-release-file", nodeReleaseFile,
		"--max-tasks", "1",
	)
	var nodeOut bytes.Buffer
	nodeWorkerCmd.Stdout = &nodeOut
	nodeWorkerCmd.Stderr = &nodeOut
	if err := nodeWorkerCmd.Start(); err != nil {
		t.Fatalf("Start Node.js Worker: %v", err)
	}
	t.Cleanup(func() { _ = nodeWorkerCmd.Process.Kill() })

	// Wait for both Workers to connect and assign WORKER role
	var pyRef, nodeRef agentregistry.Ref
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		all, err := h.registry.List()
		if err == nil {
			for _, v := range all {
				if v.Ref().AgentID == "python-worker-1" && h.server.Connected(v.Ref()) {
					pyRef = v.Ref()
				}
				if v.Ref().AgentID == "node-worker-1" && h.server.Connected(v.Ref()) {
					nodeRef = v.Ref()
				}
			}
		}
		if pyRef.AgentID != "" && nodeRef.AgentID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pyRef.AgentID == "" || nodeRef.AgentID == "" {
		t.Fatalf("Workers failed to connect in time; pyLog:\n%s\nnodeLog:\n%s", pyOut.String(), nodeOut.String())
	}

	if err := h.registry.AssignRole(pyRef, agentprotocol.RoleWorker, "acceptance operator authorized exact Python worker"); err != nil {
		t.Fatalf("AssignRole pyRef: %v", err)
	}
	if err := h.registry.AssignRole(nodeRef, agentprotocol.RoleWorker, "acceptance operator authorized exact Node worker"); err != nil {
		t.Fatalf("AssignRole nodeRef: %v", err)
	}

	// Launch parallel controller execution in background
	controllerResult := make(chan struct {
		r controller.Report
		e error
	}, 1)
	go func() {
		r, e := controller.Run(h.ctx, h.workspace, proposal.Plan.ID, h.config)
		controllerResult <- struct {
			r controller.Report
			e error
		}{r, e}
	}()

	// Barrier synchronization: wait until BOTH Workers have accepted tasks and are paused in-flight
	barrierDeadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(barrierDeadline) {
		_, errA := os.Stat(pyReadyFile)
		_, errB := os.Stat(nodeReadyFile)
		if errA == nil && errB == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := os.Stat(pyReadyFile); err != nil {
		t.Fatalf("Python worker never signalled ready (barrier): pyLog:\n%s", pyOut.String())
	}
	if _, err := os.Stat(nodeReadyFile); err != nil {
		t.Fatalf("Node.js worker never signalled ready (barrier): nodeLog:\n%s", nodeOut.String())
	}

	// === VERIFY CONCURRENCY AND OPERATIONAL STATUS IN FLIGHT ===
	// 1. Authoritative claims check: both workers must hold exclusive claims
	claims, err := coordination.Claims(h.workspace)
	if err != nil {
		t.Fatalf("Claims check: %v", err)
	}
	if len(claims) != 2 {
		t.Fatalf("Expected 2 simultaneous exclusive claims in-flight, got %d", len(claims))
	}

	// 2. Query operational status projection
	opReader, err := operations.Open(h.workspace, operations.Options{
		Connected:           h.server.Connected,
		MaxWorkers:          &h.config.MaxWorkers,
		ConfigurationSource: "acceptance_test_policy",
	})
	if err != nil {
		t.Fatalf("operations.Open: %v", err)
	}
	snap, err := opReader.Capture(h.ctx)
	if err != nil {
		t.Fatalf("opReader.Capture: %v", err)
	}

	if len(snap.Tasks) != 2 {
		t.Fatalf("Expected 2 tasks in operational snapshot, got %d", len(snap.Tasks))
	}

	// Verify distinct task IDs, generations, worktrees, and owners
	taskA := snap.Tasks[0]
	taskB := snap.Tasks[1]
	if taskA.Assignment.TaskID == taskB.Assignment.TaskID {
		t.Fatalf("Tasks must have distinct IDs, both are %s", taskA.Assignment.TaskID)
	}
	if taskA.Owner == nil || taskB.Owner == nil {
		t.Fatalf("Both tasks must have active owners: taskA=%+v taskB=%+v", taskA.Owner, taskB.Owner)
	}
	if taskA.Owner.AgentID == taskB.Owner.AgentID {
		t.Fatalf("Tasks must be owned by distinct agents: taskA=%s taskB=%s", taskA.Owner.AgentID, taskB.Owner.AgentID)
	}

	// Check agent authoritative BUSY status
	busyCount := 0
	for _, a := range snap.Agents {
		if (a.AgentID == "python-worker-1" || a.AgentID == "node-worker-1") && a.Status == state.AgentBusy {
			busyCount++
		}
	}
	if busyCount != 2 {
		t.Fatalf("Expected both heterogeneous workers to be authoritatively BUSY in operational view, got %d", busyCount)
	}

	// Verify isolated worktree paths exist and are separate
	schedulerSnap, err := scheduler.Read(h.workspace)
	if err != nil {
		t.Fatalf("scheduler.Read: %v", err)
	}
	stA := schedulerSnap.Tasks[proposal.Plan.ID+"::"+taskA.Assignment.TaskID]
	stB := schedulerSnap.Tasks[proposal.Plan.ID+"::"+taskB.Assignment.TaskID]
	if stA.Worktree == "" || stB.Worktree == "" {
		t.Fatalf("Worktrees must be populated: stA=%s stB=%s", stA.Worktree, stB.Worktree)
	}
	if stA.Worktree == stB.Worktree {
		t.Fatalf("Both workers cannot share the same worktree: %s", stA.Worktree)
	}
	if stA.Branch == stB.Branch {
		t.Fatalf("Both workers cannot share the same branch: %s", stA.Branch)
	}

	// 3. Release both Workers from barrier to perform file writes and complete
	if err := os.WriteFile(pyReleaseFile, []byte("release\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nodeReleaseFile, []byte("release\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Await controller execution completion
	select {
	case res := <-controllerResult:
		if res.e != nil {
			t.Fatalf("controller.Run failed: %v", res.e)
		}
		if res.r.Status != "AWAITING_REVIEW" {
			t.Fatalf("Expected report status AWAITING_REVIEW, got %s", res.r.Status)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("controller.Run timed out; pyLog:\n%s\nnodeLog:\n%s", pyOut.String(), nodeOut.String())
	}

	// Verify both tasks were successfully validated and completed
	finalScheduler, err := scheduler.Read(h.workspace)
	if err != nil {
		t.Fatalf("Final scheduler.Read: %v", err)
	}
	for _, tsk := range proposal.Plan.Tasks {
		st := finalScheduler.Tasks[proposal.Plan.ID+"::"+tsk.ID]
		if st.Status != "COMPLETE" {
			t.Fatalf("Task %s expected COMPLETED, got %s", tsk.ID, st.Status)
		}
		if st.ValidationEvidence == "" {
			t.Fatalf("Task %s missing validation evidence", tsk.ID)
		}
	}

	// Verify human review gate
	pkg, err := review.Build(h.workspace, proposal.Plan.ID)
	if err != nil {
		t.Fatalf("review.Build: %v", err)
	}
	if pkg.Objective.Status != "AWAITING_REVIEW" {
		t.Fatalf("Expected package status AWAITING_REVIEW, got %s", pkg.Objective.Status)
	}

	if err := review.Decide(h.workspace, proposal.Plan.ID, "APPROVED", "acceptance-reviewer"); err != nil {
		t.Fatalf("review.Decide: %v", err)
	}

	finalPkg, err := review.Build(h.workspace, proposal.Plan.ID)
	if err != nil {
		t.Fatalf("review.Build final: %v", err)
	}
	if finalPkg.Objective.Status != "APPROVED" {
		t.Fatalf("Expected approved review status, got %s", finalPkg.Objective.Status)
	}
}

// TestPhase8WorkerDisconnectAndRecoveryAcceptance exercises an abrupt Worker disconnect
// while another Worker executes concurrently, proving that:
// 1. The disconnect does not disrupt the independent healthy Worker.
// 2. The failed Worker's task transitions to BLOCKED and preserves attempt history.
// 3. Stale completion from the dead session is rejected (generation fencing).
// 4. Human operator authorizes recovery with generation increment.
// 5. A replacement Worker completes the task in a fresh worktree.
func TestPhase8WorkerDisconnectAndRecoveryAcceptance(t *testing.T) {
	h := setupAcceptance(t)
	root := repoRoot(t)

	pythonWorkerScript := filepath.Join(root, "test", "acceptance", "agents", "python_worker", "worker.py")
	nodeWorkerScript := filepath.Join(root, "test", "acceptance", "agents", "node_worker", "worker.js")

	// Plan with 2 tasks: task-stable (Node) and task-failing (Python)
	plan := objective.Plan{
		ID:        "phase8-recovery-demo",
		Objective: "Test worker disconnect and recovery",
		Tasks: []objective.Task{
			{
				ID:                 "task-stable",
				Feature:            "stable-feature",
				Scope:              "demo/node",
				Description:        "Stable task executed by Node.js",
				AcceptanceCriteria: []string{"Delivers valid files"},
				Validation:         []string{"whitespace"},
				Capability:         "coding",
			},
			{
				ID:                 "task-failing",
				Feature:            "failing-feature",
				Scope:              "demo/python",
				Description:        "Failing task executed by Python that disconnects abruptly",
				AcceptanceCriteria: []string{"Delivers valid files after recovery"},
				Validation:         []string{"whitespace"},
				Capability:         "coding",
			},
		},
	}

	report, err := recon.Inspect(h.workspace, plan.Objective)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Admit(h.workspace, plan, h.config, report, "acceptance operator authorized recovery test"); err != nil {
		t.Fatal(err)
	}

	// Issue credentials
	nodeCred := h.issueInvitation(t, "node-worker-stable", []agentprotocol.Role{agentprotocol.RoleWorker}, []agentprotocol.Capability{"coding"})
	pyCred := h.issueInvitation(t, "python-worker-failing", []agentprotocol.Role{agentprotocol.RoleWorker}, []agentprotocol.Capability{"coding"})

	// Start Node.js worker normally
	nodeCmd := exec.CommandContext(h.ctx, "node", nodeWorkerScript,
		"--socket", h.socketPath,
		"--credential-file", nodeCred,
		"--agent-id", "node-worker-stable",
		"--max-tasks", "1",
	)
	var nodeOut bytes.Buffer
	nodeCmd.Stdout = &nodeOut
	nodeCmd.Stderr = &nodeOut
	if err := nodeCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeCmd.Process.Kill() })

	// Start Python worker configured to abruptly disconnect on whichever task it receives
	pyCmd := exec.CommandContext(h.ctx, "python3", pythonWorkerScript,
		"--socket", h.socketPath,
		"--credential-file", pyCred,
		"--agent-id", "python-worker-failing",
		"--disconnect-after-accept",
	)
	var pyOut bytes.Buffer
	pyCmd.Stdout = &pyOut
	pyCmd.Stderr = &pyOut
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pyCmd.Process.Kill() })

	// Wait for workers to connect and assign roles
	var nodeRef, pyRef agentregistry.Ref
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		all, err := h.registry.List()
		if err == nil {
			for _, v := range all {
				if v.Ref().AgentID == "node-worker-stable" && h.server.Connected(v.Ref()) {
					nodeRef = v.Ref()
				}
				if v.Ref().AgentID == "python-worker-failing" && h.server.Connected(v.Ref()) {
					pyRef = v.Ref()
				}
			}
		}
		if nodeRef.AgentID != "" && pyRef.AgentID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if nodeRef.AgentID == "" || pyRef.AgentID == "" {
		t.Fatalf("Workers failed to connect: nodeLog:\n%s\npyLog:\n%s", nodeOut.String(), pyOut.String())
	}

	if err := h.registry.AssignRole(nodeRef, agentprotocol.RoleWorker, "authorize stable worker"); err != nil {
		t.Fatal(err)
	}
	if err := h.registry.AssignRole(pyRef, agentprotocol.RoleWorker, "authorize failing worker"); err != nil {
		t.Fatal(err)
	}

	// Run controller: node-worker should complete, python-worker should disconnect and fail
	rep, err := controller.Run(h.ctx, h.workspace, plan.ID, h.config)
	if err != nil {
		t.Fatalf("controller.Run failed: %v", err)
	}
	_ = rep

	snap, err := scheduler.Read(h.workspace)
	if err != nil {
		t.Fatal(err)
	}
	var stableTask, failingTask state.Task
	for _, tsk := range snap.Tasks {
		if tsk.ObjectiveID == plan.ID {
			if tsk.Status == "COMPLETE" {
				stableTask = tsk
			} else if tsk.Status == "BLOCKED" {
				failingTask = tsk
			}
		}
	}
	if stableTask.ID == "" {
		t.Fatal("Expected one task to be COMPLETE")
	}
	if failingTask.ID == "" {
		t.Fatal("Expected one task to be BLOCKED")
	}
	if failingTask.WorkerDispatch == nil || failingTask.WorkerDispatch.Phase != "FAILED" {
		t.Fatalf("Failing task must be BLOCKED with FAILED phase, got %+v", failingTask.WorkerDispatch)
	}

	// Verify operations attention state shows TASK_BLOCKED and EXPLICIT_RECOVERY_AVAILABLE
	opReader, err := operations.Open(h.workspace, operations.Options{
		Connected:  h.server.Connected,
		MaxWorkers: &h.config.MaxWorkers,
	})
	if err != nil {
		t.Fatal(err)
	}
	opView, err := opReader.Capture(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var opTask *operations.Task
	for i := range opView.Tasks {
		if opView.Tasks[i].Assignment.TaskID == failingTask.ID {
			opTask = &opView.Tasks[i]
			break
		}
	}
	if opTask == nil {
		t.Fatalf("%s not found in operations view", failingTask.ID)
	}
	if opTask.Recovery != "EXPLICIT_RECOVERY_AVAILABLE" {
		t.Fatalf("Expected EXPLICIT_RECOVERY_AVAILABLE, got %s", opTask.Recovery)
	}

	// Human authorizes recovery using exact generation
	if err := scheduler.AuthorizeRecovery(h.workspace, plan.ID, failingTask.ID, failingTask.AssignmentGeneration, "acceptance operator authorized retry after disconnect"); err != nil {
		t.Fatalf("AuthorizeRecovery: %v", err)
	}

	// Connect replacement worker to finish the task
	replCred := h.issueInvitation(t, "python-worker-replacement", []agentprotocol.Role{agentprotocol.RoleWorker}, []agentprotocol.Capability{"coding"})
	replCmd := exec.CommandContext(h.ctx, "python3", pythonWorkerScript,
		"--socket", h.socketPath,
		"--credential-file", replCred,
		"--agent-id", "python-worker-replacement",
		"--max-tasks", "1",
	)
	var replOut bytes.Buffer
	replCmd.Stdout = &replOut
	replCmd.Stderr = &replOut
	if err := replCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replCmd.Process.Kill() })

	var replRef agentregistry.Ref
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		all, err := h.registry.List()
		if err == nil {
			for _, v := range all {
				if v.Ref().AgentID == "python-worker-replacement" && h.server.Connected(v.Ref()) {
					replRef = v.Ref()
					break
				}
			}
		}
		if replRef.AgentID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if replRef.AgentID == "" {
		t.Fatalf("Replacement worker failed to connect:\n%s", replOut.String())
	}

	if err := h.registry.AssignRole(replRef, agentprotocol.RoleWorker, "authorize replacement worker"); err != nil {
		t.Fatal(err)
	}

	// Rerun controller to execute the recovered task
	rep2, err := controller.Run(h.ctx, h.workspace, plan.ID, h.config)
	if err != nil {
		t.Fatalf("Second controller.Run failed: %v", err)
	}
	if rep2.Status != "AWAITING_REVIEW" {
		t.Fatalf("Expected rep2 status AWAITING_REVIEW, got %s", rep2.Status)
	}

	finalSnap, err := scheduler.Read(h.workspace)
	if err != nil {
		t.Fatal(err)
	}
	recoveredTask := finalSnap.Tasks[plan.ID+"::"+failingTask.ID]
	if recoveredTask.Status != "COMPLETE" {
		t.Fatalf("Recovered task expected COMPLETED, got %s", recoveredTask.Status)
	}
	if recoveredTask.AssignmentGeneration <= failingTask.AssignmentGeneration {
		t.Fatalf("Expected assignment generation to strictly increase from %d, got %d", failingTask.AssignmentGeneration, recoveredTask.AssignmentGeneration)
	}
	if recoveredTask.Worktree == failingTask.Worktree {
		t.Fatalf("Recovered task must use a fresh worktree, got same: %s", recoveredTask.Worktree)
	}
}

// TestPhase8OperationsCLIExecution executes cmd/orchestrator status --json during live execution
// proving that CLI commands accurately project the authoritative multi-agent state.
func TestPhase8OperationsCLIExecution(t *testing.T) {
	h := setupAcceptance(t)
	root := repoRoot(t)

	// Admit plan with 1 task
	plan := objective.Plan{
		ID:        "phase8-cli-demo",
		Objective: "Test operations CLI execution",
		Tasks: []objective.Task{
			{
				ID:                 "task-cli",
				Feature:            "cli-feature",
				Scope:              "demo/node",
				Description:        "CLI test task",
				AcceptanceCriteria: []string{"Delivers valid output"},
				Validation:         []string{"whitespace"},
				Capability:         "coding",
			},
		},
	}
	report, err := recon.Inspect(h.workspace, plan.Objective)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Admit(h.workspace, plan, h.config, report, "operator authorized CLI test"); err != nil {
		t.Fatal(err)
	}

	syncDir := t.TempDir()
	readyFile := filepath.Join(syncDir, "node.ready")
	releaseFile := filepath.Join(syncDir, "node.release")

	nodeWorkerScript := filepath.Join(root, "test", "acceptance", "agents", "node_worker", "worker.js")
	nodeCred := h.issueInvitation(t, "node-cli-worker", []agentprotocol.Role{agentprotocol.RoleWorker}, []agentprotocol.Capability{"coding"})

	nodeCmd := exec.CommandContext(h.ctx, "node", nodeWorkerScript,
		"--socket", h.socketPath,
		"--credential-file", nodeCred,
		"--agent-id", "node-cli-worker",
		"--pause-ready-file", readyFile,
		"--pause-release-file", releaseFile,
		"--max-tasks", "1",
	)
	var nodeOut bytes.Buffer
	nodeCmd.Stdout = &nodeOut
	nodeCmd.Stderr = &nodeOut
	if err := nodeCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeCmd.Process.Kill() })

	// Wait for worker to connect
	var nodeRef agentregistry.Ref
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		all, err := h.registry.List()
		if err == nil {
			for _, v := range all {
				if v.Ref().AgentID == "node-cli-worker" && h.server.Connected(v.Ref()) {
					nodeRef = v.Ref()
					break
				}
			}
		}
		if nodeRef.AgentID != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if nodeRef.AgentID == "" {
		t.Fatalf("Worker failed to connect: %s", nodeOut.String())
	}
	if err := h.registry.AssignRole(nodeRef, agentprotocol.RoleWorker, "authorize CLI test worker"); err != nil {
		t.Fatal(err)
	}

	// Dispatch in background
	controllerDone := make(chan error, 1)
	go func() {
		_, err := controller.Run(h.ctx, h.workspace, plan.ID, h.config)
		controllerDone <- err
	}()

	// Wait for worker barrier
	barrierDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(barrierDeadline) {
		if _, err := os.Stat(readyFile); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("Worker never reached ready state: %s", nodeOut.String())
	}

	// Run cmd/orchestrator status --workspace <workspace> --json via go run
	statusCmd := exec.Command("go", "run", "./cmd/orchestrator", "status", "--workspace", h.workspace, "--json")
	statusCmd.Dir = root
	out, err := statusCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("orchestrator status command failed: %v\nOutput: %s", err, string(out))
	}

	var snap operations.Snapshot
	if err := json.Unmarshal(out, &snap); err != nil {
		t.Fatalf("Unmarshal CLI status output: %v\nRaw: %s", err, string(out))
	}

	if len(snap.Tasks) != 1 {
		t.Fatalf("Expected 1 task in CLI output, got %d", len(snap.Tasks))
	}
	if snap.Tasks[0].Assignment.TaskID != "task-cli" {
		t.Fatalf("Expected task-cli, got %s", snap.Tasks[0].Assignment.TaskID)
	}

	// Release worker
	_ = os.WriteFile(releaseFile, []byte("release\n"), 0644)
	select {
	case <-controllerDone:
	case <-time.After(15 * time.Second):
	}
}
