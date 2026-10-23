package scheduler

import (
	"context"
	"errors"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/locking"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

// failWorker preserves enrollment history and cannot change a replacement. An
// uncertain external writer remains BLOCKED/DISCONNECTED until explicit action.
func failWorker(s *state.Snapshot, t *state.Task) {
	if t.WorkerDispatch == nil {
		return
	}
	d := *t.WorkerDispatch
	d.Phase = "FAILED"
	t.WorkerDispatch = &d
	if s.AgentRegistry == nil {
		return
	}
	a := s.AgentRegistry.Sessions[d.SessionID]
	if a.Generation == d.Incarnation && a.Status == state.AgentBusy && a.SupersededBy == "" {
		a.Status, a.StateReason = state.AgentBlocked, "worker_assignment_failed"
		a.StateChangedAt = time.Now().UTC()
		if a.StateChangedAt.Before(s.AgentRegistry.ObservedAt) {
			a.StateChangedAt = s.AgentRegistry.ObservedAt
		}
		s.AgentRegistry.ObservedAt = a.StateChangedAt
		s.AgentRegistry.Sessions[d.SessionID] = a
	}
}
func workerStore(workspace string) (*state.Store, error) {
	p, e := repository.Primary(workspace)
	if e != nil {
		return nil, e
	}
	id, e := repository.Identity(p)
	if e != nil {
		return nil, e
	}
	return state.Open(p, id)
}

// WorkerEvent is an internal authoritative handler. The transport's private Ref
// and persisted task dispatch must BOTH match. Agent fields cannot create state.
func WorkerEvent(workspace string, ref state.AgentRef, e agentprotocol.Envelope) error {
	return workerEvent(context.Background(), workspace, ref, e)
}

// WorkerEventContext retries only failed lock acquisition, before any mutation.
// Parallel validation can hold state authority longer than one acquisition wait.
// Each attempt rereads and fences the report, under the exchange's bounded life.
func WorkerEventContext(ctx context.Context, workspace string, ref state.AgentRef, e agentprotocol.Envelope) error {
	return retryWorkerLock(ctx, func() error { return workerEvent(ctx, workspace, ref, e) })
}

func retryWorkerLock(ctx context.Context, update func() error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := update(); !errors.Is(err, locking.ErrLocked) {
			return err
		}
	}
}

func workerEvent(ctx context.Context, workspace string, ref state.AgentRef, e agentprotocol.Envelope) error {
	if e.ValidateFrom(agentprotocol.FromAgent) != nil || e.Work == nil || e.AgentID != ref.AgentID || e.SessionID != ref.SessionID {
		return errors.New("worker report binding rejected")
	}
	store, err := workerStore(workspace)
	if err != nil {
		return err
	}
	return store.Update("ORCHESTRATOR", "worker-report", func(s *state.Snapshot) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := e.Work.Correlation
		k := taskKey(c.ObjectiveID, c.TaskID)
		t := s.Tasks[k]
		d := t.WorkerDispatch
		if t.Status != "ASSIGNED" || d == nil || d.AgentID != ref.AgentID || d.SessionID != ref.SessionID || d.Incarnation != ref.Generation || d.Correlation != c || s.CheckWorkerSession(*d, time.Now().UTC(), state.AgentBusy) != nil {
			return errors.New("worker report stale or unbound")
		}
		next := *d
		switch e.Type {
		case agentprotocol.WorkerAcceptance:
			if d.Phase != "SENT" {
				return errors.New("worker acceptance out of order")
			}
			if *e.Work.Accepted {
				next.Phase = "ACCEPTED"
			} else {
				next.Phase = "REJECTED"
			}
		case agentprotocol.WorkerProgress, agentprotocol.WorkerBlocker, agentprotocol.WorkerResult:
			if d.Phase != "ACCEPTED" {
				return errors.New("worker report out of order or replayed")
			}
			if e.Type != agentprotocol.WorkerProgress {
				next.Phase = "REPORTED"
			}
		default:
			return errors.New("unsupported worker report")
		}
		// Rejection is terminal evidence; BLOCKED publication is the controller's
		// existing operational+structured path, never an agent state transition.
		if next.Phase == "REJECTED" {
			next.Phase = "REPORTED"
		}
		next.Detail = e.Work.Detail
		next.LastMessage = e.Type
		t.WorkerDispatch = &next
		s.Tasks[k] = t
		return nil
	})
}
func MarkWorkerSent(workspace, objectiveID string, a Result) error {
	if err := VerifyWorker(workspace, objectiveID, a); err != nil {
		return err
	}
	store, err := workerStore(workspace)
	if err != nil {
		return err
	}
	return store.Update("ORCHESTRATOR", "worker-send", func(s *state.Snapshot) error {
		k := taskKey(objectiveID, a.Task.ID)
		t := s.Tasks[k]
		d := t.WorkerDispatch
		if d == nil || a.WorkerDispatch == nil || d.Correlation != a.WorkerDispatch.Correlation || t.Status != "ASSIGNED" || d.Phase != "RESERVED" || s.CheckWorkerSession(*d, time.Now().UTC(), state.AgentBusy) != nil {
			return errors.New("worker send fenced")
		}
		n := *d
		n.Phase = "SENT"
		t.WorkerDispatch = &n
		s.Tasks[k] = t
		return nil
	})
}
func VerifyWorker(workspace, objectiveID string, a Result) error {
	if err := Verify(workspace, objectiveID, a); err != nil {
		return err
	}
	s, err := Read(workspace)
	if err != nil {
		return err
	}
	d := s.Tasks[taskKey(objectiveID, a.Task.ID)].WorkerDispatch
	if d == nil || a.WorkerDispatch == nil || d.AgentID != a.WorkerDispatch.AgentID || d.SessionID != a.WorkerDispatch.SessionID || d.Incarnation != a.WorkerDispatch.Incarnation || d.Correlation != a.WorkerDispatch.Correlation || d.Phase == "FAILED" || d.Phase == "FINISHED" {
		return errors.New("worker dispatch fenced")
	}
	return s.CheckWorkerSession(*d, time.Now().UTC(), state.AgentBusy)
}
