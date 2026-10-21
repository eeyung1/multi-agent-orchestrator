// Package providers is a closed registry of built-in CLI adapters. Discovery
// examines PATH/filesystem metadata only: it never runs a probe or project code.
package providers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Adapter struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Executable string `json:"executable"`
	Planner    bool   `json:"planner"`
	Worker     bool   `json:"worker"`
}

type Detected struct {
	Adapter
	Path        string `json:"path,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

func Registry() []Adapter {
	return []Adapter{{"codex", "Codex", "codex", true, true}, {"claude", "Claude Code", "claude", true, true}, {"gemini", "Gemini CLI", "gemini", true, true}}
}

func Lookup(id string) (Adapter, error) {
	for _, a := range Registry() {
		if a.ID == id {
			return a, nil
		}
	}
	return Adapter{}, fmt.Errorf("unknown built-in provider %q (supported: codex, claude, gemini)", id)
}

func Discover(workspace string) []Detected {
	var out []Detected
	for _, a := range Registry() {
		path, err := ResolveExecutable(a.Executable, workspace)
		d := Detected{Adapter: a, Path: path}
		if err != nil {
			d.Unavailable = err.Error()
		}
		out = append(out, d)
	}
	return out
}

func Resolve(id, workspace string) (Detected, error) {
	a, err := Lookup(id)
	if err != nil {
		return Detected{}, err
	}
	path, err := ResolveExecutable(a.Executable, workspace)
	if err != nil {
		return Detected{}, fmt.Errorf("provider %s unavailable: %w; install/authenticate it yourself or select another installed provider", id, err)
	}
	return Detected{Adapter: a, Path: path}, nil
}

// ResolveExecutable ignores relative PATH entries and any executable/directory
// controlled by the target or launching Git repository, including symlinks.
// The returned absolute real path pins invocation against later PATH shadowing.
func ResolveExecutable(name, workspace string) (string, error) {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("expected a registered executable name")
	}
	for _, dir := range safeDirs(workspace) {
		candidate := filepath.Join(dir, name)
		if path, err := trustedPath(candidate, workspace); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s was not found on a trusted absolute PATH entry (project-local executables are excluded)", name)
}

func ValidateBinding(id, path, workspace string) error {
	d, err := Resolve(id, workspace)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(path) || path != d.Path {
		return fmt.Errorf("provider %s executable binding changed or is not registry-resolved; select it again and explicitly authorize new work", id)
	}
	return nil
}

func trustedPath(path, workspace string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("relative executable path")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if linkedCheckout(filepath.Dir(path)) || linkedCheckout(filepath.Dir(real)) {
		return "", fmt.Errorf("project-controlled executable in a linked Git checkout")
	}
	for _, root := range blockedRoots(workspace) {
		if within(root, path) || within(root, real) {
			return "", fmt.Errorf("project-controlled executable")
		}
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("not an executable file")
	}
	return real, nil
}

func safeDirs(workspace string) []string {
	var dirs []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue
		}
		if linkedCheckout(dir) || linkedCheckout(real) {
			continue
		}
		blocked := false
		for _, root := range blockedRoots(workspace) {
			if within(root, dir) || within(root, real) {
				blocked = true
				break
			}
		}
		if !blocked {
			dirs = append(dirs, real)
		}
	}
	return dirs
}

// Environment also removes project-local interpreter resolution (for example a
// project-supplied `node` ahead of a trusted npm-installed agent executable).
func Environment(workspace string) []string {
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PATH=") {
			env = append(env, entry)
		}
	}
	return append(env, "PATH="+strings.Join(safeDirs(workspace), string(os.PathListSeparator)))
}

func blockedRoots(workspace string) []string {
	var roots []string
	add := func(path string) {
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		if real, e := filepath.EvalSymlinks(abs); e == nil {
			abs = real
		}
		roots = append(roots, abs)
		for dir := abs; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			marker := filepath.Join(dir, ".git")
			info, e := os.Stat(marker)
			if e != nil {
				continue
			}
			if info.IsDir() {
				if _, e := os.Stat(filepath.Join(marker, "HEAD")); e != nil {
					continue
				}
			}
			roots = append(roots, dir)
			break
		}
	}
	add(workspace)
	cwd, _ := os.Getwd()
	if cwd != "" {
		for dir := cwd; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			marker := filepath.Join(dir, ".git")
			info, e := os.Stat(marker)
			if e != nil {
				continue
			}
			if info.IsDir() {
				if _, e := os.Stat(filepath.Join(marker, "HEAD")); e != nil {
					continue
				}
			}
			add(dir)
			break
		}
	}
	return roots
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// A linked worktree may live outside the primary directory. Metadata-only
// ancestor discovery excludes its executables too, without running Git.
func checkoutRoot(path string) string {
	for dir := path; ; dir = filepath.Dir(dir) {
		info, err := os.Stat(filepath.Join(dir, ".git"))
		if err == nil {
			if !info.IsDir() {
				return dir
			}
			if _, err := os.Stat(filepath.Join(dir, ".git", "HEAD")); err == nil {
				return dir
			}
		}
		if dir == filepath.Dir(dir) {
			return ""
		}
	}
}

func linkedCheckout(path string) bool {
	root := checkoutRoot(path)
	if root == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil && !info.IsDir()
}

// ProjectControlled is used when an explicitly chosen custom config needs a
// separate trust decision. Built-in definitions cannot be overridden by JSON.
func ProjectControlled(path, workspace string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		real = abs
	}
	for _, root := range blockedRoots(workspace) {
		if within(root, abs) || within(root, real) {
			return true
		}
	}
	return false
}
