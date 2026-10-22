// Package agentattachment admits compatible local clients and binds connections
// to Go-issued registry refs. It has no scheduler, task, role-grant or execution API.
package agentattachment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agenttransport"
)

var (
	ErrAdmission   = errors.New("local agent admission denied")
	ErrBinding     = errors.New("local agent session binding rejected")
	ErrUnsupported = errors.New("message is not supported by local attachment lifecycle")
	ErrRegistry    = errors.New("local attachment registry operation failed; inspect authoritative state")
	ErrClosed      = errors.New("local attachment service closed")
)

type Config struct {
	MaxConnections   int
	MaxInvitations   int
	HandshakeTimeout time.Duration
	IdleTimeout      time.Duration
	MaxInvitationTTL time.Duration
	// Trusted test seams. Functions must be concurrency-safe. Nil uses real time.
	Clock       func() time.Time
	WithTimeout func(context.Context, time.Duration) (context.Context, context.CancelFunc)
}

func DefaultConfig() Config {
	return Config{MaxConnections: 16, MaxInvitations: 64, HandshakeTimeout: 10 * time.Second,
		IdleTimeout: 30 * time.Second, MaxInvitationTTL: 5 * time.Minute}
}

type InviteRequest struct {
	AgentID      agentprotocol.AgentID      `json:"agent_id"`
	Roles        []agentprotocol.Role       `json:"roles"`
	Capabilities []agentprotocol.Capability `json:"capabilities"`
	TTL          time.Duration              `json:"-"`
	Replace      bool                       `json:"replace,omitempty"`
}

// Pending contains no secret or verification hash. Roles are advertisement bounds,
// not assigned roles. A caller must explicitly authorize each Invite invocation.
type Pending struct {
	ID           string                     `json:"id"`
	AgentID      agentprotocol.AgentID      `json:"agent_id"`
	Roles        []agentprotocol.Role       `json:"roles"`
	Capabilities []agentprotocol.Capability `json:"capabilities"`
	ExpiresAt    time.Time                  `json:"expires_at"`
	Replaces     *agentregistry.Ref         `json:"replaces,omitempty"`
}

type invitation struct {
	Pending
	hash [32]byte
}

type Server struct {
	registry *agentregistry.Registry
	c        Config
	mu       sync.Mutex // Serializes consume/revoke/admission, including registry publish.
	pending  map[string]invitation
	closed   bool
	observed time.Time
}

func New(registry *agentregistry.Registry, c Config) (*Server, error) {
	if registry == nil || c.MaxConnections < 1 || c.MaxConnections > 128 || c.MaxInvitations < 1 || c.MaxInvitations > 1024 ||
		c.HandshakeTimeout <= 0 || c.HandshakeTimeout > time.Minute || c.IdleTimeout <= 0 || c.IdleTimeout > registry.LivenessThreshold() ||
		c.MaxInvitationTTL <= 0 || c.MaxInvitationTTL > time.Hour {
		return nil, errors.New("invalid local attachment configuration")
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	if c.WithTimeout == nil {
		c.WithTimeout = context.WithTimeout
	}
	return &Server{registry: registry, c: c, pending: map[string]invitation{}}, nil
}

func randomHex(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("local credential generation failed")
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) now() (time.Time, error) {
	now := s.c.Clock().UTC()
	if now.IsZero() || now.Before(s.observed) || now.Year() > 9999 {
		return time.Time{}, ErrAdmission
	}
	s.observed = now
	for id, p := range s.pending {
		if !now.Before(p.ExpiresAt) {
			delete(s.pending, id)
		}
	}
	return now, nil
}

// Invite is a trusted local human/ORCHESTRATOR action. It is never reachable from
// the agent socket. Replace explicitly authorizes one CAS-fenced supersession.
func (s *Server) Invite(req InviteRequest, authorized bool) (Pending, agentprotocol.InvitationProof, error) {
	if !authorized {
		return Pending{}, agentprotocol.InvitationProof{}, ErrAdmission
	}
	id := agentprotocol.Identity{ProtocolVersion: agentprotocol.Version2, AgentID: req.AgentID, SessionID: "invitation-validation",
		Metadata: agentprotocol.Metadata{Provider: "unspecified"}, Capabilities: req.Capabilities, SupportedRoles: req.Roles}
	if id.Validate() != nil || req.TTL <= 0 || req.TTL > s.c.MaxInvitationTTL {
		return Pending{}, agentprotocol.InvitationProof{}, ErrAdmission
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Pending{}, agentprotocol.InvitationProof{}, ErrClosed
	}
	now, err := s.now()
	if err != nil {
		return Pending{}, agentprotocol.InvitationProof{}, err
	}
	if len(s.pending) >= s.c.MaxInvitations {
		return Pending{}, agentprotocol.InvitationProof{}, ErrAdmission
	}
	p := Pending{AgentID: req.AgentID, Roles: append([]agentprotocol.Role(nil), req.Roles...), Capabilities: append([]agentprotocol.Capability(nil), req.Capabilities...), ExpiresAt: now.Add(req.TTL)}
	if req.Replace {
		all, err := s.registry.List()
		if err != nil {
			return Pending{}, agentprotocol.InvitationProof{}, ErrRegistry
		}
		for _, v := range all {
			if v.Current && v.Session.Identity.AgentID == req.AgentID {
				ref := v.Ref()
				p.Replaces = &ref
			}
		}
		if p.Replaces == nil {
			return Pending{}, agentprotocol.InvitationProof{}, ErrAdmission
		}
	}
	p.ID, err = randomHex(16)
	if err != nil {
		return Pending{}, agentprotocol.InvitationProof{}, err
	}
	secret, err := randomHex(32)
	if err != nil {
		return Pending{}, agentprotocol.InvitationProof{}, err
	}
	if _, exists := s.pending[p.ID]; exists {
		return Pending{}, agentprotocol.InvitationProof{}, ErrAdmission
	}
	s.pending[p.ID] = invitation{Pending: p, hash: sha256.Sum256([]byte(secret))}
	return clonePending(p), agentprotocol.InvitationProof{ID: p.ID, Secret: secret}, nil
}

func clonePending(p Pending) Pending {
	p.Roles = append([]agentprotocol.Role(nil), p.Roles...)
	p.Capabilities = append([]agentprotocol.Capability(nil), p.Capabilities...)
	if p.Replaces != nil {
		r := *p.Replaces
		p.Replaces = &r
	}
	return p
}

func (s *Server) Invitations() ([]Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if _, err := s.now(); err != nil {
		return nil, err
	}
	out := make([]Pending, 0, len(s.pending))
	for _, p := range s.pending {
		out = append(out, clonePending(p.Pending))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Server) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	delete(s.pending, id)
	return nil
}

func (s *Server) Close() { s.mu.Lock(); defer s.mu.Unlock(); s.closed = true; clear(s.pending) }

func permitted[T comparable](have, allowed []T) bool {
	for _, v := range have {
		found := false
		for _, a := range allowed {
			found = found || v == a
		}
		if !found {
			return false
		}
	}
	return true
}

func (s *Server) admit(ctx context.Context, e agentprotocol.Envelope) (agentregistry.Ref, error) {
	if e.ValidateFrom(agentprotocol.FromAgent) != nil || e.ProtocolVersion != agentprotocol.Version2 || e.Type != agentprotocol.EnrollmentRequest || e.Invitation == nil {
		return agentregistry.Ref{}, ErrAdmission
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return agentregistry.Ref{}, ErrClosed
	}
	if _, err := s.now(); err != nil {
		return agentregistry.Ref{}, err
	}
	p, ok := s.pending[e.Invitation.ID]
	hash := sha256.Sum256([]byte(e.Invitation.Secret))
	valid := subtle.ConstantTimeCompare(hash[:], p.hash[:]) == 1
	if !ok || !valid || p.AgentID != e.AgentID || !permitted(e.Identity.SupportedRoles, p.Roles) || !permitted(e.Identity.Capabilities, p.Capabilities) {
		return agentregistry.Ref{}, ErrAdmission
	}
	if err := ctx.Err(); err != nil {
		return agentregistry.Ref{}, err
	}
	// Burn before durable publication: even an uncertain fsync outcome cannot
	// turn this into a reusable credential. Revoke races serialize here.
	delete(s.pending, p.ID)
	decision := "local-invitation:" + p.ID
	v, err := s.registry.Enroll(agentregistry.Enrollment{Identity: *e.Identity, Supersedes: p.Replaces, Decision: decision})
	if err != nil {
		// Publication can precede a directory-sync error. Only quarantine the
		// record carrying this admission provenance; never reset registry history.
		if candidate, readErr := s.registry.Get(e.SessionID); readErr == nil && candidate.Session.AdmissionDecision == decision && candidate.Session.Identity.AgentID == e.AgentID {
			_ = s.registry.Disconnect(candidate.Ref(), "attachment_publication_uncertain")
		}
		return agentregistry.Ref{}, ErrRegistry
	}
	return v.Ref(), nil
}

func response(e agentprotocol.Envelope, ref agentregistry.Ref) agentprotocol.Envelope {
	out := agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version2, Type: agentprotocol.EnrollmentResponse, AgentID: e.AgentID, SessionID: e.SessionID,
		Enrollment: &agentprotocol.EnrollmentDecision{State: agentprotocol.EnrollmentAdmitted, SessionGeneration: uint64(ref.Generation)}}
	if ref.Generation == 0 {
		out.Enrollment = &agentprotocol.EnrollmentDecision{State: agentprotocol.EnrollmentRejected, Reason: "local admission denied"}
	}
	return out
}

func ack(ref agentregistry.Ref, kind agentprotocol.MessageType) agentprotocol.Envelope {
	return agentprotocol.Envelope{ProtocolVersion: agentprotocol.Version2, Type: agentprotocol.SessionAcknowledgment, AgentID: ref.AgentID, SessionID: ref.SessionID,
		Acknowledgment: &agentprotocol.SessionAck{For: kind, SessionGeneration: uint64(ref.Generation)}}
}

// Handle owns one unbound transport until termination. The successful Ref is
// private server-side state; no message chooses or replaces that binding.
func (s *Server) Handle(ctx context.Context, t agenttransport.Transport) (result error) {
	defer t.Close()
	handshake, cancel := s.c.WithTimeout(ctx, s.c.HandshakeTimeout)
	defer cancel()
	e, err := t.Receive(handshake)
	if err != nil {
		return err
	}
	ref, err := s.admit(handshake, e)
	if err != nil {
		if e.ProtocolVersion == agentprotocol.Version2 {
			_ = t.Send(handshake, response(e, agentregistry.Ref{}))
		}
		return err
	}
	defer func() {
		if err := s.registry.Disconnect(ref, "attachment_connection_closed"); err != nil && !errors.Is(err, agentregistry.ErrFenced) {
			result = errors.Join(result, ErrRegistry)
		}
	}()
	if err := t.Send(handshake, response(e, ref)); err != nil {
		return err
	}
	cancel()
	for {
		if err := s.registry.Check(ref); err != nil {
			return ErrBinding
		}
		idle, stop := s.c.WithTimeout(ctx, s.c.IdleTimeout)
		err := s.sessionMessage(idle, t, ref)
		stop()
		if errors.Is(err, errDisconnect) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

var errDisconnect = errors.New("attachment lifecycle disconnect")

func (s *Server) sessionMessage(ctx context.Context, t agenttransport.Transport, ref agentregistry.Ref) error {
	e, err := t.Receive(ctx)
	if err != nil {
		return err
	}
	if e.ValidateFrom(agentprotocol.FromAgent) != nil || e.ProtocolVersion != agentprotocol.Version2 || e.AgentID != ref.AgentID || e.SessionID != ref.SessionID {
		return ErrBinding
	}
	if err := s.registry.Check(ref); err != nil {
		return ErrBinding
	}
	if e.Assignment != nil {
		return ErrUnsupported
	}
	switch e.Type {
	case agentprotocol.Heartbeat:
		if err := s.registry.Heartbeat(ref); err != nil {
			return ErrBinding
		}
	case agentprotocol.Disconnect, agentprotocol.Handoff:
		if err := s.registry.Disconnect(ref, "attachment_client_disconnect"); err != nil {
			return ErrBinding
		}
	default:
		return ErrUnsupported
	}
	if err := t.Send(ctx, ack(ref, e.Type)); err != nil {
		return err
	}
	if e.Type != agentprotocol.Heartbeat {
		// Close is abortive in Phase 3. Let the peer consume this terminal ack
		// and close, rather than discarding a queued in-memory acknowledgment.
		if _, err := t.Receive(ctx); err == nil {
			return ErrUnsupported
		}
		return errDisconnect
	}
	return nil
}
