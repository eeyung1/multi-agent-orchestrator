package agentprotocol

import "errors"

// AssignmentRef copies the existing scheduler's ownership tuple. Generation is
// state.Task.AssignmentGeneration, not the repository's authority generation or
// a per-agent counter. It never creates/reserves a task, claim, or generation.
type AssignmentRef struct {
	ObjectiveID          string    `json:"objective_id"`
	TaskID               string    `json:"task_id"`
	AssignmentGeneration uint64    `json:"assignment_generation"`
	Token                string    `json:"token"`
	SessionID            SessionID `json:"session_id"`
}

// Validate checks shape only. Possessing a syntactically valid reference is not
// permission to execute or publish a result. Tokens must not be logged.
func (r AssignmentRef) Validate() error {
	if err := text("objective_id", r.ObjectiveID, 1024); err != nil {
		return err
	}
	if err := text("task_id", r.TaskID, 1024); err != nil {
		return err
	}
	if r.AssignmentGeneration == 0 {
		return errors.New("assignment_generation must be nonzero")
	}
	if err := identifier("token", r.Token); err != nil {
		return err
	}
	return r.SessionID.Validate()
}

// ValidateAgainst compares every ownership field with a trusted scheduler
// snapshot, never another agent-supplied reference. This pure comparison does
// not replace scheduler status, worktree/branch, enrollment, or execution guards;
// callers still need fresh authoritative checks at every existing boundary.
func (r AssignmentRef) ValidateAgainst(expected AssignmentRef) error {
	if err := expected.Validate(); err != nil {
		return errors.New("invalid authoritative assignment reference")
	}
	if err := r.Validate(); err != nil {
		return err
	}
	if r != expected {
		return errors.New("assignment reference does not match authoritative ownership")
	}
	return nil
}
