// Package agentregistry is the authoritative internal Go boundary for protocol
// session admission and liveness. It does not authenticate transports, dispatch
// work, grant claims, or register existing managed subprocesses automatically.
package agentregistry

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

type Generation = state.AgentSessionGeneration
type Status = state.AgentStatus

const (
	Ready        = state.AgentReady
	Busy         = state.AgentBusy
	Blocked      = state.AgentBlocked
	Disconnected = state.AgentDisconnected
)

var (
	ErrFenced          = errors.New("registry session is stale, superseded or disconnected")
	ErrClockRegression = errors.New("registry clock precedes durable observations")
)

// Policy is trusted Go policy, persisted on initialization and checked on reopen.
// There is no default timeout or agent-supplied liveness policy.
type Policy struct{ StaleAfter time.Duration }

// Ref is a fencing reference, not an authentication credential. Generation is
// Go-issued enrollment identity, never scheduler AssignmentGeneration.
type Ref = state.AgentRef

// Enrollment cannot select its issued generation or assigned role. Supersedes
// is only a compare-and-swap precondition using the previously issued reference.
// Decision records internal Go authorization provenance, not a password/token.
type Enrollment struct {
	Identity   agentprotocol.Identity
	Supersedes *Ref
	Decision   string
}

type View struct {
	Session state.AgentSession `json:"session"`
	Current bool               `json:"current"`
	Live    bool               `json:"live"`
}

func (v View) Ref() Ref {
	return Ref{AgentID: v.Session.Identity.AgentID, SessionID: v.Session.Identity.SessionID, Generation: v.Session.Generation}
}

// Registry contains immutable configuration and serializes this handle's calls
// before acquiring the shared filesystem lock. Concurrent local sessions cannot
// exhaust that lock's contention deadline by competing with this same handle.
// Each operation still rereads durable state under the shared authoritative lock;
// the process-local mutex never replaces persistence or cross-process fencing.
// An injected clock must be safe to call from
// concurrent callers; nil selects time.Now. No background timers are started.
type Registry struct {
	mu     sync.Mutex
	store  *state.Store
	policy Policy
	now    func() time.Time
}

func Open(workspace string, policy Policy, clock func() time.Time) (*Registry, error) {
	if policy.StaleAfter <= 0 {
		return nil, errors.New("positive registry liveness threshold is required")
	}
	if err := enrollment.RequireEnrolled(workspace); err != nil {
		return nil, err
	}
	primary, err := repository.Primary(workspace)
	if err != nil {
		return nil, err
	}
	id, err := repository.Identity(primary)
	if err != nil {
		return nil, err
	}
	store, err := state.Open(primary, id)
	if err != nil {
		return nil, err
	}
	if clock == nil {
		clock = time.Now
	}
	r := &Registry{store: store, policy: policy, now: clock}
	err = store.UpdateIf("ORCHESTRATOR", "agent-registry-initialize", func(s *state.Snapshot) (bool, error) {
		if !enrolled(*s) {
			return false, errors.New("repository authority is not enrolled")
		}
		if s.AgentRegistry != nil {
			if s.AgentRegistry.StaleAfterNS != policy.StaleAfter {
				return false, errors.New("registry liveness policy differs from persisted policy")
			}
			return false, nil
		}
		now, err := r.clock()
		if err != nil {
			return false, err
		}
		s.AgentRegistry = &state.AgentRegistryState{StaleAfterNS: policy.StaleAfter, ObservedAt: now, Sessions: map[agentprotocol.SessionID]state.AgentSession{}}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) clock() (time.Time, error) {
	now := r.now().UTC()
	if now.IsZero() || now.Year() < 1 || now.Year() > 9999 {
		return time.Time{}, errors.New("invalid registry clock")
	}
	return now, nil
}

func enrolled(s state.Snapshot) bool {
	switch s.Authority.Enrollment {
	case string(enrollment.Native), string(enrollment.Compatible), "MIGRATED":
		return true
	default:
		return false
	}
}

// transact expires sessions before observing/updating them. Expiration is
// committed even when the requested action is subsequently fenced. A rejected
// action itself never mutates state. Reads only write when expiration is due.
func (r *Registry) transact(operation string, fn func(*state.AgentRegistryState, time.Time) (bool, error)) error {
	return r.transactSnapshot(operation, func(s *state.Snapshot, now time.Time) (bool, error) { return fn(s.AgentRegistry, now) })
}

func (r *Registry) transactSnapshot(operation string, fn func(*state.Snapshot, time.Time) (bool, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var actionErr error
	err := r.store.UpdateIf("ORCHESTRATOR", "agent-registry-"+operation, func(s *state.Snapshot) (bool, error) {
		if !enrolled(*s) || s.AgentRegistry == nil {
			return false, errors.New("registry authority is unavailable")
		}
		data := s.AgentRegistry
		if data.StaleAfterNS != r.policy.StaleAfter {
			return false, errors.New("registry liveness policy changed")
		}
		now, err := r.clock()
		if err != nil {
			return false, err
		}
		if now.Before(data.ObservedAt) {
			return false, ErrClockRegression
		}
		expired := false
		for id, session := range data.Sessions {
			if session.Status != Disconnected && now.After(session.LastSeenAt.Add(data.StaleAfterNS)) {
				session.Status, session.StateReason, session.StateChangedAt = Disconnected, "heartbeat_timeout", now
				session.DisconnectedAt = &now
				data.Sessions[id] = session
				expired = true
			}
		}
		invalidated := invalidatePlanners(data)
		changed, e := fn(s, now)
		changed = invalidatePlanners(data) || changed
		actionErr = e
		if expired || invalidated || changed {
			data.ObservedAt = now
		}
		return expired || invalidated || changed, nil
	})
	return errors.Join(err, actionErr)
}

func current(data *state.AgentRegistryState, agent agentprotocol.AgentID) (state.AgentSession, bool) {
	var found state.AgentSession
	for _, session := range data.Sessions {
		if session.Identity.AgentID == agent && session.Generation > found.Generation {
			found = session
		}
	}
	return found, found.Generation != 0
}

func verify(data *state.AgentRegistryState, ref Ref, allowDisconnected bool) (state.AgentSession, error) {
	session, ok := current(data, ref.AgentID)
	if !ok || session.Identity.SessionID != ref.SessionID || session.Generation != ref.Generation || (!allowDisconnected && session.Status == Disconnected) {
		return state.AgentSession{}, ErrFenced
	}
	return session, nil
}

func view(data *state.AgentRegistryState, session state.AgentSession, now time.Time) View {
	latest, _ := current(data, session.Identity.AgentID)
	isCurrent := latest.Generation == session.Generation
	return View{Session: session, Current: isCurrent, Live: isCurrent && session.Status != Disconnected && !now.After(session.LastSeenAt.Add(data.StaleAfterNS))}
}

// Enroll is an authoritative Go decision, not an agent-callable wire handler.
// Re-enrollment needs a fresh, never-used SessionID and an exact Supersedes fence.
func (r *Registry) Enroll(request Enrollment) (View, error) {
	if err := request.Identity.Validate(); err != nil {
		return View{}, err
	}
	if err := state.RegistryDecision(request.Decision); err != nil {
		return View{}, err
	}
	if request.Supersedes != nil {
		if err := request.Supersedes.Validate(); err != nil {
			return View{}, err
		}
	}
	var result View
	err := r.transact("enroll", func(data *state.AgentRegistryState, now time.Time) (bool, error) {
		id := request.Identity
		if _, exists := data.Sessions[id.SessionID]; exists {
			return false, errors.New("registry session ID was already used")
		}
		old, exists := current(data, id.AgentID)
		if exists {
			if request.Supersedes == nil || request.Supersedes.AgentID != id.AgentID {
				return false, errors.New("expected previous registry incarnation is required")
			}
			var err error
			old, err = verify(data, *request.Supersedes, true)
			if err != nil {
				return false, err
			}
		} else if request.Supersedes != nil {
			return false, ErrFenced
		}
		if data.LastGeneration == ^Generation(0) {
			return false, errors.New("registry session generation exhausted")
		}
		if exists {
			if old.Status != Disconnected {
				old.Status, old.StateChangedAt, old.DisconnectedAt = Disconnected, now, &now
				old.StateReason = "superseded_by_authorized_enrollment"
			}
			old.SupersededBy = id.SessionID
			data.Sessions[old.Identity.SessionID] = old
		}
		data.LastGeneration++
		session := state.AgentSession{Identity: id, Generation: data.LastGeneration, Status: Ready,
			EnrolledAt: now, LastSeenAt: now, StateChangedAt: now,
			AdmissionDecision: request.Decision, StateReason: "authorized_enrollment"}
		data.Sessions[id.SessionID] = session
		result = view(data, session, now)
		return true, nil
	})
	if err != nil {
		return View{}, err
	}
	return result, nil
}

func (r *Registry) Heartbeat(ref Ref) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	return r.transact("heartbeat", func(data *state.AgentRegistryState, now time.Time) (bool, error) {
		session, err := verify(data, ref, false)
		if err != nil {
			return false, err
		}
		session.LastSeenAt = now
		data.Sessions[ref.SessionID] = session
		return true, nil
	})
}

// LivenessThreshold is immutable trusted policy, not an agent advertisement.
func (r *Registry) LivenessThreshold() time.Duration { return r.policy.StaleAfter }

// Check fences a connection observation without refreshing liveness or authority.
func (r *Registry) Check(ref Ref) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	return r.transact("check", func(data *state.AgentRegistryState, _ time.Time) (bool, error) {
		_, err := verify(data, ref, false)
		return false, err
	})
}

// Disconnect is idempotent for the exact current incarnation. Superseded refs
// remain fenced; a delayed connection close cannot disconnect a replacement.
func (r *Registry) Disconnect(ref Ref, reason string) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if err := state.RegistryDecision(reason); err != nil {
		return err
	}
	return r.transact("disconnect", func(data *state.AgentRegistryState, now time.Time) (bool, error) {
		s, err := verify(data, ref, true)
		if err != nil {
			return false, err
		}
		if s.Status == Disconnected {
			return false, nil
		}
		s.Status, s.StateReason, s.StateChangedAt, s.DisconnectedAt = Disconnected, reason, now, &now
		data.Sessions[ref.SessionID] = s
		return true, nil
	})
}

func (r *Registry) List() ([]View, error) {
	var result []View
	err := r.transact("observe", func(data *state.AgentRegistryState, now time.Time) (bool, error) {
		result = make([]View, 0, len(data.Sessions))
		for _, session := range data.Sessions {
			result = append(result, view(data, session, now))
		}
		sort.Slice(result, func(i, j int) bool { return result[i].Session.Generation < result[j].Session.Generation })
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Registry) Get(id agentprotocol.SessionID) (View, error) {
	if err := id.Validate(); err != nil {
		return View{}, err
	}
	var result View
	err := r.transact("observe", func(data *state.AgentRegistryState, now time.Time) (bool, error) {
		session, ok := data.Sessions[id]
		if !ok {
			return false, errors.New("registry session not found")
		}
		result = view(data, session, now)
		return false, nil
	})
	if err != nil {
		return View{}, err
	}
	return result, nil
}

// Eligible returns a fresh authoritative snapshot, not a reservation or task
// grant. Future dispatch must recheck it atomically with assignment admission.
func (r *Registry) Eligible(role agentprotocol.Role, capability agentprotocol.Capability) ([]View, error) {
	if err := role.Validate(); err != nil {
		return nil, err
	}
	if err := capability.Validate(); err != nil {
		return nil, err
	}
	all, err := r.List()
	if err != nil {
		return nil, err
	}
	eligible := make([]View, 0)
	for _, v := range all {
		if v.Current && v.Live && v.Session.Status == Ready && v.Session.AssignedRole == role {
			for _, cap := range v.Session.Identity.Capabilities {
				if cap == capability {
					eligible = append(eligible, v)
					break
				}
			}
		}
	}
	return eligible, nil
}

func supports(id agentprotocol.Identity, role agentprotocol.Role) bool {
	for _, supported := range id.SupportedRoles {
		if role == supported {
			return true
		}
	}
	return false
}

func (r *Registry) AssignRole(ref Ref, role agentprotocol.Role, decision string) error {
	if err := role.Validate(); err != nil {
		return err
	}
	return r.setRole(ref, role, decision)
}

func (r *Registry) ClearRole(ref Ref, decision string) error { return r.setRole(ref, "", decision) }

func (r *Registry) setRole(ref Ref, role agentprotocol.Role, decision string) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if err := state.RegistryDecision(decision); err != nil {
		return err
	}
	return r.transact("role-decision", func(data *state.AgentRegistryState, now time.Time) (bool, error) {
		session, err := verify(data, ref, false)
		if err != nil {
			return false, err
		}
		if session.Status == Busy {
			return false, errors.New("cannot change role while registry session is BUSY")
		}
		if role != "" && !supports(session.Identity, role) {
			return false, errors.New("assigned role is not advertised")
		}
		if session.AssignedRole == role {
			return false, nil
		}
		session.AssignedRole, session.RoleDecision = role, decision
		data.Sessions[ref.SessionID] = session
		return true, nil
	})
}

// Register updates advertised facts only. It cannot change identity/incarnation,
// assign a role, refresh liveness, or withdraw an already-assigned role.
func (r *Registry) Register(ref Ref, identity agentprotocol.Identity) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if err := identity.Validate(); err != nil {
		return err
	}
	if identity.AgentID != ref.AgentID || identity.SessionID != ref.SessionID {
		return ErrFenced
	}
	return r.transact("advertisement", func(data *state.AgentRegistryState, now time.Time) (bool, error) {
		session, err := verify(data, ref, false)
		if err != nil {
			return false, err
		}
		if session.Status == Busy {
			return false, errors.New("cannot replace advertisement while BUSY")
		}
		if session.AssignedRole != "" && !supports(identity, session.AssignedRole) {
			return false, errors.New("advertisement withdraws assigned role")
		}
		session.Identity = identity
		data.Sessions[ref.SessionID] = session
		return true, nil
	})
}

func (r *Registry) Transition(ref Ref, next Status, reason string) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if err := state.RegistryDecision(reason); err != nil {
		return err
	}
	return r.transactSnapshot("transition", func(snapshot *state.Snapshot, now time.Time) (bool, error) {
		data := snapshot.AgentRegistry
		if next == Ready {
			for _, task := range snapshot.Tasks {
				d := task.WorkerDispatch
				if task.Status == "ASSIGNED" && d != nil && d.SessionID == ref.SessionID && d.Incarnation == ref.Generation {
					return false, errors.New("scheduler still owns worker reservation")
				}
			}
		}
		session, err := verify(data, ref, true)
		if err != nil {
			return false, err
		}
		allowed := session.Status == Ready && (next == Busy || next == Disconnected) ||
			session.Status == Busy && (next == Ready || next == Blocked || next == Disconnected) ||
			session.Status == Blocked && (next == Ready || next == Disconnected)
		if !allowed {
			return false, fmt.Errorf("invalid registry transition %s -> %s", session.Status, next)
		}
		if next == Busy && session.AssignedRole == "" {
			return false, errors.New("BUSY requires an assigned protocol role")
		}
		session.Status, session.StateReason, session.StateChangedAt = next, reason, now
		if next == Disconnected {
			session.DisconnectedAt = &now
		}
		data.Sessions[ref.SessionID] = session
		return true, nil
	})
}
