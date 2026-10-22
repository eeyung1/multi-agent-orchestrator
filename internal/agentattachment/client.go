package agentattachment

import (
	"context"
	"io"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agenttransport"
)

func (s *Server) handleStream(ctx context.Context, conn io.ReadWriteCloser) error {
	t, err := agenttransport.NewStream(conn, agentprotocol.FromOrchestrator)
	if err != nil {
		_ = conn.Close()
		return err
	}
	return s.Handle(ctx, t)
}

// Client is the minimal provider-neutral bridge foundation. It does not execute
// a Planner/task or grant roles. Lifecycle RPCs serialize to match acknowledgments.
type Client struct {
	transport agenttransport.Transport
	ref       agentregistry.Ref
	gate      chan struct{}
}

func DialLocal(ctx context.Context, path string, id agentprotocol.Identity, proof agentprotocol.InvitationProof) (*Client, error) {
	if id.ProtocolVersion != agentprotocol.Version2 || id.Validate() != nil || proof.Validate() != nil {
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
	if id.ProtocolVersion != agentprotocol.Version2 || id.Validate() != nil || proof.Validate() != nil {
		_ = t.Close()
		return nil, ErrAdmission
	}
	e := agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version2, Type: agentprotocol.EnrollmentRequest, AgentID: id.AgentID, SessionID: id.SessionID, Identity: &id, Invitation: &proof}
	if err := t.Send(ctx, e); err != nil {
		_ = t.Close()
		return nil, err
	}
	r, err := t.Receive(ctx)
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	if r.ValidateFrom(agentprotocol.FromOrchestrator) != nil || r.ProtocolVersion != agentprotocol.Version2 || r.Type != agentprotocol.EnrollmentResponse ||
		r.AgentID != id.AgentID || r.SessionID != id.SessionID || r.Enrollment.State != agentprotocol.EnrollmentAdmitted {
		_ = t.Close()
		return nil, ErrAdmission
	}
	return &Client{transport: t, ref: agentregistry.Ref{AgentID: id.AgentID, SessionID: id.SessionID, Generation: agentregistry.Generation(r.Enrollment.SessionGeneration)}, gate: make(chan struct{}, 1)}, nil
}

func (c *Client) Binding() agentregistry.Ref          { return c.ref }
func (c *Client) Heartbeat(ctx context.Context) error { return c.call(ctx, agentprotocol.Heartbeat) }
func (c *Client) Disconnect(ctx context.Context) error {
	defer c.Close()
	return c.call(ctx, agentprotocol.Disconnect)
}
func (c *Client) Close() error { return c.transport.Close() }

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
	e := agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version2, Type: kind, AgentID: c.ref.AgentID, SessionID: c.ref.SessionID}
	if err := c.transport.Send(ctx, e); err != nil {
		return err
	}
	a, err := c.transport.Receive(ctx)
	if err != nil {
		_ = c.Close()
		return err
	}
	if a.ValidateFrom(agentprotocol.FromOrchestrator) != nil || a.ProtocolVersion != agentprotocol.Version2 || a.Type != agentprotocol.SessionAcknowledgment || a.AgentID != c.ref.AgentID ||
		a.SessionID != c.ref.SessionID || a.Acknowledgment.For != kind || a.Acknowledgment.SessionGeneration != uint64(c.ref.Generation) {
		_ = c.Close()
		return ErrBinding
	}
	return nil
}
