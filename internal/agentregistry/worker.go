package agentregistry

import (
	"errors"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

// ReserveWorker couples READY -> BUSY with the existing scheduler reservation in
// the SAME state.json transaction. The callback is trusted Go, never agent data;
// it must not do I/O or retain the snapshot. Errors discard all callback changes.
func (r *Registry) ReserveWorker(ref Ref, capability agentprotocol.Capability, reserve func(*state.Snapshot) error) error {
	if ref.Validate() != nil || capability.Validate() != nil || reserve == nil {
		return errors.New("invalid worker reservation")
	}
	return r.transactSnapshot("worker-reserve", func(s *state.Snapshot, now time.Time) (bool, error) {
		d := state.WorkerDispatch{AgentID: ref.AgentID, SessionID: ref.SessionID, Incarnation: ref.Generation}
		if err := s.CheckWorkerSession(d, now, Ready); err != nil {
			return false, err
		}
		a := s.AgentRegistry.Sessions[ref.SessionID]
		match := false
		for _, c := range a.Identity.Capabilities {
			match = match || c == capability
		}
		if !match {
			return false, errors.New("worker capability mismatch")
		}
		// Run on a private copy: a rejected scheduler callback cannot leak mutations
		// even if expiration must be committed by transactSnapshot.
		copy := *s
		copy.Tasks = make(map[string]state.Task, len(s.Tasks))
		for k, v := range s.Tasks {
			copy.Tasks[k] = v
		}
		copy.Objectives = make(map[string]state.Objective, len(s.Objectives))
		for k, v := range s.Objectives {
			copy.Objectives[k] = v
		}
		if err := reserve(&copy); err != nil {
			return false, err
		}
		reservations := 0
		for k, t := range copy.Tasks {
			d := t.WorkerDispatch
			old := s.Tasks[k].WorkerDispatch
			if d != nil && old == nil && t.Status == "ASSIGNED" && d.AgentID == ref.AgentID && d.SessionID == ref.SessionID && d.Incarnation == ref.Generation && d.Phase == "RESERVED" {
				reservations++
			}
		}
		if reservations != 1 {
			return false, errors.New("worker reservation must bind exactly one scheduler assignment")
		}
		if s.AgentRegistry.WorkerDispatchCount == ^uint64(0) {
			return false, errors.New("worker dispatch counter exhausted")
		}
		s.AgentRegistry.WorkerDispatchCount++
		s.Tasks, s.Objectives = copy.Tasks, copy.Objectives
		a.Status, a.StateReason, a.StateChangedAt = Busy, "scheduler_reserved_worker", now
		s.AgentRegistry.Sessions[ref.SessionID] = a
		return true, nil
	})
}

// CheckWorkerReservation rechecks an existing reservation under the scheduler's
// state transaction, using this registry's trusted/injectable clock. It performs
// no nested state operation, heartbeat, or authority change.
func (r *Registry) CheckWorkerReservation(s state.Snapshot, d state.WorkerDispatch) error {
	now, err := r.clock()
	if err != nil {
		return err
	}
	if s.AgentRegistry == nil || s.AgentRegistry.StaleAfterNS != r.policy.StaleAfter {
		return errors.New("worker registry policy mismatch")
	}
	return s.CheckWorkerSession(d, now, Busy)
}
