package state

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// ReadOnly opens an existing atomic snapshot without initializing, migrating,
// expiring sessions, acquiring ownership, or writing audit events. Older schemas
// must be migrated explicitly by the existing authoritative startup path.
func ReadOnly(primary, repositoryID string) (Snapshot, error) {
	path := filepath.Join(primary, "multi-agent-orchestrator", stateFile)
	info, err := os.Lstat(path)
	if err != nil {
		return Snapshot{}, err
	}
	if !info.Mode().IsRegular() || repositoryID == "" {
		return Snapshot{}, errors.New("existing authoritative snapshot must be a regular file bound to a repository")
	}
	f, err := os.Open(path)
	if err != nil {
		return Snapshot{}, err
	}
	defer f.Close()
	const limit = 64 << 20
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return Snapshot{}, err
	}
	if len(b) > limit {
		return Snapshot{}, errors.New("authoritative snapshot exceeds read-only inspection limit")
	}
	return (&Store{repositoryID: repositoryID}).decode(b, false)
}
