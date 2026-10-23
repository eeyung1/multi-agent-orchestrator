//go:build unix

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agenttransport"
	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/review"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

type workerRuntimeFixture struct {
	workspace string
	config    Config
	registry  *agentregistry.Registry
	server    *agentattachment.Server
	socket    string
	clients   []*agentattachment.Client
	ctx       context.Context
}

func enrolledFixture(t *testing.T, n int) *workerRuntimeFixture {
	return enrolledFixtureConfigured(t, n, nil)
}
func enrolledFixtureConfigured(t *testing.T, n int, configure func(*agentattachment.Config)) *workerRuntimeFixture {
	t.Helper()
	d, c := fixture(t)
	c.Workers = nil
	c.EnrolledCapabilities = []agentprotocol.Capability{"coding"}
	c.MaxWorkers = 3
	c.TimeoutSeconds = 180
	// Integration deadlines are deliberately generous under race instrumentation;
	// timeout behavior itself is exercised with a controlled cancellation seam.
	c.AcceptanceSeconds = 120
	r, err := agentregistry.Open(d, agentregistry.Policy{StaleAfter: 10 * time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := agentattachment.DefaultConfig()
	cfg.IdleTimeout = 5 * time.Minute
	if configure != nil {
		configure(&cfg)
	}
	s, err := agentattachment.New(r, cfg)
	if err != nil {
		t.Fatal(err)
	}
	short, err := os.MkdirTemp("", "mao-workers-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(short) })
	short, err = filepath.EvalSymlinks(short)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(short, "workers.sock")
	l, err := agentattachment.ListenLocal(s, socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	done := make(chan error, 1)
	go func() { done <- l.Serve(ctx) }()
	f := &workerRuntimeFixture{workspace: d, config: c, registry: r, server: s, socket: socket, ctx: ctx}
	f.config.Runtime = &EnrolledWorkers{Registry: r, Server: s}
	t.Cleanup(func() {
		cancel()
		for _, c := range f.clients {
			_ = c.Close()
		}
		_ = l.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < n; i++ {
		f.add(t, fmt.Sprintf("agent-%d", i), fmt.Sprintf("session-%d", i), fmt.Sprintf("future-runtime-%d", i), false)
	}
	return f
}
func (f *workerRuntimeFixture) add(t *testing.T, agent, session, provider string, replace bool) *agentattachment.Client {
	t.Helper()
	_, proof, err := f.server.Invite(agentattachment.InviteRequest{AgentID: agentprotocol.AgentID(agent), Roles: []agentprotocol.Role{agentprotocol.RoleWorker, agentprotocol.RolePlanner}, Capabilities: []agentprotocol.Capability{"coding", "planning"}, TTL: time.Minute, Replace: replace}, true)
	if err != nil {
		t.Fatal(err)
	}
	id := agentprotocol.Identity{ProtocolVersion: agentprotocol.Version4, AgentID: agentprotocol.AgentID(agent), SessionID: agentprotocol.SessionID(session), Metadata: agentprotocol.Metadata{Provider: provider, Runtime: "independent-" + session}, Capabilities: []agentprotocol.Capability{"coding"}, SupportedRoles: []agentprotocol.Role{agentprotocol.RoleWorker}}
	client, err := agentattachment.DialLocal(f.ctx, f.socket, id, proof)
	if err != nil {
		t.Fatal(err)
	}
	f.clients = append(f.clients, client)
	if err := f.registry.AssignRole(client.Binding(), agentprotocol.RoleWorker, "test human authorized exact session"); err != nil {
		t.Fatal(err)
	}
	// A lifecycle round trip also proves the server route completed enrollment.
	if err := client.Heartbeat(f.ctx); err != nil {
		t.Fatal(err)
	}
	return client
}
func receive[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		var zero T
		return zero
	}
}
func report(c *agentattachment.Client, ctx context.Context, w agentprotocol.WorkerMessage, kind agentprotocol.MessageType, detail string) error {
	m := agentprotocol.WorkerMessage{Correlation: w.Correlation, Detail: detail}
	if kind == agentprotocol.WorkerAcceptance {
		yes := true
		m.Accepted = &yes
		m.Detail = ""
	}
	return c.ReportWork(ctx, kind, m)
}
func writeWorkerResult(w agentprotocol.WorkerMessage) error {
	path := filepath.Join(w.Assignment.Worktree, w.Assignment.Task.Scope, "result.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(w.Assignment.Task.Description+"\n"), 0644)
}

// The barrier is released ONLY after all three actual IPC clients accepted their
// distinct assignments. A serialized dispatcher cannot pass this test.
func TestEnrolledWorkersParallelAcceptance(t *testing.T) {
	ended := make(chan string, 16)
	f := enrolledFixtureConfigured(t, 3, func(c *agentattachment.Config) {
		c.SessionEnded = func(ref agentregistry.Ref, err error) { ended <- fmt.Sprintf("%s: %v", ref.SessionID, err) }
	})
	p := admit(t, f.workspace, f.config, task("a"), task("b"), task("c"))
	started := make(chan agentprotocol.WorkerMessage, 3)
	release := make(chan struct{})
	errs := make(chan error, 3)
	for _, client := range f.clients {
		go func(c *agentattachment.Client) {
			w, err := c.ReceiveWork(f.ctx)
			if err == nil {
				err = report(c, f.ctx, w, agentprotocol.WorkerAcceptance, "")
			}
			if err == nil {
				err = c.Heartbeat(f.ctx)
			}
			if err != nil {
				errs <- err
				return
			}
			started <- w
			select {
			case <-release:
			case <-f.ctx.Done():
				errs <- f.ctx.Err()
				return
			}
			if err = writeWorkerResult(w); err == nil {
				err = report(c, f.ctx, w, agentprotocol.WorkerProgress, "bounded implementation finished")
			}
			if err == nil {
				err = report(c, f.ctx, w, agentprotocol.WorkerResult, "All task writers stopped; changes left uncommitted")
			}
			errs <- err
		}(client)
	}
	results := make(chan struct {
		r Report
		e error
	}, 1)
	go func() {
		r, e := Run(f.ctx, f.workspace, p.ID, f.config)
		results <- struct {
			r Report
			e error
		}{r, e}
	}()
	var assignments []agentprotocol.WorkerMessage
	for i := 0; i < 3; i++ {
		assignments = append(assignments, receive(t, f.ctx, started))
	}
	before, err := scheduler.Read(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := coordination.Claims(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 3 {
		t.Fatal("parallel dispatch did not preserve three exclusive claims")
	}
	tasks, branches, trees, gens, dispatch := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[uint64]bool{}, map[string]bool{}
	sessions, incarnations, providers, runtimes := map[agentprotocol.SessionID]bool{}, map[state.AgentSessionGeneration]bool{}, map[string]bool{}, map[string]bool{}
	for _, w := range assignments {
		d := before.Tasks[p.ID+"::"+w.Correlation.TaskID]
		if d.Status != "ASSIGNED" || d.WorkerDispatch.Phase != "ACCEPTED" {
			t.Fatalf("not simultaneously in flight: %+v", d)
		}
		a := before.AgentRegistry.Sessions[d.WorkerDispatch.SessionID]
		if a.Status != state.AgentBusy || a.AssignedRole != agentprotocol.RoleWorker || a.Generation != d.WorkerDispatch.Incarnation || a.Identity.AgentID != d.WorkerDispatch.AgentID {
			t.Fatal("overlapping assignment lacks exact BUSY worker owner")
		}
		sessions[a.Identity.SessionID] = true
		incarnations[a.Generation] = true
		providers[a.Identity.Metadata.Provider] = true
		runtimes[a.Identity.Metadata.Runtime] = true
		matched := false
		for _, claim := range claims {
			matched = matched || claim.Token == d.Token && claim.SessionID == d.SessionID && claim.Scope == d.Scope
		}
		if !matched {
			t.Fatal("claim does not match its unique assignment owner/scope")
		}
		tasks[d.ID] = true
		branches[d.Branch] = true
		trees[d.Worktree] = true
		gens[d.AssignmentGeneration] = true
		dispatch[w.Correlation.DispatchID] = true
		if d.Worktree != w.Assignment.Worktree || d.Branch != w.Assignment.Branch || strings.Contains(w.Correlation.DispatchID, d.Token) || w.Correlation.DispatchID == strings.TrimPrefix(d.Token, "claim-") {
			t.Fatal("wire identity/worktree leak or mismatch")
		}
	}
	if len(tasks) != 3 || len(branches) != 3 || len(trees) != 3 || len(gens) != 3 || len(dispatch) != 3 || len(sessions) != 3 || len(incarnations) != 3 || len(providers) != 3 || len(runtimes) != 3 {
		t.Fatal("parallel assignments share identity/isolation")
	}
	close(release)
	for i := 0; i < 3; i++ {
		if err := receive(t, f.ctx, errs); err != nil {
			t.Fatal(err)
		}
	}
	result := receive(t, f.ctx, results)
	if result.e != nil || result.r.Status != "AWAITING_REVIEW" {
		var reasons []string
		for {
			select {
			case reason := <-ended:
				reasons = append(reasons, reason)
			default:
				t.Fatalf("%+v %v; ended sessions: %v", result.r, result.e, reasons)
			}
		}
	}
	after, err := scheduler.Read(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	claims, err = coordination.Claims(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 || len(after.Results) != 3 || after.Reviews[p.ID].Status != "AWAITING_REVIEW" {
		t.Fatal("parallel results did not release claims and reach human review")
	}
	for _, w := range assignments {
		task := after.Tasks[p.ID+"::"+w.Correlation.TaskID]
		if task.Status != "COMPLETE" || task.WorkerDispatch.Phase != "FINISHED" || task.ValidatedHead == "" || task.ResultID == "" {
			t.Fatalf("not reviewed: %+v", task)
		}
		if helperGit(t, task.Worktree, "status", "--porcelain") != "" {
			t.Fatal("dirty result")
		}
		a := after.AgentRegistry.Sessions[task.WorkerDispatch.SessionID]
		if a.Status != state.AgentReady {
			t.Fatalf("worker not READY: %+v", a)
		}
		replay := agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version4, Type: agentprotocol.WorkerResult, AgentID: a.Identity.AgentID, SessionID: a.Identity.SessionID, Work: &agentprotocol.WorkerMessage{Correlation: w.Correlation, Detail: "replayed"}}
		if scheduler.WorkerEvent(f.workspace, workerRef(*task.WorkerDispatch), replay) == nil {
			t.Fatal("replayed completion accepted")
		}
	}
}

func TestEnrolledWorkerFailuresPreserveRecovery(t *testing.T) {
	for _, mode := range []string{"reject", "blocker", "disconnect-before", "disconnect-during", "supersede", "wrong-generation", "wrong-dispatch", "unowned", "validation", "preparation"} {
		t.Run(mode, func(t *testing.T) {
			f := enrolledFixture(t, 1)
			v := task("work")
			if mode == "validation" {
				v.Validation = []string{"fail"}
			}
			if mode == "preparation" {
				f.config.WorktreePreparation = []execution.Command{{Program: "false"}}
			}
			p := admit(t, f.workspace, f.config, v)
			done := make(chan error, 1)
			var old agentprotocol.WorkerMessage
			go func() {
				if mode == "preparation" {
					done <- nil
					return
				}
				c := f.clients[0]
				w, err := c.ReceiveWork(f.ctx)
				old = w
				if err != nil {
					done <- err
					return
				}
				if mode == "disconnect-before" {
					done <- c.Close()
					return
				}
				if mode == "reject" {
					no := false
					done <- c.ReportWork(f.ctx, agentprotocol.WorkerAcceptance, agentprotocol.WorkerMessage{Correlation: w.Correlation, Accepted: &no, Detail: "cannot perform bounded task"})
					return
				}
				err = report(c, f.ctx, w, agentprotocol.WorkerAcceptance, "")
				if err == nil {
					err = c.Heartbeat(f.ctx)
				}
				if err != nil {
					done <- err
					return
				}
				switch mode {
				case "blocker":
					err = report(c, f.ctx, w, agentprotocol.WorkerBlocker, "missing authorized dependency")
				case "disconnect-during", "supersede":
					err = c.Close()
				case "wrong-generation":
					w.Correlation.Generation++
					err = report(c, f.ctx, w, agentprotocol.WorkerResult, "invalid generation")
					if err == nil {
						_ = c.Heartbeat(f.ctx)
					}
				case "wrong-dispatch":
					w.Correlation.DispatchID = "unknown"
					err = report(c, f.ctx, w, agentprotocol.WorkerResult, "invalid request")
					if err == nil {
						_ = c.Heartbeat(f.ctx)
					}
				default:
					err = writeWorkerResult(w)
					if err == nil && mode == "unowned" {
						err = os.WriteFile(filepath.Join(w.Assignment.Worktree, "outside.txt"), []byte("forbidden"), 0644)
					}
					if err == nil {
						err = report(c, f.ctx, w, agentprotocol.WorkerResult, "writers stopped")
					}
				}
				done <- err
			}()
			r, err := Run(f.ctx, f.workspace, p.ID, f.config)
			if err != nil {
				t.Fatal(err)
			}
			if e := receive(t, f.ctx, done); e != nil {
				t.Fatal(e)
			}
			s, err := scheduler.Read(f.workspace)
			if err != nil {
				t.Fatal(err)
			}
			st := s.Tasks[p.ID+"::work"]
			if st.Status != "BLOCKED" || st.WorkerDispatch.Phase != "FAILED" || st.Branch == "" || st.Worktree == "" || r.Status == "AWAITING_REVIEW" {
				t.Fatalf("failure lost recovery: %+v %+v", st, r)
			}
			if status, err := coordination.SessionStatus(f.workspace, st.SessionID); err != nil || status != "BLOCKED" {
				t.Fatalf("operational split: %s %v", status, err)
			}
			if mode == "preparation" {
				if _, err := os.Stat(filepath.Join(st.Worktree, "work", "result.txt")); !os.IsNotExist(err) {
					t.Fatal("worker executed after preparation failure")
				}
			}
			if mode == "reject" {
				if st.WorkerDispatch.LastMessage != agentprotocol.WorkerAcceptance || st.WorkerDispatch.Detail != "cannot perform bounded task" {
					t.Fatal("rejection evidence lost")
				}
				if _, err := Run(f.ctx, f.workspace, p.ID, f.config); err != nil {
					t.Fatal(err)
				}
				again, err := scheduler.Read(f.workspace)
				if err != nil || again.Tasks[p.ID+"::work"].Attempt != 1 || again.AgentRegistry.Sessions[f.clients[0].Binding().SessionID].Status == state.AgentBusy {
					t.Fatal("rejection retried or retained BUSY reservation", err)
				}
			}
			if mode == "supersede" {
				replacement := f.add(t, "agent-0", "replacement", "unknown-new-runtime", true)
				if scheduler.WorkerEvent(f.workspace, f.clients[0].Binding(), agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version4, Type: agentprotocol.WorkerResult, AgentID: "agent-0", SessionID: "session-0", Work: &agentprotocol.WorkerMessage{Correlation: old.Correlation, Detail: "late result"}}) == nil {
					t.Fatal("old completion accepted")
				}
				if err := scheduler.AuthorizeRecovery(f.workspace, p.ID, st.ID, st.AssignmentGeneration, "human reviewed stale attempt"); err != nil {
					t.Fatal(err)
				}
				snapshot, err := scheduler.Read(f.workspace)
				if err != nil {
					t.Fatal(err)
				}
				pending := snapshot.Tasks[p.ID+"::work"]
				if pending.Status != "PENDING" || pending.WorkerDispatch != nil || len(pending.AssignmentHistory) != 1 || pending.AssignmentHistory[0].WorkerDispatch.Correlation != st.WorkerDispatch.Correlation {
					t.Fatal("history lost or replacement inherited assignment")
				}
				if err := replacement.Heartbeat(f.ctx); err != nil {
					t.Fatal(err)
				}
				ref := replacement.Binding()
				next, err := scheduler.Acquire(scheduler.Request{Workspace: f.workspace, Agent: "enrolled-worker", Role: "coding", Plan: p, TaskID: "work", Base: "main", Worker: &ref, Registry: f.registry})
				if err != nil {
					t.Fatal(err)
				}
				if next.AssignmentGeneration <= st.AssignmentGeneration || next.Attempt != 2 || next.Token == st.Token || next.Branch == st.Branch || next.Worktree == st.Worktree || next.WorkerDispatch.Correlation.DispatchID == st.WorkerDispatch.Correlation.DispatchID {
					t.Fatal("recovery reused old authority/worktree")
				}
				before, err := scheduler.Read(f.workspace)
				if err != nil {
					t.Fatal(err)
				}
				for _, binding := range []agentregistry.Ref{f.clients[0].Binding(), ref} {
					late := agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version4, Type: agentprotocol.WorkerResult, AgentID: binding.AgentID, SessionID: binding.SessionID, Work: &agentprotocol.WorkerMessage{Correlation: old.Correlation, Detail: "late old attempt"}}
					if scheduler.WorkerEvent(f.workspace, binding, late) == nil {
						t.Fatal("old attempt affected replacement assignment")
					}
				}
				after, err := scheduler.Read(f.workspace)
				if err != nil || after.Authority.Generation != before.Authority.Generation {
					t.Fatal("stale report mutated newer generation", err)
				}
			}
		})
	}
}

func TestEnrolledWorkerCandidatePolicy(t *testing.T) {
	f := enrolledFixture(t, 2)
	ref, err := f.config.selectWorker("coding")
	if err != nil || ref != f.clients[0].Binding() {
		t.Fatal("candidate selection not deterministic")
	}
	if _, err := f.config.selectWorker("not-allowed"); err == nil {
		t.Fatal("unapproved capability dispatched")
	}
	if err := f.registry.ClearRole(ref, "human removed assigned role"); err != nil {
		t.Fatal(err)
	}
	next, err := f.config.selectWorker("coding")
	if err != nil || next != f.clients[1].Binding() {
		t.Fatal("unassigned role eligible")
	}
	if err := f.registry.Transition(next, agentregistry.Busy, "other bounded execution"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.config.selectWorker("coding"); err == nil {
		t.Fatal("BUSY/unassigned worker eligible")
	}
	if err := f.registry.Transition(next, agentregistry.Ready, "execution stopped"); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.ClearRole(next, "human changes participant role"); err != nil {
		t.Fatal(err)
	}
	v, err := f.registry.Get(next.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	id := v.Session.Identity
	id.SupportedRoles = []agentprotocol.Role{agentprotocol.RolePlanner}
	id.Capabilities = []agentprotocol.Capability{"planning"}
	if err := f.registry.Register(next, id); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.AssignRole(next, agentprotocol.RolePlanner, "human planner role"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.config.selectWorker("coding"); err == nil {
		t.Fatal("Planner-only agent eligible")
	}
}

func TestEnrolledWorkerTimeoutAndCancellation(t *testing.T) {
	for _, mode := range []string{"acceptance-timeout", "execution-cancel"} {
		t.Run(mode, func(t *testing.T) {
			// Controlled acceptance deadline: no sleeping or wall-clock race.
			cancelAcceptance := make(chan context.CancelFunc, 1)
			f := enrolledFixtureConfigured(t, 1, func(c *agentattachment.Config) {
				c.WithTimeout = func(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
					if d == 7*time.Second {
						ctx, cancel := context.WithCancel(parent)
						cancelAcceptance <- cancel
						return ctx, cancel
					}
					return context.WithTimeout(parent, d)
				}
			})
			f.config.AcceptanceSeconds = 7
			p := admit(t, f.workspace, f.config, task("timeout"))
			ctx, cancelRun := context.WithCancel(f.ctx)
			defer cancelRun()
			done := make(chan error, 1)
			go func() {
				c := f.clients[0]
				w, err := c.ReceiveWork(f.ctx)
				if err != nil {
					done <- err
					return
				}
				if mode == "acceptance-timeout" {
					cancel := <-cancelAcceptance
					cancel()
				} else {
					if err = report(c, f.ctx, w, agentprotocol.WorkerAcceptance, ""); err == nil {
						err = c.Heartbeat(f.ctx)
					}
					cancelRun()
				}
				done <- err
			}()
			if _, err := Run(ctx, f.workspace, p.ID, f.config); err != nil {
				t.Fatal(err)
			}
			if err := receive(t, f.ctx, done); err != nil {
				t.Fatal(err)
			}
			snapshot, err := scheduler.Read(f.workspace)
			if err != nil {
				t.Fatal(err)
			}
			st := snapshot.Tasks[p.ID+"::timeout"]
			if st.Status != "BLOCKED" || st.WorkerDispatch.Phase != "FAILED" {
				t.Fatal("timeout/cancel did not preserve blocked assignment")
			}
		})
	}
}

func TestMixedManagedAndEnrolledWorkers(t *testing.T) {
	f := enrolledFixture(t, 1)
	f.config.Workers = map[string]Worker{"managed": {Program: os.Args[0], Args: []string{"-test.run=^TestWorkerHelper$"}}}
	managed := task("managed")
	managed.Capability = "managed"
	p := admit(t, f.workspace, f.config, managed, task("enrolled"))
	done := make(chan error, 1)
	go func() {
		c := f.clients[0]
		w, err := c.ReceiveWork(f.ctx)
		if err == nil {
			err = report(c, f.ctx, w, agentprotocol.WorkerAcceptance, "")
		}
		if err == nil {
			err = writeWorkerResult(w)
		}
		if err == nil {
			err = report(c, f.ctx, w, agentprotocol.WorkerResult, "writers stopped")
		}
		done <- err
	}()
	r, err := Run(f.ctx, f.workspace, p.ID, f.config)
	if err != nil || r.Status != "AWAITING_REVIEW" {
		t.Fatalf("mixed result %+v %v", r, err)
	}
	if err := receive(t, f.ctx, done); err != nil {
		t.Fatal(err)
	}
	snapshot, err := scheduler.Read(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Tasks[p.ID+"::managed"].WorkerDispatch != nil || snapshot.Tasks[p.ID+"::enrolled"].WorkerDispatch == nil {
		t.Fatal("worker execution modes confused")
	}
}

func TestManagedWorkerTakesPriorityForSharedCapability(t *testing.T) {
	f := enrolledFixture(t, 1)
	f.config.Workers = map[string]Worker{"coding": {Program: os.Args[0], Args: []string{"-test.run=^TestWorkerHelper$"}}}
	p := admit(t, f.workspace, f.config, task("shared"))
	r, err := Run(f.ctx, f.workspace, p.ID, f.config)
	if err != nil || r.Status != "AWAITING_REVIEW" {
		t.Fatalf("managed priority: %+v %v", r, err)
	}
	s, err := scheduler.Read(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	if s.Tasks[p.ID+"::shared"].WorkerDispatch != nil || s.AgentRegistry.WorkerDispatchCount != 0 || s.AgentRegistry.Sessions[f.clients[0].Binding().SessionID].Status != state.AgentReady {
		t.Fatal("shared capability consumed enrolled worker instead of managed worker")
	}
}

func TestEnrolledRestartPreservesBindingAndRequiresRecovery(t *testing.T) {
	f := enrolledFixture(t, 1)
	p := admit(t, f.workspace, f.config, task("old"))
	ref := f.clients[0].Binding()
	a, err := scheduler.Acquire(scheduler.Request{Workspace: f.workspace, Agent: "enrolled-worker", Role: "coding", Plan: p, TaskID: "old", Base: "main", Worker: &ref, Registry: f.registry})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.MarkWorkerSent(f.workspace, p.ID, a); err != nil {
		t.Fatal(err)
	}
	if err := f.clients[0].Disconnect(f.ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := agentregistry.Open(f.workspace, agentregistry.Policy{StaleAfter: 10 * time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.config.Runtime.Registry = reopened
	if _, err := Run(f.ctx, f.workspace, p.ID, f.config); err != nil {
		t.Fatal(err)
	}
	snapshot, err := scheduler.Read(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	st := snapshot.Tasks[p.ID+"::old"]
	if st.Status != "BLOCKED" || st.Attempt != 1 || st.WorkerDispatch.Correlation != a.WorkerDispatch.Correlation || st.Branch != a.Branch || st.Worktree != a.Worktree {
		t.Fatal("restart relaunched/lost old assignment")
	}
	replacement := f.add(t, "agent-0", "new-session", "new-provider", true)
	if snapshot.Tasks[p.ID+"::old"].WorkerDispatch.SessionID == replacement.Binding().SessionID {
		t.Fatal("replacement inherited assignment")
	}
	if scheduler.AuthorizeRecovery(f.workspace, p.ID, st.ID, st.AssignmentGeneration, "") == nil {
		t.Fatal("silence authorized recovery")
	}
}

func TestWorkerReportsRequireExactDurableBinding(t *testing.T) {
	f := enrolledFixture(t, 2)
	p := admit(t, f.workspace, f.config, task("a"))
	ref := f.clients[0].Binding()
	a, err := scheduler.Acquire(scheduler.Request{Workspace: f.workspace, Agent: "enrolled-worker", Role: "coding", Plan: p, TaskID: "a", Base: "main", Worker: &ref, Registry: f.registry})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.MarkWorkerSent(f.workspace, p.ID, a); err != nil {
		t.Fatal(err)
	}
	yes := true
	valid := agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version4, Type: agentprotocol.WorkerAcceptance, AgentID: ref.AgentID, SessionID: ref.SessionID, Work: &agentprotocol.WorkerMessage{Correlation: a.WorkerDispatch.Correlation, Accepted: &yes}}
	checkFaults := func(valid agentprotocol.Envelope) {
		t.Helper()
		before, err := scheduler.Read(f.workspace)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"wrong-agent", "wrong-session", "wrong-incarnation", "wrong-objective", "wrong-task", "wrong-generation", "unknown-dispatch", "result-before-accept"} {
			if mode == "result-before-accept" && valid.Type != agentprotocol.WorkerAcceptance {
				continue
			}
			e := valid
			w := *valid.Work
			e.Work = &w
			binding := ref
			switch mode {
			case "wrong-agent":
				binding = f.clients[1].Binding()
				e.AgentID = binding.AgentID
				e.SessionID = binding.SessionID
			case "wrong-session":
				e.SessionID = "not-bound"
			case "wrong-incarnation":
				binding.Generation++
			case "wrong-objective":
				w.Correlation.ObjectiveID = "other"
			case "wrong-task":
				w.Correlation.TaskID = "other"
			case "wrong-generation":
				w.Correlation.Generation++
			case "unknown-dispatch":
				w.Correlation.DispatchID = "other"
			case "result-before-accept":
				e.Type = agentprotocol.WorkerResult
				w.Accepted = nil
				w.Detail = "premature"
			}
			if scheduler.WorkerEvent(f.workspace, binding, e) == nil {
				t.Fatalf("%s %s accepted", valid.Type, mode)
			}
		}
		after, err := scheduler.Read(f.workspace)
		if err != nil || after.Authority.Generation != before.Authority.Generation {
			t.Fatal("invalid worker assertion mutated authoritative state", err)
		}
	}
	checkFaults(valid)
	if err := scheduler.WorkerEvent(f.workspace, ref, valid); err != nil {
		t.Fatal(err)
	}
	if scheduler.WorkerEvent(f.workspace, ref, valid) == nil {
		t.Fatal("duplicate acceptance accepted")
	}
	for _, kind := range []agentprotocol.MessageType{agentprotocol.WorkerProgress, agentprotocol.WorkerBlocker, agentprotocol.WorkerResult} {
		e := valid
		e.Type = kind
		e.Work = &agentprotocol.WorkerMessage{Correlation: valid.Work.Correlation, Detail: "report assertion"}
		checkFaults(e)
	}
	if f.registry.Transition(ref, agentregistry.Ready, "unilateral release while executing") == nil {
		t.Fatal("registry bypassed active scheduler reservation")
	}
	replacement := f.add(t, "agent-0", "superseding-session", "unknown-provider", true)
	late := valid
	late.Type = agentprotocol.WorkerResult
	late.Work = &agentprotocol.WorkerMessage{Correlation: valid.Work.Correlation, Detail: "old result"}
	if scheduler.WorkerEvent(f.workspace, ref, late) == nil {
		t.Fatal("superseded result accepted")
	}
	if err := scheduler.Block(f.workspace, p.ID, a, "exact stale worker superseded"); err != nil {
		t.Fatal(err)
	}
	if err := f.clients[0].Close(); err != nil {
		t.Fatal(err)
	}
	if err := replacement.Heartbeat(f.ctx); err != nil {
		t.Fatal("old close affected replacement", err)
	}
}

func TestEnrolledSupersessionDuringExecution(t *testing.T) {
	f := enrolledFixture(t, 1)
	p := admit(t, f.workspace, f.config, task("a"))
	finished := make(chan error, 1)
	go func() { _, err := Run(f.ctx, f.workspace, p.ID, f.config); finished <- err }()
	c := f.clients[0]
	w, err := c.ReceiveWork(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := report(c, f.ctx, w, agentprotocol.WorkerAcceptance, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Heartbeat(f.ctx); err != nil {
		t.Fatal(err)
	}
	replacement := f.add(t, "agent-0", "new-session", "future-runtime", true)
	if err := receive(t, f.ctx, finished); err != nil {
		t.Fatal(err)
	}
	s, err := scheduler.Read(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	task := s.Tasks[p.ID+"::a"]
	if task.Status != "BLOCKED" || task.WorkerDispatch.SessionID != c.Binding().SessionID {
		t.Fatal("supersession failed to fence old task")
	}
	if s.AgentRegistry.Sessions[replacement.Binding().SessionID].Status != state.AgentReady {
		t.Fatal("old task changed replacement session")
	}
}

func TestEnrolledWorktreePreparationPrecedesDelivery(t *testing.T) {
	f := enrolledFixture(t, 1)
	if err := os.WriteFile(filepath.Join(f.workspace, ".gitignore"), []byte(".worktree-preparation\n"), 0644); err != nil {
		t.Fatal(err)
	}
	helperGit(t, f.workspace, "add", ".gitignore")
	helperGit(t, f.workspace, "commit", "-m", "Ignore local preparation fixture")
	f.config.WorktreePreparation = []execution.Command{preparationCommand("first; literal argument")}
	p := admit(t, f.workspace, f.config, task("prepared"))
	done := make(chan error, 1)
	go func() {
		c := f.clients[0]
		w, err := c.ReceiveWork(f.ctx)
		if err == nil {
			b, e := os.ReadFile(filepath.Join(w.Assignment.Worktree, ".worktree-preparation"))
			if e != nil || string(b) != "first\n" {
				err = fmt.Errorf("assignment delivered before preparation: %q %v", b, e)
			}
		}
		if err == nil {
			err = report(c, f.ctx, w, agentprotocol.WorkerAcceptance, "")
		}
		if err == nil {
			err = writeWorkerResult(w)
		}
		if err == nil {
			err = report(c, f.ctx, w, agentprotocol.WorkerResult, "writers stopped")
		}
		done <- err
	}()
	r, err := Run(f.ctx, f.workspace, p.ID, f.config)
	if err != nil || r.Status != "AWAITING_REVIEW" {
		t.Fatalf("prepared result %+v %v", r, err)
	}
	if err := receive(t, f.ctx, done); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedEnrolledResultFinalizesAfterCrashDisconnect(t *testing.T) {
	f := enrolledFixture(t, 1)
	p := admit(t, f.workspace, f.config, task("checkpoint"))
	ref := f.clients[0].Binding()
	a, err := scheduler.Acquire(scheduler.Request{Workspace: f.workspace, Agent: "enrolled-worker", Role: "coding", Plan: p, TaskID: "checkpoint", Base: "main", Worker: &ref, Registry: f.registry})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.MarkWorkerSent(f.workspace, p.ID, a); err != nil {
		t.Fatal(err)
	}
	yes := true
	e := agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version4, Type: agentprotocol.WorkerAcceptance, AgentID: ref.AgentID, SessionID: ref.SessionID, Work: &agentprotocol.WorkerMessage{Correlation: a.WorkerDispatch.Correlation, Accepted: &yes}}
	if err := scheduler.WorkerEvent(f.workspace, ref, e); err != nil {
		t.Fatal(err)
	}
	w := agentprotocol.WorkerMessage{Assignment: &agentprotocol.WorkerAssignmentData{Worktree: a.Worktree, Task: a.Task}}
	if err := writeWorkerResult(w); err != nil {
		t.Fatal(err)
	}
	e.Type = agentprotocol.WorkerResult
	e.Work = &agentprotocol.WorkerMessage{Correlation: a.WorkerDispatch.Correlation, Detail: "writers stopped"}
	if err := scheduler.WorkerEvent(f.workspace, ref, e); err != nil {
		t.Fatal(err)
	}
	helperGit(t, a.Worktree, "add", "--", a.Task.Scope)
	helperGit(t, a.Worktree, "commit", "-m", "Implement checkpoint")
	snapshot, err := scheduler.Read(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	st := snapshot.Tasks[p.ID+"::checkpoint"]
	head, err := review.CheckOutput(st)
	if err != nil {
		t.Fatal(err)
	}
	in := input(f.workspace, p.ID, st)
	in.ValidatedHead = head
	in.Validation = "trusted checkpoint validator PASS"
	if err := review.RecordValidation(in); err != nil {
		t.Fatal(err)
	}
	if err := coordination.CompleteSession(f.workspace, a.SessionID, a.Token, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := f.clients[0].Disconnect(f.ctx); err != nil {
		t.Fatal(err)
	}
	// No transport/re-execution survives the simulated crash; only durable Go
	// validation plus operational COMPLETE can finalize this exact commit.
	f.config.Runtime = nil
	r, err := Run(f.ctx, f.workspace, p.ID, f.config)
	if err != nil || r.Status != "AWAITING_REVIEW" {
		t.Fatalf("checkpoint recovery %+v %v", r, err)
	}
	snapshot, err = scheduler.Read(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	st = snapshot.Tasks[p.ID+"::checkpoint"]
	if st.Status != "COMPLETE" || st.Attempt != 1 || st.ValidatedHead != head || snapshot.AgentRegistry.Sessions[ref.SessionID].Status != state.AgentDisconnected {
		t.Fatal("metadata recovery revived worker or replaced commit")
	}
}

func TestMissingWorkerDispatchFailsClosedOnReopen(t *testing.T) {
	f := enrolledFixture(t, 1)
	p := admit(t, f.workspace, f.config, task("a"))
	ref := f.clients[0].Binding()
	if _, err := scheduler.Acquire(scheduler.Request{Workspace: f.workspace, Agent: "enrolled-worker", Role: "coding", Plan: p, TaskID: "a", Base: "main", Worker: &ref, Registry: f.registry}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.workspace, "multi-agent-orchestrator", "state.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Error(err)
		}
	}()
	var s state.Snapshot
	if err := json.Unmarshal(original, &s); err != nil {
		t.Fatal(err)
	}
	st := s.Tasks[p.ID+"::a"]
	st.WorkerDispatch = nil
	s.Tasks[p.ID+"::a"] = st
	corrupt, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Read(f.workspace); err == nil {
		t.Fatal("missing dispatch silently treated as managed assignment")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(corrupt) {
		t.Fatal("corrupt state was silently repaired")
	}
}

type workerSendFailure struct{ agenttransport.Transport }

func (w workerSendFailure) Send(ctx context.Context, e agentprotocol.Envelope) error {
	if e.Type == agentprotocol.WorkerAssignment {
		return fmt.Errorf("injected assignment delivery failure")
	}
	return w.Transport.Send(ctx, e)
}

func TestEnrolledSendFailureBlocksDurableAssignment(t *testing.T) {
	f := enrolledFixture(t, 0)
	_, proof, err := f.server.Invite(agentattachment.InviteRequest{AgentID: "send-worker", Roles: []agentprotocol.Role{agentprotocol.RoleWorker}, Capabilities: []agentprotocol.Capability{"coding"}, TTL: time.Minute}, true)
	if err != nil {
		t.Fatal(err)
	}
	a, o, err := agenttransport.NewMemoryPair(2)
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() { ended <- f.server.Handle(f.ctx, workerSendFailure{o}) }()
	id := agentprotocol.Identity{ProtocolVersion: agentprotocol.Version4, AgentID: "send-worker", SessionID: "send-session", Metadata: agentprotocol.Metadata{Provider: "future-provider"}, Capabilities: []agentprotocol.Capability{"coding"}, SupportedRoles: []agentprotocol.Role{agentprotocol.RoleWorker}}
	c, err := agentattachment.Attach(f.ctx, a, id, proof)
	if err != nil {
		t.Fatal(err)
	}
	f.clients = append(f.clients, c)
	if err := f.registry.AssignRole(c.Binding(), agentprotocol.RoleWorker, "human authorized delivery test"); err != nil {
		t.Fatal(err)
	}
	if err := c.Heartbeat(f.ctx); err != nil {
		t.Fatal(err)
	}
	p := admit(t, f.workspace, f.config, task("undelivered"))
	r, err := Run(f.ctx, f.workspace, p.ID, f.config)
	if err != nil || r.Status == "AWAITING_REVIEW" {
		t.Fatalf("failed delivery: %+v %v", r, err)
	}
	if err := receive(t, f.ctx, ended); err == nil {
		t.Fatal("failed delivery left connection active")
	}
	s, err := scheduler.Read(f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	st := s.Tasks[p.ID+"::undelivered"]
	if st.Status != "BLOCKED" || st.WorkerDispatch.Phase != "FAILED" || st.WorkerDispatch.LastMessage != "" || st.Branch == "" || st.Worktree == "" || len(s.Results) != 0 {
		t.Fatalf("delivery failure lost authority/evidence: %+v", st)
	}
	if status, err := coordination.SessionStatus(f.workspace, st.SessionID); err != nil || status != "BLOCKED" {
		t.Fatalf("delivery failure split operational state: %s %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(st.Worktree, "undelivered", "result.txt")); !os.IsNotExist(err) {
		t.Fatal("failed delivery executed work")
	}
}

func TestEnrolledWorkerReusedOnlyAfterValidatedCompletion(t *testing.T) {
	f := enrolledFixture(t, 1)
	f.config.MaxWorkers = 1
	p := admit(t, f.workspace, f.config, task("first"), task("second", "first"), task("third"))
	done := make(chan error, 1)
	go func() {
		c := f.clients[0]
		var previous agentprotocol.WorkerMessage
		for i := 0; i < 3; i++ {
			w, err := c.ReceiveWork(f.ctx)
			if err != nil {
				done <- err
				return
			}
			s, err := scheduler.Read(f.workspace)
			if err != nil {
				done <- err
				return
			}
			assigned := 0
			for _, st := range s.Tasks {
				if st.Status == "ASSIGNED" {
					assigned++
				}
			}
			if assigned != 1 || i > 0 && s.Tasks[p.ID+"::"+previous.Correlation.TaskID].Status != "COMPLETE" {
				done <- fmt.Errorf("worker reused before validation/max_workers release")
				return
			}
			if w.Correlation.TaskID == "second" {
				first := s.Tasks[p.ID+"::first"]
				second := s.Tasks[p.ID+"::second"]
				if first.Status != "COMPLETE" || first.ValidatedHead != second.BaseSHA {
					done <- fmt.Errorf("dependency dispatched without validated prerequisite head")
					return
				}
			}
			if err = report(c, f.ctx, w, agentprotocol.WorkerAcceptance, ""); err == nil {
				err = writeWorkerResult(w)
			}
			if err == nil {
				err = report(c, f.ctx, w, agentprotocol.WorkerResult, "all writers stopped")
			}
			if err != nil {
				done <- err
				return
			}
			previous = w
		}
		done <- nil
	}()
	r, err := Run(f.ctx, f.workspace, p.ID, f.config)
	if err != nil || r.Status != "AWAITING_REVIEW" {
		t.Fatalf("worker reuse: %+v %v", r, err)
	}
	if err := receive(t, f.ctx, done); err != nil {
		t.Fatal(err)
	}
	s, err := scheduler.Read(f.workspace)
	if err != nil || s.AgentRegistry.Sessions[f.clients[0].Binding().SessionID].Status != state.AgentReady || len(s.Results) != 3 {
		t.Fatal("reused worker failed to return READY with three validated results", err)
	}
}
