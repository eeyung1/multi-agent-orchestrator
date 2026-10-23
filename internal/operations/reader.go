package operations

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

var (
	ErrAuthority = errors.New("authoritative state or ledger unavailable, corrupt, or unsupported; inspection refused without changing state")
	ErrChanging  = errors.New("authoritative state changed during inspection; retry status")
)

type Options struct {
	Now func() time.Time
	// Connected must be a bounded, concurrency-safe local-host observation of
	// the EXACT ref. Nil means UNKNOWN, not disconnected or available.
	Connected           func(state.AgentRef) bool
	MaxWorkers          *int
	ConfigurationSource string
}
type Reader struct {
	primary, repositoryID string
	options               Options
}

// Open resolves only local Git identity once (also for watch). It never opens a
// registry, initializes/migrates authority, runs recon, or executes validation.
func Open(workspace string, options Options) (*Reader, error) {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.MaxWorkers != nil && (*options.MaxWorkers < 1 || *options.MaxWorkers > 16) {
		return nil, errors.New("invalid display concurrency policy")
	}
	if options.MaxWorkers != nil {
		n := *options.MaxWorkers
		options.MaxWorkers = &n
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, errors.New("invalid workspace")
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil, errors.New("workspace directory unavailable")
	}
	primary, err := repository.Primary(abs)
	if err != nil {
		// A new plain directory can be inspected without creating Git or state.
		for _, name := range []string{"state.json", "agentmeeting.md"} {
			if _, e := os.Stat(filepath.Join(abs, "multi-agent-orchestrator", name)); !errors.Is(e, os.ErrNotExist) {
				return nil, ErrAuthority
			}
		}
		return &Reader{primary: abs, options: options}, nil
	}
	id, err := repository.Identity(primary)
	if err != nil {
		return nil, ErrAuthority
	}
	return &Reader{primary: primary, repositoryID: id, options: options}, nil
}

func (r *Reader) Capture(ctx context.Context) (Snapshot, error) {
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		s, err := state.ReadOnly(r.primary, r.repositoryID)
		if errors.Is(err, os.ErrNotExist) {
			if _, e := os.Stat(filepath.Join(r.primary, "multi-agent-orchestrator")); !errors.Is(e, os.ErrNotExist) {
				return Snapshot{}, ErrAuthority
			}
			ledger, e := coordination.Observe(r.primary, nil)
			if e != nil || ledger.Present {
				return Snapshot{}, ErrAuthority
			}
			// Do not label a concurrently initialized repository empty.
			if _, e := state.ReadOnly(r.primary, r.repositoryID); !errors.Is(e, os.ErrNotExist) {
				continue
			}
			return project(r.primary, r.repositoryID, nil, ledger, r.options)
		}
		if err != nil || r.repositoryID == "" {
			return Snapshot{}, ErrAuthority
		}
		ledger, err := coordination.Observe(r.primary, s.Tasks)
		if err != nil {
			return Snapshot{}, ErrAuthority
		}
		view, err := project(r.primary, r.repositoryID, &s, ledger, r.options)
		if err != nil {
			return Snapshot{}, err
		}
		after, err := state.ReadOnly(r.primary, r.repositoryID)
		if err != nil {
			return Snapshot{}, ErrAuthority
		}
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		// Atomic state files contain registry/tasks/bindings in one revision.
		// A stable monotonic revision brackets the single atomic ledger read and
		// host observations. Cross-resource intermediate states remain explicit.
		if s.Authority == after.Authority {
			return view, nil
		}
	}
	return Snapshot{}, ErrChanging
}

func (r *Reader) Workspace() string { return r.primary }
