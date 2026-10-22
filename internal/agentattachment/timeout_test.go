package agentattachment

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agenttransport"
)

// An explicit deadline event, not a sleep or dependence on wall-clock scheduling.
type manualDeadline struct {
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func (c *manualDeadline) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *manualDeadline) Done() <-chan struct{}       { return c.done }
func (c *manualDeadline) Err() error                  { c.mu.Lock(); defer c.mu.Unlock(); return c.err }
func (c *manualDeadline) Value(any) any               { return nil }
func (c *manualDeadline) finish(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
		close(c.done)
	}
}

type observedTransport struct {
	agenttransport.Transport
	entered chan struct{}
	once    sync.Once
}

func (t *observedTransport) Receive(ctx context.Context) (agentprotocol.Envelope, error) {
	t.once.Do(func() { close(t.entered) })
	return t.Transport.Receive(ctx)
}

func TestHandshakeDeadlineRejectsUnboundPeerWithoutEnrollment(t *testing.T) {
	deadlines := make(chan *manualDeadline, 1)
	f := setup(t, func(c *Config) {
		c.WithTimeout = func(_ context.Context, d time.Duration) (context.Context, context.CancelFunc) {
			if d != 10*time.Second {
				t.Error("wrong handshake policy")
			}
			m := &manualDeadline{done: make(chan struct{})}
			deadlines <- m
			return m, func() { m.finish(context.Canceled) }
		}
	})
	a, o, err := agenttransport.NewMemoryPair(1)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	observed := &observedTransport{Transport: o, entered: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- f.s.Handle(context.Background(), observed) }()
	m := await(t, deadlines)
	await(t, observed.entered)
	m.finish(context.DeadlineExceeded)
	if err := await(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	if len(readState(t, f).AgentRegistry.Sessions) != 0 {
		t.Fatal("handshake timeout enrolled session")
	}
}

func TestBoundIdleDeadlineDisconnectsExactSession(t *testing.T) {
	deadlines := make(chan *manualDeadline, 2)
	f := setup(t, func(c *Config) {
		c.WithTimeout = func(_ context.Context, _ time.Duration) (context.Context, context.CancelFunc) {
			m := &manualDeadline{done: make(chan struct{})}
			deadlines <- m
			return m, func() { m.finish(context.Canceled) }
		}
	})
	_, proof := invite(t, f, "agent", false)
	c, done := attach(t, f, identity("agent", "run"), proof)
	_ = await(t, deadlines)
	idle := await(t, deadlines)
	idle.finish(context.DeadlineExceeded)
	if err := await(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if record(t, f, c.Binding().SessionID).Session.Status != agentregistry.Disconnected {
		t.Fatal("idle timeout left session eligible")
	}
}

func TestPreEnrollmentNonEnrollmentMessageRejected(t *testing.T) {
	f := setup(t, nil)
	a, done := connection(t, f)
	if err := a.Send(context.Background(), agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version2, Type: agentprotocol.Heartbeat, AgentID: "agent", SessionID: "run"}); err != nil {
		t.Fatal(err)
	}
	if err := await(t, done); !errors.Is(err, ErrAdmission) {
		t.Fatal(err)
	}
	if len(readState(t, f).AgentRegistry.Sessions) != 0 {
		t.Fatal("heartbeat self-enrolled")
	}
}
