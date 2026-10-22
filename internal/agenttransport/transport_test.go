package agenttransport

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
)

func heartbeat() agentprotocol.Envelope {
	return agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version1,
		Type: agentprotocol.Heartbeat, AgentID: "logical-agent", SessionID: "runtime-session"}
}

func assignment() agentprotocol.Envelope {
	e := heartbeat()
	e.Type, e.Role = agentprotocol.Assignment, agentprotocol.RoleWorker
	e.Assignment = &agentprotocol.AssignmentRef{ObjectiveID: "objective", TaskID: "task",
		AssignmentGeneration: ^uint64(0), Token: "private-claim-fixture", SessionID: e.SessionID}
	return e
}

func advertisement() agentprotocol.Envelope {
	e := heartbeat()
	e.Type = agentprotocol.EnrollmentRequest
	e.Identity = &agentprotocol.Identity{ProtocolVersion: e.ProtocolVersion, AgentID: e.AgentID, SessionID: e.SessionID,
		Metadata:       agentprotocol.Metadata{Provider: "unknown-future-provider", Runtime: "PLANNER"},
		Capabilities:   []agentprotocol.Capability{"future.example/coding:v7"},
		SupportedRoles: []agentprotocol.Role{agentprotocol.RolePlanner, agentprotocol.RoleWorker}}
	return e
}

func memoryPair(t *testing.T, capacity int) (Transport, Transport) {
	t.Helper()
	a, o, err := NewMemoryPair(capacity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(); _ = o.Close() })
	return a, o
}

// Done is reached by a memory operation only when it waits for queue capacity
// or a frame. Tests synchronize with that wait rather than relying on sleeps.
type waitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *waitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestMemoryConversationPreservesIdentityAndOwnership(t *testing.T) {
	a, o := memoryPair(t, 1)
	ctx := context.Background()
	e := advertisement()
	if err := a.Send(ctx, e); err != nil {
		t.Fatal(err)
	}
	e.Identity.Metadata.Provider = "mutated-after-send"
	got, err := o.Receive(ctx)
	if err != nil || !reflect.DeepEqual(got, advertisement()) {
		t.Fatalf("advertisement changed: %v", err)
	}
	if got.Role != "" {
		t.Fatal("provider metadata assigned a role")
	}
	want := assignment()
	if err := o.Send(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err = a.Receive(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("assignment identity changed: %v", err)
	}
	// A reference is forwarded unchanged, including its scheduler generation; no
	// registry/scheduler API or authoritative state is accessed by this package.
	got.Type, got.Role, got.Detail = agentprotocol.Result, "", "reported result"
	if err := a.Send(ctx, got); err != nil {
		t.Fatal(err)
	}
	report, err := o.Receive(ctx)
	if err != nil || !reflect.DeepEqual(got, report) {
		t.Fatalf("result changed: %v", err)
	}
}

func TestMemoryRejectsMalformedUnsupportedAndWrongDirection(t *testing.T) {
	a, o := memoryPair(t, 1)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		e    agentprotocol.Envelope
		want error
	}{
		{"empty", agentprotocol.Envelope{}, ErrInvalidMessage},
		{"version", func() agentprotocol.Envelope { e := heartbeat(); e.ProtocolVersion = "future/v99"; return e }(), ErrUnsupportedMessage},
		{"type", func() agentprotocol.Envelope { e := heartbeat(); e.Type = "self-authorize"; return e }(), ErrUnsupportedMessage},
		{"agent assignment", assignment(), ErrInvalidMessage},
		{"agent role", func() agentprotocol.Envelope {
			e := heartbeat()
			e.Type, e.Role = agentprotocol.RoleAssignment, agentprotocol.RolePlanner
			return e
		}(), ErrInvalidMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := a.Send(ctx, tc.e); !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
	if err := o.Send(ctx, heartbeat()); !errors.Is(err, ErrInvalidMessage) {
		t.Fatal("Go endpoint sent an agent heartbeat")
	}
	if err := a.Send(ctx, heartbeat()); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Receive(ctx); err != nil {
		t.Fatal("invalid outgoing send damaged connection")
	}
}

func TestMemoryFIFOAndBoundedBackpressure(t *testing.T) {
	a, o := memoryPair(t, 2)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		e := heartbeat()
		e.AgentID = agentprotocol.AgentID(fmt.Sprint(i))
		if err := a.Send(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	parent, cancel := context.WithCancel(ctx)
	defer cancel()
	wait := &waitingContext{Context: parent, waiting: make(chan struct{})}
	errCh := make(chan error, 1)
	go func() { errCh <- a.Send(wait, heartbeat()) }()
	<-wait.waiting
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		e, err := o.Receive(ctx)
		if err != nil || e.AgentID != agentprotocol.AgentID(fmt.Sprint(i)) {
			t.Fatalf("FIFO: %v", err)
		}
	}
	// Cancellation did not enqueue the third frame or close the connection.
	if err := a.Send(ctx, heartbeat()); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Receive(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryBlockedOperationsWake(t *testing.T) {
	for _, send := range []bool{false, true} {
		for _, closePeer := range []bool{false, true} {
			t.Run(fmt.Sprintf("send=%t/close=%t", send, closePeer), func(t *testing.T) {
				a, o := memoryPair(t, 1)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				wait := &waitingContext{Context: ctx, waiting: make(chan struct{})}
				if send {
					if err := a.Send(context.Background(), heartbeat()); err != nil {
						t.Fatal(err)
					}
				}
				done := make(chan error, 1)
				go func() {
					if send {
						done <- a.Send(wait, heartbeat())
					} else {
						_, err := a.Receive(wait)
						done <- err
					}
				}()
				<-wait.waiting
				want := error(context.Canceled)
				if closePeer {
					if err := o.Close(); err != nil {
						t.Fatal(err)
					}
					want = ErrClosed
				} else {
					cancel()
				}
				if err := <-done; !errors.Is(err, want) {
					t.Fatalf("expected %v, got %v", want, err)
				}
			})
		}
	}
}

func TestMemoryCloseDiscardsQueuedFramesAndIsIdempotent(t *testing.T) {
	a, o := memoryPair(t, 1)
	ctx := context.Background()
	if err := a.Send(ctx, heartbeat()); err != nil {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []Transport{a, o} {
		if err := endpoint.Close(); err != nil {
			t.Fatal(err)
		}
		if err := endpoint.Send(ctx, heartbeat()); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
		if _, err := endpoint.Receive(ctx); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	}
}

func TestMemoryConcurrentSendsAndReceives(t *testing.T) {
	a, o := memoryPair(t, 4)
	const count = 40
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := heartbeat()
			e.AgentID = agentprotocol.AgentID(fmt.Sprint(i))
			errs <- a.Send(context.Background(), e)
		}(i)
	}
	seen := map[agentprotocol.AgentID]bool{}
	for i := 0; i < count; i++ {
		e, err := o.Receive(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if seen[e.AgentID] {
			t.Fatal("duplicate delivery")
		}
		seen[e.AgentID] = true
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestMemoryPreCanceledAndCapacityPolicy(t *testing.T) {
	for _, capacity := range []int{-1, 0, MaxMemoryCapacity + 1} {
		if _, _, err := NewMemoryPair(capacity); err == nil {
			t.Fatal("invalid capacity accepted")
		}
	}
	a, o := memoryPair(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Send(ctx, heartbeat()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := a.Send(context.Background(), heartbeat()); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Receive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := o.Receive(context.Background()); err != nil {
		t.Fatal("canceled receive consumed a frame")
	}
}

func TestConcurrentReceiversAndClose(t *testing.T) {
	for name, pair := range map[string]func(*testing.T) (Transport, Transport){
		"memory": func(t *testing.T) (Transport, Transport) { return memoryPair(t, 2) },
		"stream": streamPair,
	} {
		t.Run(name, func(t *testing.T) {
			a, o := pair(t)
			const count = 16
			messages := make(chan agentprotocol.Envelope, count)
			errs := make(chan error, count)
			for i := 0; i < count; i++ {
				go func() { e, err := o.Receive(context.Background()); messages <- e; errs <- err }()
			}
			for i := 0; i < count; i++ {
				e := heartbeat()
				e.AgentID = agentprotocol.AgentID(fmt.Sprint(i))
				if err := a.Send(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			}
			seen := map[agentprotocol.AgentID]bool{}
			for i := 0; i < count; i++ {
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
				e := <-messages
				if seen[e.AgentID] {
					t.Fatal("concurrent readers duplicated a frame")
				}
				seen[e.AgentID] = true
			}
			var wg sync.WaitGroup
			for i := 0; i < count; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); _ = a.Close(); _ = o.Close() }()
			}
			wg.Wait()
			if err := a.Send(context.Background(), heartbeat()); !errors.Is(err, ErrClosed) {
				t.Fatal(err)
			}
		})
	}
}
