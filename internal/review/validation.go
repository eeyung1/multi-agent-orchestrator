package review

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

// ErrValidationBoundary forbids automatic worker repairs after history,
// ownership, or cleanup integrity failures. Preserve the checkout for inspection.
var ErrValidationBoundary = errors.New("validation boundary rejected")

// ValidationBoundary attributes filesystem mutations to trusted validators only
// after strict committed/clean/scoped output review. It never resets Git history.
type ValidationBoundary struct {
	Head    string
	task    state.Task
	gitDir  string
	before  map[string]fs.FileMode
	tracked map[string]bool
}

func BeginValidation(t state.Task, guard func() error) (*ValidationBoundary, error) {
	if guard == nil {
		return nil, errors.New("validation requires an assignment guard")
	}
	if err := guard(); err != nil {
		return nil, err
	}
	head, err := CheckOutput(t)
	if err != nil {
		return nil, err
	}
	dir, err := git(t.Worktree, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	before, err := validationInventory(t.Worktree)
	if err != nil {
		return nil, err
	}
	// Include ignored files/directories in the baseline so local dependencies
	// are preserved, even if a validator stages them or changes ignore rules.
	names, err := validationPaths(t.Worktree, "ls-tree", "-r", "--name-only", "-z", head)
	if err != nil {
		return nil, err
	}
	tracked := make(map[string]bool, len(names))
	for _, name := range names {
		mode, exists := before[name]
		if !exists || mode.IsDir() {
			return nil, fmt.Errorf("validation boundary does not support sparse/missing tracked paths or submodules: %q", name)
		}
		tracked[name] = true
	}
	b := &ValidationBoundary{Head: head, task: t, gitDir: dir, before: before, tracked: tracked}
	if err := b.Verify(guard); err != nil {
		return nil, err
	}
	return b, nil
}

// Verify is used after every validator and immediately before each cleanup
// mutation. A branch/HEAD change is evidence to retain, never history to undo.
func (b *ValidationBoundary) Verify(guard func() error) error {
	if err := b.verify(guard); err != nil {
		return fmt.Errorf("%w: %w", ErrValidationBoundary, err)
	}
	return nil
}

func (b *ValidationBoundary) verify(guard func() error) error {
	if guard == nil {
		return errors.New("validation requires an assignment guard")
	}
	if err := guard(); err != nil {
		return err
	}
	dir, err := git(b.task.Worktree, "rev-parse", "--absolute-git-dir")
	if err != nil || dir != b.gitDir {
		return errors.New("validator changed the assigned Git worktree identity")
	}
	root, err := git(b.task.Worktree, "rev-parse", "--show-toplevel")
	if err != nil || filepath.Clean(root) != filepath.Clean(b.task.Worktree) {
		return errors.New("validator changed the Git working-tree root")
	}
	branch, err := git(b.task.Worktree, "branch", "--show-current")
	if err != nil || branch != b.task.Branch {
		return errors.New("validator changed the assigned branch")
	}
	head, err := git(b.task.Worktree, "rev-parse", "HEAD")
	if err != nil || head != b.Head {
		return errors.New("validator changed HEAD; history was preserved without cleanup")
	}
	return nil
}

// Restore uses only explicit literal paths under the captured checkout. run must
// be the controller's guarded/supervised Git runner, also for failing validators.
func (b *ValidationBoundary) Restore(guard func() error, run func(...string) error) (string, error) {
	summary, err := b.restore(guard, run)
	if err != nil {
		return "", fmt.Errorf("%w: cleanup: %w", ErrValidationBoundary, err)
	}
	return summary, nil
}

func (b *ValidationBoundary) restore(guard func() error, run func(...string) error) (string, error) {
	if run == nil {
		return "", errors.New("supervised Git runner is required")
	}
	if err := b.Verify(guard); err != nil {
		return "", err
	}
	// Preflight before even a tracked restore: a validator may have replaced a
	// tracked file with a directory containing another repository.
	if _, err := validationInventory(b.task.Worktree); err != nil {
		return "", err
	}
	changed := map[string]bool{}
	// Query index and worktree separately: staged changes can cancel out in a
	// combined HEAD-to-worktree diff. Disable rename collapsing for explicit paths.
	for _, args := range [][]string{
		{"diff", "--name-only", "--no-renames", "-z"},
		{"diff", "--cached", "--name-only", "--no-renames", "-z", b.Head},
	} {
		names, err := validationPaths(b.task.Worktree, args...)
		if err != nil {
			return "", err
		}
		for _, name := range names {
			changed[name] = true
		}
	}
	var names []string
	for name := range changed {
		names = append(names, name)
	}
	sort.Strings(names)
	mutate := func(name string, args ...string) error {
		if err := b.Verify(guard); err != nil {
			return err
		}
		if err := validationPath(b.task.Worktree, name); err != nil {
			return err
		}
		return run(append([]string{"--literal-pathspecs"}, args...)...)
	}
	for _, name := range names {
		// Restore staged additions in the index only. Their working copies may
		// have existed as ignored files before validation and must be preserved.
		if err := mutate(name, "restore", "--source="+b.Head, "--staged", "--", name); err != nil {
			return "", err
		}
		if b.tracked[name] {
			if err := mutate(name, "restore", "--source="+b.Head, "--worktree", "--", name); err != nil {
				return "", err
			}
		}
	}
	after, err := validationInventory(b.task.Worktree)
	if err != nil {
		return "", err
	}
	var added []string
	for name := range after {
		if _, existed := b.before[name]; existed {
			continue
		}
		// Only select new roots, never a directory that existed at the clean
		// boundary. This also removes newly created empty or ignored directories.
		if _, parentExisted := b.before[path.Dir(name)]; path.Dir(name) == "." || parentExisted {
			added = append(added, name)
		}
	}
	sort.Strings(added)
	for _, name := range added {
		// Single force deliberately refuses nested repositories. Never use a
		// repository-wide clean, or force deletion of another Git checkout.
		if err := mutate(name, "clean", "-f", "-d", "-x", "--", name); err != nil {
			return "", err
		}
	}
	if err := b.Verify(guard); err != nil {
		return "", err
	}
	final, err := validationInventory(b.task.Worktree)
	if err != nil {
		return "", err
	}
	for name := range final {
		if _, existed := b.before[name]; !existed {
			return "", fmt.Errorf("validator-created path remains after cleanup: %q", name)
		}
	}
	head, err := CheckOutput(b.task)
	if err != nil || head != b.Head {
		return "", fmt.Errorf("validation cleanup did not restore clean owned output at captured HEAD: %v", err)
	}
	if len(names) == 0 && len(added) == 0 {
		return "", nil
	}
	return fmt.Sprintf("Validator filesystem side effects restored at %s (%d index/tracked paths, %d new paths removed).", b.Head, len(names), len(added)), nil
}

func validationInventory(root string) (map[string]fs.FileMode, error) {
	entries := map[string]fs.FileMode{}
	err := filepath.WalkDir(root, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.Name() == ".git" {
			if rel != ".git" {
				return fmt.Errorf("nested Git repository makes validation cleanup ambiguous: %q", rel)
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		entries[filepath.ToSlash(rel)] = entry.Type()
		return nil
	})
	return entries, err
}

func validationPaths(root string, args ...string) ([]string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if len(output) == 0 {
		return nil, nil
	}
	if output[len(output)-1] != 0 {
		return nil, errors.New("unterminated Git path inventory")
	}
	return strings.Split(string(output[:len(output)-1]), "\x00"), nil
}

func validationPath(root, name string) error {
	if name == "" || name == "." || path.Clean(name) != name || path.IsAbs(name) || strings.ContainsAny(name, "\\\x00") {
		return fmt.Errorf("unsafe validation cleanup path: %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || strings.EqualFold(part, ".git") {
			return fmt.Errorf("unsafe validation cleanup path: %q", name)
		}
	}
	// Git may replace a leaf symlink, but cleanup must never traverse one.
	for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(parent)))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("validation cleanup path traverses a symlink: %q", name)
		}
	}
	return nil
}
