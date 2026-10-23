package agentattachment

import (
	"context"
	"io"
	"sync"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agenttransport"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
)

func (s *Server) handleStream(ctx context.Context, conn io.ReadWriteCloser) error {
	t, err := agenttransport.NewStream(conn, agentprotocol.FromOrchestrator)
	if err != nil {
		_ = conn.Close()
		return err
	}
	return s.Handle(ctx, t)
}

// Client moves lifecycle, Planner and worker messages without reasoning, task
// execution or role authority. Lifecycle RPCs serialize; one consumer per request
// mailbox may operate concurrently with heartbeat/reporting/Close. Close joins its
// receive pump. Transport cancellation and terminal errors are not retried.
type Client struct {
	transport agenttransport.Transport
	ref       agentregistry.Ref
	gate      chan struct{}
	version   agentprotocol.ProtocolVersion
	acks      chan agentprotocol.Envelope
	work      chan agentprotocol.WorkerMessage
	requests  chan agentprotocol.PlanningRequest
	done      chan struct{}
	mu        sync.Mutex
	err       error
}

func DialLocal(ctx context.Context, path string, id agentprotocol.Identity, proof agentprotocol.InvitationProof) (*Client, error) {
	if id.ProtocolVersion == agentprotocol.Version1 || id.Validate() != nil || proof.Validate() != nil {
		return nil, ErrAdmission
	}
	conn, err := dialLocal(ctx, path)
	if err != nil {
		return nil, err
	}
	t, err := agenttransport.NewStream(conn, agentprotocol.FromAgent)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return Attach(ctx, t, id, proof)
}

// Attach owns an unbound transport. The returned Ref is informational client-side
// evidence; the server keeps its own binding and never trusts client generation.
func Attach(ctx context.Context, t agenttransport.Transport, id agentprotocol.Identity, proof agentprotocol.InvitationProof) (*Client, error) {
	if id.ProtocolVersion == agentprotocol.Version1 || id.Validate() != nil || proof.Validate() != nil {
		_ = t.Close()
		return nil, ErrAdmission
	}
	e := agentprotocol.Envelope{ProtocolVersion: id.ProtocolVersion, Type: agentprotocol.EnrollmentRequest, AgentID: id.AgentID, SessionID: id.SessionID, Identity: &id, Invitation: &proof}
	if err := t.Send(ctx, e); err != nil {
		_ = t.Close()
		return nil, err
	}
	r, err := t.Receive(ctx)
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	if r.ValidateFrom(agentprotocol.FromOrchestrator) != nil || r.ProtocolVersion != id.ProtocolVersion || r.Type != agentprotocol.EnrollmentResponse ||
		r.AgentID != id.AgentID || r.SessionID != id.SessionID || r.Enrollment.State != agentprotocol.EnrollmentAdmitted {
		_ = t.Close()
		return nil, ErrAdmission
	}
	c := &Client{work: make(chan agentprotocol.WorkerMessage, 1), transport: t, version: id.ProtocolVersion, acks: make(chan agentprotocol.Envelope, 1), requests: make(chan agentprotocol.PlanningRequest, 1), done: make(chan struct{}), ref: agentregistry.Ref{AgentID: id.AgentID, SessionID: id.SessionID, Generation: agentregistry.Generation(r.Enrollment.SessionGeneration)}, gate: make(chan struct{}, 1)}
	go c.receive()
	return c, nil
}

func (c *Client) Binding() agentregistry.Ref          { return c.ref }
func (c *Client) Heartbeat(ctx context.Context) error { return c.call(ctx, agentprotocol.Heartbeat) }
func (c *Client) Disconnect(ctx context.Context) error {
	defer c.Close()
	return c.call(ctx, agentprotocol.Disconnect)
}
func (c *Client) Close() error { err := c.transport.Close(); <-c.done; return err }

func (c *Client) call(ctx context.Context, kind agentprotocol.MessageType) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.gate }()
	e := agentprotocol.Envelope{ProtocolVersion: c.version, Type: kind, AgentID: c.ref.AgentID, SessionID: c.ref.SessionID}
	if err := c.transport.Send(ctx, e); err != nil {
		return err
	}
	var a agentprotocol.Envelope
	select {
	case a = <-c.acks:
	case <-c.done:
		return c.failure()
	case <-ctx.Done():
		_ = c.Close()
		return ctx.Err()
	}
	if a.ValidateFrom(agentprotocol.FromOrchestrator) != nil || a.ProtocolVersion != c.version || a.Type != agentprotocol.SessionAcknowledgment || a.AgentID != c.ref.AgentID ||
		a.SessionID != c.ref.SessionID || a.Acknowledgment.For != kind || a.Acknowledgment.SessionGeneration != uint64(c.ref.Generation) {
		_ = c.Close()
		return ErrBinding
	}
	return nil
}

// receive is the sole reader for this client. Bounded mailboxes fail closed on
// unsolicited/flooded messages; no protocol queue can grow without limit.
func (c *Client) receive() {
	defer close(c.done)
	for {
		e, err := c.transport.Receive(context.Background())
		if err == nil && (e.ValidateFrom(agentprotocol.FromOrchestrator) != nil || e.ProtocolVersion != c.version || e.AgentID != c.ref.AgentID || e.SessionID != c.ref.SessionID) {
			err = ErrBinding
		}
		if err == nil {
			switch e.Type {
			case agentprotocol.SessionAcknowledgment:
				select {
				case c.acks <- e:
				default:
					err = ErrBinding
				}
			case agentprotocol.WorkerAssignment:
				select {
				case c.work <- *e.Work:
				default:
					err = ErrBinding
				}
			case agentprotocol.PlannerRequest:
				select {
				case c.requests <- *e.PlanningRequest:
				default:
					err = ErrBinding
				}
			default:
				err = ErrUnsupported
			}
		}
		if err != nil {
			c.mu.Lock()
			c.err = err
			c.mu.Unlock()
			_ = c.transport.Close()
			return
		}
	}
}
func (c *Client) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	return ErrClosed
}
func (c *Client) ReceivePlanning(ctx context.Context) (agentprotocol.PlanningRequest, error) {
	select {
	case <-c.done:
		return agentprotocol.PlanningRequest{}, c.failure()
	default:
	}
	select {
	case r := <-c.requests:
		return r, nil
	case <-ctx.Done():
		return agentprotocol.PlanningRequest{}, ctx.Err()
	case <-c.done:
		return agentprotocol.PlanningRequest{}, c.failure()
	}
}

// RespondPlanning only sends a proposal. The server verifies the authoritative
// selection/request/session and still requires normal Go/human plan adoption.
func (c *Client) RespondPlanning(ctx context.Context, r agentprotocol.PlanningRequest, p planning.Proposal) error {
	response := agentprotocol.PlanningResponse{Correlation: r.Correlation, Proposal: p}
	return c.transport.Send(ctx, agentprotocol.Envelope{ProtocolVersion: c.version, Type: agentprotocol.PlannerResponse, AgentID: c.ref.AgentID, SessionID: c.ref.SessionID, PlanningResponse: &response})
}
