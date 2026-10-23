package agentprotocol

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
)

// WorkerCorrelation references existing scheduler authority. It intentionally
// omits the private operational claim token and operational supervisor session.
// All fields are assertions on agent-origin messages, checked against Go state.
type WorkerCorrelation struct {
	ObjectiveID string `json:"objective_id"`
	TaskID      string `json:"task_id"`
	Generation  uint64 `json:"assignment_generation"`
	DispatchID  string `json:"dispatch_id"`
}

func (c WorkerCorrelation) Validate() error {
	for _, v := range []string{c.ObjectiveID, c.TaskID, c.DispatchID} {
		if identifier("worker correlation", v) != nil {
			return errors.New("invalid worker correlation")
		}
	}
	if c.Generation == 0 {
		return errors.New("assignment generation required")
	}
	return nil
}

type WorkerAssignmentData struct {
	Objective    string          `json:"objective"`
	Task         planning.Task   `json:"task"`
	Worktree     string          `json:"worktree"`
	Branch       string          `json:"branch"`
	Instructions []planning.File `json:"instructions,omitempty"`
	// Changes must be left uncommitted. A terminal report relinquishes writing;
	// Go checks scope, commits, validates, and records results authoritatively.
	Rules string `json:"rules"`
}

// WorkerMessage has exactly one typed use selected by Envelope.Type. Progress
// and terminal detail are bounded informational text, never commands/authority.
type WorkerMessage struct {
	Correlation WorkerCorrelation     `json:"correlation"`
	Assignment  *WorkerAssignmentData `json:"assignment,omitempty"`
	Accepted    *bool                 `json:"accepted,omitempty"`
	Detail      string                `json:"detail,omitempty"`
}

func (e Envelope) validateWork() error {
	worker := e.Type == WorkerAssignment || e.Type == WorkerAcceptance || e.Type == WorkerProgress || e.Type == WorkerBlocker || e.Type == WorkerResult
	if !worker {
		if e.Work != nil {
			return errors.New("unexpected worker payload")
		}
		return nil
	}
	if e.ProtocolVersion != Version4 || e.Work == nil {
		return errors.New("worker messages require v4 payload")
	}
	w := e.Work
	if err := w.Correlation.Validate(); err != nil {
		return err
	}
	if (w.Assignment != nil) != (e.Type == WorkerAssignment) || (w.Accepted != nil) != (e.Type == WorkerAcceptance) {
		return errors.New("invalid worker payload fields")
	}
	if w.Assignment != nil {
		a := w.Assignment
		if strings.TrimSpace(a.Objective) == "" || a.Task.ID != w.Correlation.TaskID || a.Task.Scope == "" || a.Task.Description == "" || len(a.Task.Validation) == 0 || !filepath.IsAbs(a.Worktree) || a.Branch == "" || a.Rules == "" {
			return errors.New("incomplete worker assignment")
		}
	}
	if e.Type == WorkerProgress || e.Type == WorkerBlocker || e.Type == WorkerResult || e.Type == WorkerAcceptance && !*w.Accepted {
		if err := text("worker detail", w.Detail, 8192); err != nil {
			return err
		}
	} else if w.Detail != "" {
		return errors.New("unexpected worker detail")
	}
	return nil
}
