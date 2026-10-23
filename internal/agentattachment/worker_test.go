package agentattachment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agenttransport"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
)

func workMessage() agentprotocol.WorkerMessage {
	return agentprotocol.WorkerMessage{Correlation: agentprotocol.WorkerCorrelation{ObjectiveID: "o", TaskID: "t", Generation: 7, DispatchID: "go-id"}, Assignment: &agentprotocol.WorkerAssignmentData{Objective: "bounded fixture", Task: planning.Task{ID: "t", Scope: "docs", Description: "write", Validation: []string{"check"}}, Worktree: "/tmp/assigned", Branch: "agent/t", Rules: "leave uncommitted; Go owns authority"}}
}
func TestWorkerRoutingRequiresPendingExactSession(t *testing.T) {
	f := setup(t, nil)
	_, proof := invite(t, f, "worker", false)
	id := identity("worker", "session")
	id.ProtocolVersion = agentprotocol.Version4
	c, done := attach(t, f, id, proof)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	handled := make(chan agentprotocol.MessageType, 3)
	go func() {
		_, err := f.s.Work(ctx, c.Binding(), workMessage(), time.Second, func(_ context.Context, e agentprotocol.Envelope) error { handled <- e.Type; return nil })
		result <- err
	}()
	w, err := c.ReceiveWork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if w.Correlation != workMessage().Correlation {
		t.Fatal("transport changed assignment")
	}
	yes := true
	if err := c.ReportWork(ctx, agentprotocol.WorkerAcceptance, agentprotocol.WorkerMessage{Correlation: w.Correlation, Accepted: &yes}); err != nil {
		t.Fatal(err)
	}
	if err := c.Heartbeat(ctx); err != nil {
		t.Fatal("worker receive competed with heartbeat", err)
	}
	if err := c.ReportWork(ctx, agentprotocol.WorkerResult, agentprotocol.WorkerMessage{Correlation: w.Correlation, Detail: "all writers stopped"}); err != nil {
		t.Fatal(err)
	}
	if err := await(t, result); err != nil {
		t.Fatal(err)
	}
	if await(t, handled) != agentprotocol.WorkerAcceptance || await(t, handled) != agentprotocol.WorkerResult {
		t.Fatal("routing order corrupted")
	}
	if err := c.ReportWork(ctx, agentprotocol.WorkerResult, agentprotocol.WorkerMessage{Correlation: w.Correlation, Detail: "replayed"}); err != nil {
		t.Fatal(err)
	}
	if err := await(t, done); err == nil {
		t.Fatal("duplicate terminal report accepted")
	}
}

type failAssignmentSend struct{ agenttransport.Transport }

func (t failAssignmentSend) Send(ctx context.Context, e agentprotocol.Envelope) error {
	if e.Type == agentprotocol.WorkerAssignment {
		return errors.New("injected send failure")
	}
	return t.Transport.Send(ctx, e)
}
func TestWorkerSendFailureClosesBoundExchange(t *testing.T) {
	f := setup(t, nil)
	_, proof := invite(t, f, "worker", false)
	id := identity("worker", "session")
	id.ProtocolVersion = agentprotocol.Version4
	a, o, err := agenttransport.NewMemoryPair(2)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.s.Handle(context.Background(), failAssignmentSend{o}) }()
	c, err := Attach(context.Background(), a, id, proof)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Work(context.Background(), c.Binding(), workMessage(), time.Second, func(context.Context, agentprotocol.Envelope) error {
		t.Error("failed send reached handler")
		return nil
	}); err == nil {
		t.Fatal("send failure hidden")
	}
	if err := await(t, done); err == nil {
		t.Fatal("failed exchange stayed connected")
	}
}

func TestWorkerAcceptanceDeadlineCancelsReportPersistence(t *testing.T) {
	deadline := make(chan context.CancelFunc, 1)
	f := setup(t, func(c *Config) {
		c.WithTimeout = func(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
			if d == 7*time.Second {
				ctx, cancel := context.WithCancel(parent)
				deadline <- cancel
				return ctx, cancel
			}
			return context.WithTimeout(parent, d)
		}
	})
	_, proof := invite(t, f, "worker", false)
	id := identity("worker", "session")
	id.ProtocolVersion = agentprotocol.Version4
	c, ended := attach(t, f, id, proof)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	entered, exited := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := f.s.Work(ctx, c.Binding(), workMessage(), 7*time.Second, func(reportCtx context.Context, _ agentprotocol.Envelope) error {
			close(entered)
			<-reportCtx.Done() // Simulate contended persistence awaiting cancellation.
			close(exited)
			return reportCtx.Err()
		})
		result <- err
	}()
	w, err := c.ReceiveWork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	if err := c.ReportWork(ctx, agentprotocol.WorkerAcceptance, agentprotocol.WorkerMessage{Correlation: w.Correlation, Accepted: &yes}); err != nil {
		t.Fatal(err)
	}
	await(t, entered)
	stop := await(t, deadline)
	stop()
	if err := await(t, result); !errors.Is(err, context.Canceled) {
		t.Fatal("acceptance expiry failed to stop exchange", err)
	}
	await(t, exited)
	if err := await(t, ended); err == nil {
		t.Fatal("timed-out report left route active")
	}
}
