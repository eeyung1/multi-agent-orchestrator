package agentattachment

import (
	"context"
	"errors"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
)

type workerExchange struct {
	ctx          context.Context
	correlation  agentprotocol.WorkerCorrelation
	handle       func(context.Context, agentprotocol.Envelope) error
	accepted     chan agentprotocol.Envelope
	terminal     chan agentprotocol.Envelope
	acceptedOnce bool
	terminalOnce bool
}

// Work moves one conversation on an already-bound session. The trusted handler
// MUST verify durable scheduler ownership for every inbound report. This routing
// helper owns no tasks, generations, role decisions, or claims. The server's sole
// receive loop also continues servicing independent heartbeats.
func (s *Server) Work(ctx context.Context, ref agentregistry.Ref, w agentprotocol.WorkerMessage, acceptTimeout time.Duration, handle func(context.Context, agentprotocol.Envelope) error) (agentprotocol.Envelope, error) {
	if handle == nil || acceptTimeout <= 0 || s.registry.Check(ref) != nil {
		return agentprotocol.Envelope{}, ErrBinding
	}
	s.routesMu.Lock()
	conn := s.routes[ref]
	s.routesMu.Unlock()
	if conn == nil || conn.version != agentprotocol.Version4 {
		return agentprotocol.Envelope{}, ErrBinding
	}
	select {
	case <-conn.ready:
	case <-ctx.Done():
		return agentprotocol.Envelope{}, ctx.Err()
	case <-conn.done:
		return agentprotocol.Envelope{}, ErrBinding
	}
	exchange, stopExchange := context.WithCancel(ctx)
	defer stopExchange()
	x := &workerExchange{ctx: exchange, correlation: w.Correlation, handle: handle, accepted: make(chan agentprotocol.Envelope, 1), terminal: make(chan agentprotocol.Envelope, 1)}
	conn.mu.Lock()
	if conn.work != nil || conn.pending != nil {
		conn.mu.Unlock()
		return agentprotocol.Envelope{}, ErrBinding
	}
	conn.work = x
	conn.mu.Unlock()
	defer func() {
		// Cancel report persistence before joining its handler. This also bounds
		// lock-contention retries when acceptance expires or the route closes.
		stopExchange()
		conn.mu.Lock()
		conn.work = nil
		conn.mu.Unlock()
	}()
	// Any failed/abandoned exchange closes this route. A new admitted session is
	// required; an uncertain external writer cannot silently receive another task.
	completed := false
	defer func() {
		if !completed {
			_ = conn.transport.Close()
		}
	}()
	accept, cancel := s.c.WithTimeout(ctx, acceptTimeout)
	defer cancel()
	e := agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version4, Type: agentprotocol.WorkerAssignment, AgentID: ref.AgentID, SessionID: ref.SessionID, Work: &w}
	if err := conn.transport.Send(accept, e); err != nil {
		return agentprotocol.Envelope{}, err
	}
	select {
	case acceptance := <-x.accepted:
		if err := accept.Err(); err != nil {
			return agentprotocol.Envelope{}, err
		}
		if !*acceptance.Work.Accepted {
			completed = true
			return acceptance, errors.New("worker rejected assignment: " + acceptance.Work.Detail)
		}
	case <-accept.Done():
		return agentprotocol.Envelope{}, accept.Err()
	case <-conn.done:
		return agentprotocol.Envelope{}, ErrBinding
	}
	cancel()
	select {
	case e := <-x.terminal:
		if err := ctx.Err(); err != nil {
			return agentprotocol.Envelope{}, err
		}
		if s.registry.Check(ref) != nil {
			return agentprotocol.Envelope{}, ErrBinding
		}
		completed = true
		if e.Type == agentprotocol.WorkerBlocker {
			return e, errors.New("worker reported blocker: " + e.Work.Detail)
		}
		return e, nil
	case <-ctx.Done():
		return agentprotocol.Envelope{}, ctx.Err()
	case <-conn.done:
		return agentprotocol.Envelope{}, ErrBinding
	}
}
func (s *Server) workerResponse(ref agentregistry.Ref, e agentprotocol.Envelope) error {
	s.routesMu.Lock()
	conn := s.routes[ref]
	s.routesMu.Unlock()
	if conn == nil || e.Work == nil {
		return ErrBinding
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	x := conn.work
	if x == nil || x.correlation != e.Work.Correlation || x.terminalOnce {
		return ErrBinding
	}
	switch e.Type {
	case agentprotocol.WorkerAcceptance:
		if x.acceptedOnce {
			return ErrBinding
		}
	case agentprotocol.WorkerProgress, agentprotocol.WorkerBlocker, agentprotocol.WorkerResult:
		if !x.acceptedOnce {
			return ErrBinding
		}
	default:
		return ErrUnsupported
	}
	if err := x.handle(x.ctx, e); err != nil {
		return errors.Join(ErrBinding, err)
	}
	switch e.Type {
	case agentprotocol.WorkerAcceptance:
		x.acceptedOnce = true
		x.accepted <- e
		if !*e.Work.Accepted {
			x.terminalOnce = true
		}
	case agentprotocol.WorkerBlocker, agentprotocol.WorkerResult:
		x.terminalOnce = true
		x.terminal <- e
	}
	return nil
}
func (c *Client) ReceiveWork(ctx context.Context) (agentprotocol.WorkerMessage, error) {
	select {
	case <-c.done:
		return agentprotocol.WorkerMessage{}, c.failure()
	default:
	}
	select {
	case w := <-c.work:
		return w, nil
	case <-ctx.Done():
		return agentprotocol.WorkerMessage{}, ctx.Err()
	case <-c.done:
		return agentprotocol.WorkerMessage{}, c.failure()
	}
}

// ReportWork only reports assertions; Go verifies session/incarnation/assignment
// and owns all state transitions. Result/blocker/rejection relinquishes writing.
func (c *Client) ReportWork(ctx context.Context, kind agentprotocol.MessageType, w agentprotocol.WorkerMessage) error {
	return c.transport.Send(ctx, agentprotocol.Envelope{ProtocolVersion: c.version, Type: kind, AgentID: c.ref.AgentID, SessionID: c.ref.SessionID, Work: &w})
}
