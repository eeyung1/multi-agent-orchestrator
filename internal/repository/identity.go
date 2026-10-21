package repository

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Identity returns one stable local repository identity for primary checkouts,
// subdirectories, and linked worktrees. The common Git directory is resolved
// relative to the canonical primary checkout and is never supplied by callers.
func Identity(workspace string) (string, error) {
	primary, err := Primary(workspace)
	if err != nil { return "", err }
	common, err := git(primary, "rev-parse", "--git-common-dir")
	if err != nil { return "", fmt.Errorf("resolve git common directory: %w", err) }
	if !filepath.IsAbs(common) { common = filepath.Join(primary, common) }
	common, err = filepath.Abs(common)
	if err != nil { return "", err }
	common = filepath.Clean(common)
	top, err := filepath.Abs(primary)
	if err != nil { return "", err }
	return "local-git:" + filepath.ToSlash(strings.TrimSpace(top)) + "#" + filepath.ToSlash(common), nil
}
