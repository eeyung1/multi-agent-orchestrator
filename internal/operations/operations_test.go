package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/review"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

type fixture struct {
	root  string
	r     *agentregistry.Registry
	store *state.Store
	path  string
}

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = root
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}
func setup(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-b", "main")
	git(t, root, "config", "user.email", "test@example.com")
	git(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "seed"), []byte("seed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "seed")
	git(t, root, "commit", "-m", "Seed")
	if _, err := enrollment.Enroll(root, enrollment.Native, "test human"); err != nil {
		t.Fatal(err)
	}
	r, err := agentregistry.Open(root, agentregistry.Policy{StaleAfter: 10 * time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := repository.Identity(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(root, id)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{root, r, store, filepath.Join(root, "multi-agent-orchestrator", "state.json")}
}
func (f *fixture) agent(t *testing.T, agent, session string, role agentprotocol.Role) state.AgentRef {
	t.Helper()
	caps := []agentprotocol.Capability{"coding"}
	if role == agentprotocol.RolePlanner {
		caps = []agentprotocol.Capability{"planning"}
	}
	v, err := f.r.Enroll(agentregistry.Enrollment{Identity: agentprotocol.Identity{ProtocolVersion: agentprotocol.Version4, AgentID: agentprotocol.AgentID(agent), SessionID: agentprotocol.SessionID(session), Metadata: agentprotocol.Metadata{Provider: "future-" + agent, Runtime: "runtime-" + session}, Capabilities: caps, SupportedRoles: []agentprotocol.Role{role}}, Decision: "test human admission"})
	if err != nil {
		t.Fatal(err)
	}
	if role == agentprotocol.RoleWorker {
		if err := f.r.AssignRole(v.Ref(), role, "test human worker authorization"); err != nil {
			t.Fatal(err)
		}
	}
	return v.Ref()
}
func (f *fixture) acquire(t *testing.T, refs ...state.AgentRef) (objective.Plan, []scheduler.Result) {
	t.Helper()
	p := objective.Plan{ID: "objective", Objective: "independent tasks"}
	for i := range refs {
		p.Tasks = append(p.Tasks, objective.Task{ID: fmt.Sprintf("task-%d", i), Feature: fmt.Sprintf("feature-%d", i), Scope: fmt.Sprintf("owned-%d", i), Description: "bounded task", Capability: "coding", Validation: []string{"check"}})
	}
	if len(refs) == 0 {
		p.Tasks = []objective.Task{{ID: "managed", Feature: "managed", Scope: "managed", Description: "managed task", Capability: "coding", Validation: []string{"check"}}}
	}
	if err := scheduler.Register(f.root, p, state.Objective{AuthorizedBy: "test human", PlanDigest: scheduler.PlanDigest(p)}); err != nil {
		t.Fatal(err)
	}
	var out []scheduler.Result
	for i, task := range p.Tasks {
		req := scheduler.Request{Workspace: f.root, Agent: "worker", Role: "coding", Base: "main", Plan: p, TaskID: task.ID, MaxWorkers: 3}
		if len(refs) > 0 {
			req.Worker = &refs[i]
			req.Registry = f.r
		}
		a, err := scheduler.Acquire(req)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return p, out
}
func (f *fixture) accept(t *testing.T, a scheduler.Result) {
	t.Helper()
	if err := scheduler.MarkWorkerSent(f.root, "objective", a); err != nil {
		t.Fatal(err)
	}
	yes := true
	f.event(t, a, agentprotocol.WorkerAcceptance, &yes, "")
}
func (f *fixture) event(t *testing.T, a scheduler.Result, kind agentprotocol.MessageType, accepted *bool, detail string) {
	t.Helper()
	d := a.WorkerDispatch
	err := scheduler.WorkerEvent(f.root, state.AgentRef{AgentID: d.AgentID, SessionID: d.SessionID, Generation: d.Incarnation}, agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version4, AgentID: d.AgentID, SessionID: d.SessionID, Type: kind, Work: &agentprotocol.WorkerMessage{Correlation: d.Correlation, Accepted: accepted, Detail: detail}})
	if err != nil {
		t.Fatal(err)
	}
}
func capture(t *testing.T, root string, opt Options) Snapshot {
	t.Helper()
	r, err := Open(root, opt)
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func code(v Snapshot, want string) bool {
	for _, a := range v.Attention {
		if a.Code == want {
			return true
		}
	}
	return false
}
func data(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEmptyStatusNeverInitializesState(t *testing.T) {
	for _, withGit := range []bool{false, true} {
		t.Run(fmt.Sprint(withGit), func(t *testing.T) {
			root := t.TempDir()
			if withGit {
				git(t, root, "init", "-b", "main")
			}
			var out bytes.Buffer
			if err := Run(context.Background(), []string{"status", "--workspace", root, "--json"}, &out); err != nil {
				t.Fatal(err)
			}
			var v Snapshot
			if json.Unmarshal(out.Bytes(), &v) != nil || v.Version != Version || v.Initialized || !code(v, "PROJECT_NOT_INITIALIZED") {
				t.Fatalf("empty view: %s", out.String())
			}
			if _, err := os.Stat(filepath.Join(root, "multi-agent-orchestrator")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("status created authority")
			}
		})
	}
}

func TestThreeAssignmentsPlannerStatusAndInspection(t *testing.T) {
	f := setup(t)
	planner := f.agent(t, "planner", "planning-session", agentprotocol.RolePlanner)
	b, err := f.r.SelectPlanner("run", planner, "", true, "human selected exact Planner")
	if err != nil {
		t.Fatal(err)
	}
	request, err := f.r.BeginPlannerRequest(b.RunID, b.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	refs := []state.AgentRef{f.agent(t, "future-c", "session-c", agentprotocol.RoleWorker), f.agent(t, "future-a", "session-a", agentprotocol.RoleWorker), f.agent(t, "future-b", "session-b", agentprotocol.RoleWorker)}
	_, assignments := f.acquire(t, refs...)
	for _, a := range assignments {
		f.accept(t, a)
	}
	f.event(t, assignments[1], agentprotocol.WorkerProgress, nil, "bounded implementation in progress")
	before := data(t, f.path)
	ledger := data(t, filepath.Join(f.root, "multi-agent-orchestrator", "agentmeeting.md"))
	now := time.Now().UTC()
	max := 3
	opt := Options{Now: func() time.Time { return now }, Connected: func(state.AgentRef) bool { return true }, MaxWorkers: &max, ConfigurationSource: "test_host"}
	v := capture(t, f.root, opt)
	if len(v.Agents) != 4 || len(v.Planners) != 1 || v.Planners[0].BindingID != b.BindingID || v.Planners[0].RequestID != request.RequestID || v.Planners[0].RequestStatus != "PENDING" || v.System.ActiveAssignments != 3 || len(v.Tasks) != 3 {
		t.Fatalf("projection: %+v", v)
	}
	seen := map[string]bool{}
	for _, task := range v.Tasks {
		if task.Owner == nil || task.Owner.Mode != "enrolled" || task.Assignment.Generation == 0 || task.DispatchPhase != "ACCEPTED" || task.Claim != "HELD" || task.Branch == "" || task.Worktree == "" {
			t.Fatalf("assignment: %+v", task)
		}
		seen[task.Worktree] = true
		matched := false
		for _, a := range v.Agents {
			if a.SessionID == task.Owner.SessionID {
				matched = a.Status == state.AgentBusy && a.Incarnation == task.Owner.Incarnation && len(a.Assignments) == 1 && a.Assignments[0] == task.Assignment
			}
		}
		if !matched {
			t.Fatal("torn agent/task binding")
		}
	}
	if len(seen) != 3 || v.Tasks[1].Progress != "bounded implementation in progress" {
		t.Fatal("lost isolation/progress")
	}
	var human, encoded bytes.Buffer
	if err := WriteHuman(&human, v); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "3 assigned") || !strings.Contains(human.String(), "session-b") {
		t.Fatal(human.String())
	}
	if err := WriteJSON(&encoded, v); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		var again bytes.Buffer
		if err := WriteJSON(&again, capture(t, f.root, opt)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(encoded.Bytes(), again.Bytes()) {
			t.Fatal("nondeterministic JSON")
		}
	}
	for _, args := range [][]string{{"inspect-agent", "--agent", "future-a", "--json"}, {"inspect-task", "--objective", "objective", "--task", "task-1", "--json"}, {"inspect-task", "--objective", "objective", "--task", "task-1"}, {"candidates", "--role", "WORKER", "--json"}} {
		var out bytes.Buffer
		args = append(args, "--workspace", f.root)
		if err := RunWithOptions(context.Background(), args, &out, opt); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), assignments[1].Token) {
			t.Fatal("inspection exposed claim")
		}
	}
	if !bytes.Equal(before, data(t, f.path)) || !bytes.Equal(ledger, data(t, filepath.Join(f.root, "multi-agent-orchestrator", "agentmeeting.md"))) {
		t.Fatal("read path mutated authority")
	}
	unknown := capture(t, f.root, Options{})
	if unknown.System.AvailableWorkers != nil || unknown.Agents[0].Connection != "UNKNOWN" {
		t.Fatal("standalone status invented connectivity")
	}
	otherHost := capture(t, f.root, Options{Connected: func(state.AgentRef) bool { return false }})
	if !code(otherHost, "LOCAL_ROUTE_NOT_OBSERVED") || code(otherHost, "WORKER_SESSION_LOST") || code(otherHost, "PLANNER_UNAVAILABLE") {
		t.Fatal("host-local route absence was treated as global lost authority")
	}
}

func TestStaleBlockedDisconnectedAndHumanActions(t *testing.T) {
	f := setup(t)
	one := f.agent(t, "one", "s-one", agentprotocol.RoleWorker)
	two := f.agent(t, "two", "s-two", agentprotocol.RoleWorker)
	ready := f.agent(t, "ready", "s-ready", agentprotocol.RoleWorker)
	p, as := f.acquire(t, one, two)
	if err := scheduler.Block(f.root, p.ID, as[0], "validation check failed: controlled fixture"); err != nil {
		t.Fatal(err)
	}
	if err := f.r.Disconnect(two, "operator reviewed disconnect"); err != nil {
		t.Fatal(err)
	}
	v := capture(t, f.root, Options{})
	if !code(v, "VALIDATION_FAILED") || !code(v, "TASK_BLOCKED") || !code(v, "WORKER_SESSION_LOST") || v.System.ReadyWorkers != 1 || v.Tasks[0].Recovery != "EXPLICIT_RECOVERY_AVAILABLE" {
		t.Fatalf("attention: %+v", v)
	}
	var output bytes.Buffer
	before := data(t, f.path)
	base := []string{"recover-task", "--workspace", f.root, "--objective", p.ID, "--task", as[0].Task.ID, "--generation", fmt.Sprint(as[0].AssignmentGeneration), "--actor", "test-operator", "--reason", "reviewed preserved files"}
	if Run(context.Background(), base, &output) == nil || !bytes.Equal(before, data(t, f.path)) {
		t.Fatal("silence authorized recovery")
	}
	wrong := append(append([]string{}, base...), "--yes", "--writers-stopped", "--generation", "999999")
	if Run(context.Background(), wrong, &output) == nil || !bytes.Equal(before, data(t, f.path)) {
		t.Fatal("stale generation changed assignment")
	}
	if err := Run(context.Background(), append(base, "--yes", "--writers-stopped"), &output); err != nil {
		t.Fatal(err)
	}
	after := capture(t, f.root, Options{})
	if after.Tasks[0].Status != "PENDING" || after.Tasks[0].Owner != nil || after.Tasks[0].OwnershipRetained || len(after.Tasks[0].History) != 1 || after.Tasks[0].History[0].Assignment.Generation != as[0].AssignmentGeneration {
		t.Fatal("recovery did not preserve history")
	}
	provenance := false
	for _, e := range after.Audit {
		provenance = provenance || e.Actor == "operator test-operator: reviewed preserved files"
	}
	if !provenance {
		t.Fatal("human action not attributable")
	}
	if err := Run(context.Background(), []string{"disconnect-agent", "--workspace", f.root, "--agent", string(ready.AgentID), "--session", string(ready.SessionID), "--incarnation", fmt.Sprint(ready.Generation), "--actor", "test-operator", "--reason", "retiring idle worker", "--yes"}, &output); err != nil {
		t.Fatal(err)
	}
	stale := capture(t, f.root, Options{Now: func() time.Time { return time.Now().Add(11 * time.Minute) }})
	if !code(stale, "AGENT_STALE") {
		t.Fatal("stale state invisible")
	}
	status, err := f.store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if status.AgentRegistry.Sessions[one.SessionID].Status != state.AgentBlocked {
		t.Fatal("stale read expired/mutated registry")
	}
}

func TestCompletedEvidenceAndManagedCompatibility(t *testing.T) {
	f := setup(t)
	ref := f.agent(t, "worker", "enrolled", agentprotocol.RoleWorker)
	p, as := f.acquire(t, ref)
	a := as[0]
	f.accept(t, a)
	f.event(t, a, agentprotocol.WorkerResult, nil, "all task writers stopped")
	if err := os.MkdirAll(filepath.Join(a.Worktree, a.Task.Scope), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Worktree, a.Task.Scope, "done"), []byte("done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, a.Worktree, "add", "--", a.Task.Scope)
	git(t, a.Worktree, "commit", "-m", "Implement fixture")
	head := git(t, a.Worktree, "rev-parse", "HEAD")
	in := review.ResultInput{Workspace: f.root, ObjectiveID: p.ID, TaskID: a.Task.ID, SessionID: a.SessionID, Token: a.Token, Branch: a.Branch, Worktree: a.Worktree, AssignmentGeneration: a.AssignmentGeneration, ValidatedHead: head, Validation: "trusted check PASS; RAW_ENVIRONMENT_SECRET_VALUE"}
	if err := review.RecordValidation(in); err != nil {
		t.Fatal(err)
	}
	if _, err := review.FinalizeRecorded(in); err != nil {
		t.Fatal(err)
	}
	v := capture(t, f.root, Options{})
	task := v.Tasks[0]
	if task.Status != "COMPLETE" || task.Claim != "RELEASED" || task.Result == nil || !task.Result.EvidencePresent || task.Result.Head != head || task.ReviewStatus != "AWAITING_REVIEW" || !code(v, "REVIEW_REQUIRED") || v.Agents[0].Status != state.AgentReady {
		t.Fatalf("result: %+v", v)
	}
	b, _ := json.Marshal(v)
	if bytes.Contains(b, []byte("RAW_ENVIRONMENT_SECRET_VALUE")) {
		t.Fatal("raw validator output leaked")
	}
	managed := setup(t)
	_, ma := managed.acquire(t)
	mv := capture(t, managed.root, Options{})
	if len(mv.Agents) != 0 || mv.Tasks[0].Owner.Mode != "managed" || mv.Tasks[0].Owner.OperationalSession != ma[0].SessionID || mv.Tasks[0].Claim != "HELD" {
		t.Fatal("managed workers were treated as enrolled agents")
	}
}

func TestRedactionCoversAllViews(t *testing.T) {
	f := setup(t)
	ref := f.agent(t, "worker", "enrolled", agentprotocol.RoleWorker)
	_, as := f.acquire(t, ref)
	f.accept(t, as[0])
	f.event(t, as[0], agentprotocol.WorkerProgress, nil, "claim="+as[0].Token+" API_KEY=provider-private-value Authorization: Bearer bearer-private-value")
	if err := f.store.Update("test human", "seed-history", func(s *state.Snapshot) error {
		task := s.Tasks["objective::task-0"]
		task.Description = "description " + as[0].Token
		task.AssignmentHistory = []state.AssignmentHistory{{Attempt: 1, AssignmentGeneration: 1, Token: "history-private-claim", Blocker: "history-private-claim"}}
		s.Tasks["objective::task-0"] = task
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status", "--json"}, {"status"}, {"inspect-agent", "--agent", "worker", "--json"}, {"inspect-task", "--objective", "objective", "--task", "task-0", "--json"}, {"inspect-task", "--objective", "objective", "--task", "task-0"}} {
		var out bytes.Buffer
		if err := Run(context.Background(), append(args, "--workspace", f.root), &out); err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{as[0].Token, "history-private-claim", "provider-private-value", "bearer-private-value"} {
			if strings.Contains(out.String(), secret) {
				t.Fatalf("view leaked private data: %s", secret)
			}
		}
		if !strings.Contains(out.String(), "REDACTED") {
			t.Fatal("expected explicit redaction")
		}
	}
}

func TestCorruptionFailsClosedWithoutMigrationOrRepair(t *testing.T) {
	f := setup(t)
	original := data(t, f.path)
	for _, mode := range []string{"bad-json", "duplicate", "legacy", "missing-objective", "invalid-review", "missing-state"} {
		t.Run(mode, func(t *testing.T) {
			defer func() {
				if err := os.WriteFile(f.path, original, 0600); err != nil {
					t.Error(err)
				}
			}()
			bad := []byte("{invalid")
			if mode == "duplicate" {
				bad = bytes.Replace(original, []byte(`"schema": 4`), []byte(`"schema": 4,"schema": 4`), 1)
			}
			if mode == "legacy" {
				bad = bytes.Replace(original, []byte(`"schema": 4`), []byte(`"schema": 3`), 1)
			}
			if mode == "missing-objective" {
				var s state.Snapshot
				if json.Unmarshal(original, &s) != nil {
					t.Fatal("fixture")
				}
				s.Tasks["o::t"] = state.Task{ID: "t", ObjectiveID: "o", Status: "PENDING"}
				bad, _ = json.Marshal(s)
			}
			if mode == "invalid-review" {
				var s state.Snapshot
				if json.Unmarshal(original, &s) != nil {
					t.Fatal("fixture")
				}
				s.Reviews["unknown"] = state.Review{ObjectiveID: "unknown", Status: "unknown"}
				bad, _ = json.Marshal(s)
			}
			if mode == "missing-state" {
				if err := os.Rename(f.path, f.path+".preserved"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(f.path, bad, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			err := Run(context.Background(), []string{"status", "--workspace", f.root, "--json"}, &out)
			if !errors.Is(err, ErrAuthority) || out.Len() != 0 {
				t.Fatalf("corruption accepted: %s %v", out.String(), err)
			}
			if mode != "missing-state" && !bytes.Equal(bad, data(t, f.path)) {
				t.Fatal("inspection rewrote corrupt state")
			}
		})
	}
}

func TestConcurrentReadsHeartbeatsProgressAndDetachedViews(t *testing.T) {
	f := setup(t)
	ref := f.agent(t, "worker", "enrolled", agentprotocol.RoleWorker)
	_, as := f.acquire(t, ref)
	f.accept(t, as[0])
	max := 3
	r, err := Open(f.root, Options{MaxWorkers: &max})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	start := make(chan struct{})
	errs := make(chan error, 5)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for n := 0; n < 15; n++ {
				v, e := r.Capture(ctx)
				if errors.Is(e, ErrChanging) {
					continue
				}
				if e != nil {
					errs <- e
					return
				}
				task := v.Tasks[0]
				agent := v.Agents[0]
				if task.Owner.SessionID != agent.SessionID || task.Owner.Incarnation != agent.Incarnation || task.Assignment != agent.Assignments[0] {
					errs <- errors.New("torn authority projection")
					return
				}
				*v.MaxWorkers = 9
			}
			errs <- nil
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for n := 0; n < 15; n++ {
			if e := f.r.Heartbeat(ref); e != nil {
				errs <- e
				return
			}
			e := scheduler.WorkerEventContext(ctx, f.root, ref, agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version4, AgentID: ref.AgentID, SessionID: ref.SessionID, Type: agentprotocol.WorkerProgress, Work: &agentprotocol.WorkerMessage{Correlation: as[0].WorkerDispatch.Correlation, Detail: fmt.Sprintf("progress %d", n)}})
			if e != nil {
				errs <- e
				return
			}
		}
		errs <- nil
	}()
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	v, e := r.Capture(ctx)
	if e != nil || *v.MaxWorkers != 3 || v.Tasks[0].Progress != "progress 14" {
		t.Fatal("read view aliases authority/options or lost progress", e)
	}
}

type cancelWriter struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelWriter) Write(p []byte) (int, error) {
	n, e := w.Buffer.Write(p)
	w.cancel()
	return n, e
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestWatchCancellationBoundsAndChangingSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	out := &cancelWriter{cancel: cancel}
	calls := 0
	capture := func(context.Context) (Snapshot, error) { calls++; return Snapshot{Version: Version}, nil }
	if err := watch(ctx, capture, out, true, time.Second, 0, make(chan time.Time)); err != nil || calls != 1 {
		t.Fatal("watch cancellation failed", err, calls)
	}
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	var output bytes.Buffer
	calls = 0
	if err := watch(context.Background(), capture, &output, true, time.Second, 2, ticks); err != nil || calls != 2 || bytes.Count(output.Bytes(), []byte("\n")) != 2 {
		t.Fatal("finite NDJSON watch failed", err, calls)
	}
	ctx, cancel = context.WithCancel(context.Background())
	out = &cancelWriter{cancel: cancel}
	calls = 0
	ticks = make(chan time.Time, 1)
	ticks <- time.Now()
	changing := func(context.Context) (Snapshot, error) {
		calls++
		if calls == 1 {
			return Snapshot{}, ErrChanging
		}
		return Snapshot{Version: Version}, nil
	}
	if err := watch(ctx, changing, out, true, time.Second, 0, ticks); err != nil || calls != 2 {
		t.Fatal("transient snapshot contention failed", err, calls)
	}
	if err := watch(context.Background(), capture, brokenWriter{}, false, time.Second, 1, make(chan time.Time)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("watch lost output error", err)
	}
	for _, args := range [][]string{{"status", "--watch", "--interval", "1ms"}, {"inspect-agent", "--watch"}, {"status", "--updates", "2"}} {
		if Run(context.Background(), args, &output) == nil {
			t.Fatal("invalid watch configuration accepted")
		}
	}
}

func TestSnapshotContentsDoNotAliasInput(t *testing.T) {
	f := setup(t)
	f.agent(t, "one", "one-session", agentprotocol.RoleWorker)
	before, err := f.store.Read()
	if err != nil {
		t.Fatal(err)
	}
	v := capture(t, f.root, Options{})
	v.Agents[0].Capabilities[0] = "changed"
	v.Agents[0].Metadata.Provider = "changed"
	after, err := f.store.Read()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("projection changed authority", err)
	}
}
