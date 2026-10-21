package review

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

// CommitOwned lets bounded CLI workers leave Git control files read-only. Only
// the Go controller stages/commits eligible product paths, never integrates.
func CommitOwned(t state.Task, guard func() error, run func(...string) error) (string, error) {
	if guard == nil || run == nil {
		return "", errors.New("assignment guard and supervised Git runner are required for controller commits")
	}
	if err := guard(); err != nil {
		return "", err
	}
	if t.Worktree == "" || t.Branch == "" || t.BaseSHA == "" {
		return "", errors.New("assignment metadata is incomplete")
	}
	branch, err := git(t.Worktree, "branch", "--show-current")
	if err != nil || branch != t.Branch {
		return "", errors.New("worker changed its assigned branch")
	}
	if err := execGit(t.Worktree, "merge-base", "--is-ancestor", t.BaseSHA, "HEAD"); err != nil {
		return "", errors.New("worker changed its assigned base history")
	}
	paths := map[string]bool{}
	for query, args := range [][]string{{"diff", "--name-only", "--no-renames", "-z", t.BaseSHA, "HEAD"}, {"diff", "--name-only", "--no-renames", "-z"}, {"diff", "--cached", "--name-only", "--no-renames", "-z", "HEAD"}, {"ls-files", "--others", "--exclude-standard", "-z"}} {
		out, err := git(t.Worktree, args...)
		if err != nil {
			return "", err
		}
		for _, path := range strings.Split(out, "\x00") {
			if path == "" {
				continue
			}
			canonical, err := state.CanonicalScope(t.Worktree, path)
			if err != nil {
				return "", err
			}
			if canonical != t.Scope && !strings.HasPrefix(canonical, t.Scope+"/") {
				return "", fmt.Errorf("worker edited an unowned path: %s", canonical)
			}
			if query > 0 {
				paths[path] = true
			}
		}
	}
	dirty, err := git(t.Worktree, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	if dirty != "" {
		if len(paths) == 0 {
			return "", errors.New("unparsed dirty output; cannot safely commit")
		}
		var owned []string
		for path := range paths {
			owned = append(owned, path)
		}
		sort.Strings(owned)
		if err := guard(); err != nil {
			return "", err
		}
		if err := run(append([]string{"--literal-pathspecs", "add", "--"}, owned...)...); err != nil {
			return "", fmt.Errorf("stage owned output: %w", err)
		}
		if err := guard(); err != nil {
			return "", err
		}
		if err := execGit(t.Worktree, "diff", "--cached", "--quiet"); err != nil {
			if err := run("commit", "-m", "Implement "+t.Feature); err != nil {
				return "", fmt.Errorf("commit owned output: %w", err)
			}
		}
	}
	if err := guard(); err != nil {
		return "", err
	}
	return CheckOutput(t)
}
