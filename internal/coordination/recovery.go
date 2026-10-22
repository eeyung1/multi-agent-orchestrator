package coordination

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

// ReconcileBlockedAssignment is exclusively for explicit human recovery of an
// already-BLOCKED structured assignment. It cannot retire an ASSIGNED task or an
// arbitrary WORKING session. Lock order is structured state then coordination;
// both remain held until operational retirement is published.
//
// Structured BLOCKED is retained here. The scheduler subsequently records history
// and resets to PENDING with its own identity fence. If either state write fails,
// the same exact BLOCKED/HANDOFF assignment can be retried safely.
func ReconcileBlockedAssignment(primary string, expected state.Task, human string) error {
	if strings.TrimSpace(human) == "" {
		return errors.New("explicit human recovery authorization is required")
	}
	if expected.ID == "" || expected.ObjectiveID == "" || expected.Status != "BLOCKED" || expected.AssignmentGeneration == 0 {
		return errors.New("reconciliation requires an identified BLOCKED structured assignment")
	}
	id, err := repository.Identity(primary)
	if err != nil {
		return err
	}
	store, err := state.Open(primary, id)
	if err != nil {
		return err
	}
	return store.Update(human, "reconcile-blocked-assignment", func(s *state.Snapshot) error {
		key := expected.ObjectiveID + "::" + expected.ID
		current, ok := s.Tasks[key]
		if !ok || !reflect.DeepEqual(current, expected) {
			return errors.New("blocked assignment changed before operational reconciliation")
		}
		for otherKey, task := range s.Tasks {
			if otherKey == key || (task.Status != "ASSIGNED" && task.Status != "BLOCKED") {
				continue
			}
			if task.SessionID == expected.SessionID || task.Token == expected.Token || task.Worktree == expected.Worktree || task.Branch == expected.Branch {
				return fmt.Errorf("operational ownership is also referenced by task %s", otherKey)
			}
		}
		return retireBlockedSession(primary, expected.SessionID, expected.Token, &current)
	})
}
