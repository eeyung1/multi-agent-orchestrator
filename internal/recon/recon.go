// Package recon supplies bounded, read-only project context to a proposal model.
package recon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
	"github.com/eeyung1/multi-agent-orchestrator/internal/repository"
	"github.com/eeyung1/multi-agent-orchestrator/internal/scheduler"
)

type File = planning.File
type Existing = planning.Existing
type Report = planning.Report

func Inspect(workspace, objective string) (Report, error) {
	primary, err := repository.Primary(workspace)
	if err != nil {
		return Report{}, err
	}
	r := Report{Workspace: primary}
	r.Head, err = git(primary, "rev-parse", "HEAD")
	if err != nil {
		return r, err
	}
	listed, err := git(primary, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return r, err
	}
	seen := map[string]bool{}
	for _, path := range strings.Split(listed, "\x00") {
		if path == "" || excluded(path) || seen[path] {
			continue
		}
		seen[path] = true
		r.Files = append(r.Files, path)
	}
	sort.Strings(r.Files)
	if len(r.Files) > 20000 {
		return r, fmt.Errorf("repository inventory exceeds 20000 files; narrow the target repository")
	}
	for _, path := range r.Files {
		if instruction(path) {
			f, err := read(primary, path, 64<<10)
			if err != nil {
				return r, fmt.Errorf("read instructions: %w", err)
			}
			r.Instructions = append(r.Instructions, f)
		}
		switch filepath.Base(path) {
		case "agent_coordination.py", "agent-coordination.py", "coordination.py", "agent_claims.py", "claims.py":
			f, err := read(primary, path, 128<<10)
			if err != nil {
				return r, err
			}
			r.CoordinationFiles = append(r.CoordinationFiles, f)
		}
	}
	// Inherited AGENTS.md files can apply even though they are outside Git.
	for dir := filepath.Dir(primary); ; dir = filepath.Dir(dir) {
		path := filepath.Join(dir, "AGENTS.md")
		if _, err := os.Lstat(path); err == nil {
			f, err := read(dir, "AGENTS.md", 64<<10)
			if err != nil {
				return r, err
			}
			f.Path = path
			r.Instructions = append(r.Instructions, f)
		} else if !os.IsNotExist(err) {
			return r, err
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	r.InstructionDigest = digest(struct{ Instructions, Coordination []File }{r.Instructions, r.CoordinationFiles})
	budget := 192 << 10
	paths := append([]string(nil), r.Files...)
	words := strings.Fields(strings.ToLower(objective))
	score := func(path string) int {
		lower := strings.ToLower(path)
		n := 0
		for _, key := range []string{"readme", "roadmap", "todo", "spec", "test", "go.mod", "package.json", "pyproject"} {
			if strings.Contains(lower, key) {
				n += 2
			}
		}
		for _, word := range words {
			if len(word) > 3 && strings.Contains(lower, strings.Trim(word, ".,:;!?")) {
				n += 5
			}
		}
		return n
	}
	sort.SliceStable(paths, func(i, j int) bool { return score(paths[i]) > score(paths[j]) })
	for _, path := range paths {
		if instruction(path) {
			continue
		}
		if budget <= 0 {
			r.Limitations = append(r.Limitations, "Source context was bounded; workers must inspect their implementation and tests.")
			break
		}
		f, err := read(primary, path, 12<<10)
		if err != nil {
			continue
		}
		if strings.IndexByte(f.Content, 0) >= 0 {
			continue
		}
		r.Context = append(r.Context, f)
		budget -= len(f.Content)
	}
	dirty, err := git(primary, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return r, err
	}
	for _, entry := range strings.Split(dirty, "\x00") {
		if len(entry) > 3 {
			path := entry[3:]
			if !excluded(path) {
				r.DirtyPaths = append(r.DirtyPaths, path)
			}
		}
	}
	s, err := scheduler.Read(primary)
	if err != nil {
		return r, err
	}
	for key, t := range s.Tasks {
		r.Existing = append(r.Existing, Existing{ID: key, Scope: t.Scope, Status: t.Status, HeadSHA: s.Results[t.ResultID].HeadSHA, ReviewStatus: s.Reviews[t.ObjectiveID].Status})
	}
	for _, claim := range s.Claims {
		r.ClaimedScopes = append(r.ClaimedScopes, claim.Scope)
	}
	claims, err := coordination.Claims(primary)
	if err != nil {
		return r, err
	}
	for _, claim := range claims {
		r.ClaimedScopes = append(r.ClaimedScopes, claim.Scope)
	}
	sort.Slice(r.Existing, func(i, j int) bool { return r.Existing[i].ID < r.Existing[j].ID })
	sort.Strings(r.ClaimedScopes)
	return r, nil
}

func InstructionsDigest(workspace string) (string, error) {
	r, err := Inspect(workspace, "")
	return r.InstructionDigest, err
}
func instruction(path string) bool {
	switch filepath.Base(path) {
	case "AGENTS.md", "CLAUDE.md", "GEMINI.md", "CONTRIBUTING.md":
		return true
	}
	return false
}
func excluded(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		lower := strings.ToLower(part)
		if part == ".git" || part == ".agent-worktrees" || part == "multi-agent-orchestrator" || part == "node_modules" || part == ".venv" || part == "vendor" || part == "__pycache__" || strings.HasPrefix(part, ".orchestrator-") || strings.HasPrefix(part, ".agent-") || strings.HasPrefix(lower, ".env") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") || strings.Contains(lower, "credentials") {
			return true
		}
	}
	return false
}
func read(root, path string, limit int) (File, error) {
	full := filepath.Join(root, filepath.FromSlash(path))
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return File{}, err
	}
	if real != filepath.Clean(full) {
		return File{}, fmt.Errorf("refusing symlink context %s", path)
	}
	info, err := os.Stat(full)
	if err != nil {
		return File{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return File{}, fmt.Errorf("context file %s exceeds its size/type boundary", path)
	}
	b, err := os.ReadFile(full)
	return File{Path: path, Content: string(b)}, err
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func git(dir string, args ...string) (string, error) {
	c := exec.Command("git", args...)
	c.Dir = dir
	b, err := c.Output()
	return strings.TrimSuffix(string(b), "\n"), err
}
