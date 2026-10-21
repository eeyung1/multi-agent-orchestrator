package review

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/enrollment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

// CheckOutput binds review to a committed, clean, owned branch. Both renames'
// paths appear with --no-renames, including deletions and earlier commits.
func CheckOutput(t state.Task) (string, error) {
	if t.Worktree == "" || t.Branch == "" || t.BaseSHA == "" {
		return "", errors.New("assignment metadata is incomplete")
	}
	branch, err := git(t.Worktree, "branch", "--show-current")
	if err != nil || branch != t.Branch {
		return "", errors.New("worker changed its assigned branch")
	}
	dirty, err := git(t.Worktree, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	if dirty != "" {
		return "", errors.New("worker output must be committed and clean before validation/review")
	}
	ancestor := execGit(t.Worktree, "merge-base", "--is-ancestor", t.BaseSHA, "HEAD")
	if ancestor != nil {
		return "", errors.New("result no longer descends from the assigned base")
	}
	paths, err := git(t.Worktree, "diff", "--name-only", "--no-renames", "-z", t.BaseSHA, "HEAD")
	if err != nil {
		return "", err
	}
	for _, path := range strings.Split(paths, "\x00") {
		if path == "" {
			continue
		}
		canonical, err := state.CanonicalScope(t.Worktree, path)
		if err != nil {
			return "", err
		}
		if canonical != t.Scope && !strings.HasPrefix(canonical, t.Scope+"/") {
			return "", fmt.Errorf("result changes an unowned path: %s", canonical)
		}
	}
	return git(t.Worktree, "rev-parse", "HEAD")
}

func RecordValidation(in ResultInput) error {
	if err := enrollment.RequireEnrolled(in.Workspace); err != nil {
		return err
	}
	if in.ValidatedHead == "" || strings.TrimSpace(in.Validation) == "" {
		return errors.New("exact validated head and evidence are required")
	}
	primary, store, err := resultStore(in.Workspace)
	if err != nil {
		return err
	}
	if err := coordination.VerifyExecution(primary, in.SessionID, in.Worktree); err != nil {
		return err
	}
	return store.Update("ORCHESTRATOR", "record-validated-output", func(s *state.Snapshot) error {
		t, err := matchingTask(s, in)
		if err != nil {
			return err
		}
		head, err := CheckOutput(t)
		if err != nil {
			return err
		}
		if head != in.ValidatedHead {
			return errors.New("head changed after validation")
		}
		t.ValidatedHead = head
		t.ValidationEvidence = in.Validation
		s.Tasks[in.ObjectiveID+"::"+in.TaskID] = t
		return nil
	})
}

// FinalizeRecorded can reconcile a crash after COMPLETE was published but before
// structured collection. It never re-executes a worker or takes over a claim.
func FinalizeRecorded(in ResultInput) (state.ResultRecord, error) {
	if err := enrollment.RequireEnrolled(in.Workspace); err != nil {
		return state.ResultRecord{}, err
	}
	primary, store, err := resultStore(in.Workspace)
	if err != nil {
		return state.ResultRecord{}, err
	}
	s, err := store.Read()
	if err != nil {
		return state.ResultRecord{}, err
	}
	t, err := matchingTask(&s, in)
	if err != nil {
		return state.ResultRecord{}, err
	}
	if t.ValidatedHead == "" || t.ValidationEvidence == "" {
		return state.ResultRecord{}, errors.New("no durable validation evidence")
	}
	head, err := CheckOutput(t)
	if err != nil {
		return state.ResultRecord{}, err
	}
	if head != t.ValidatedHead {
		return state.ResultRecord{}, errors.New("recorded validation is stale")
	}
	in.ValidatedHead = t.ValidatedHead
	in.Validation = t.ValidationEvidence
	in.Branch = t.Branch
	in.Worktree = t.Worktree
	if len(in.Artifacts) == 0 {
		in.Artifacts = []string{t.Worktree}
	}
	status, err := coordination.SessionStatus(primary, in.SessionID)
	if err != nil {
		return state.ResultRecord{}, err
	}
	if status != "COMPLETE" {
		err = coordination.CompleteSession(primary, in.SessionID, in.Token, func() error {
			current, err := store.Read()
			if err != nil {
				return err
			}
			if _, err := matchingTask(&current, in); err != nil {
				return err
			}
			f, err := os.OpenFile(filepath.Join(primary, "multi-agent-orchestrator", "resolution.md"), os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				return err
			}
			_, writeErr := fmt.Fprintf(f, "\n## Validated task %s/%s\n- Session: %s\n- HEAD: %s\n- Evidence: %s\n- Recorded UTC: %s\n", in.ObjectiveID, in.TaskID, in.SessionID, in.ValidatedHead, strings.ReplaceAll(in.Validation, "\n", "; "), time.Now().UTC().Format(time.RFC3339))
			if writeErr == nil {
				writeErr = f.Sync()
			}
			closeErr := f.Close()
			if writeErr != nil {
				return writeErr
			}
			return closeErr
		})
		if err != nil {
			return state.ResultRecord{}, err
		}
	}
	return Collect(in)
}

func matchingTask(s *state.Snapshot, in ResultInput) (state.Task, error) {
	t, ok := s.Tasks[in.ObjectiveID+"::"+in.TaskID]
	if !ok || t.Status != "ASSIGNED" || t.Token != in.Token || t.SessionID != in.SessionID || t.AssignmentGeneration != in.AssignmentGeneration || t.Worktree != in.Worktree {
		return t, errors.New("stale or unauthorized validated result")
	}
	return t, nil
}
func resultStore(workspace string) (string, *state.Store, error) {
	primary, err := repository.Primary(workspace)
	if err != nil {
		return "", nil, err
	}
	id, err := repository.Identity(primary)
	if err != nil {
		return "", nil, err
	}
	store, err := state.Open(primary, id)
	return primary, store, err
}
func execGit(dir string, args ...string) error {
	c := exec.Command("git", args...)
	c.Dir = dir
	return c.Run()
}
