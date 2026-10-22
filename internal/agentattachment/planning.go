package agentattachment

import (
	"context"
	"sync"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agenttransport"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
)

// Routing is process-local; selection, role and request authority are durable in
// the registry. Routes cannot grant authority or accept an unsolicited response.
type plannerConnection struct {
	version   agentprotocol.ProtocolVersion
	transport agenttransport.Transport
	done      chan struct{}
	ready     chan struct{}
	mu        sync.Mutex
	pending   *agentprotocol.PlannerCorrelation
	response  chan planning.Proposal
}

// Connected reports routing availability only, never selection/role authority.
func (s *Server) Connected(ref agentregistry.Ref) bool {
	s.routesMu.Lock()
	defer s.routesMu.Unlock()
	conn := s.routes[ref]
	if conn == nil {
		return false
	}
	select {
	case <-conn.ready:
		return true
	default:
		return false
	}
}

func (s *Server) RequestPlanning(ctx context.Context, ref agentregistry.Ref, r agentprotocol.PlanningRequest) (planning.Proposal, error) {
	if r.Validate() != nil || s.registry.CheckPlanner(ref, r.Correlation, false) != nil {
		return planning.Proposal{}, ErrBinding
	}
	s.routesMu.Lock()
	conn := s.routes[ref]
	s.routesMu.Unlock()
	if conn == nil {
		return planning.Proposal{}, ErrBinding
	}
	select {
	case <-conn.ready:
	case <-ctx.Done():
		return planning.Proposal{}, ctx.Err()
	case <-conn.done:
		return planning.Proposal{}, ErrBinding
	}
	conn.mu.Lock()
	if conn.pending != nil {
		conn.mu.Unlock()
		return planning.Proposal{}, ErrBinding
	}
	c := r.Correlation
	conn.pending = &c
	conn.response = make(chan planning.Proposal, 1)
	ch := conn.response
	conn.mu.Unlock()
	defer func() { conn.mu.Lock(); conn.pending = nil; conn.response = nil; conn.mu.Unlock() }()
	e := agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version3, Type: agentprotocol.PlannerRequest, AgentID: ref.AgentID, SessionID: ref.SessionID, PlanningRequest: &r}
	if err := conn.transport.Send(ctx, e); err != nil {
		return planning.Proposal{}, err
	}
	select {
	case p := <-ch:
		if err := ctx.Err(); err != nil {
			return planning.Proposal{}, err
		}
		if err := s.registry.CheckPlanner(ref, c, true); err != nil {
			return planning.Proposal{}, ErrBinding
		}
		return p, nil
	case <-ctx.Done():
		return planning.Proposal{}, ctx.Err()
	case <-conn.done:
		return planning.Proposal{}, ErrBinding
	}
}

func (s *Server) plannerResponse(ref agentregistry.Ref, e agentprotocol.Envelope) error {
	s.routesMu.Lock()
	conn := s.routes[ref]
	s.routesMu.Unlock()
	if conn == nil || e.PlanningResponse == nil {
		return ErrBinding
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.pending == nil || *conn.pending != e.PlanningResponse.Correlation || conn.response == nil {
		return ErrBinding
	}
	if err := s.registry.AcceptPlannerResponse(ref, *e.PlanningResponse); err != nil {
		return ErrBinding
	}
	select {
	case conn.response <- e.PlanningResponse.Proposal:
		conn.response = nil
		return nil
	default:
		return ErrBinding
	}
}
