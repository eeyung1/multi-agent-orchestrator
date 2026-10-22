package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/eeyung1/multi-agent-orchestrator/internal/controller"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/providers"
)

// Bootstrap proposes familiar test vectors based only on file names. A human
// must authorize required checks and any optional preparation; setup runs none.
func bootstrap(reader *bufio.Reader, out io.Writer, workspace, savePath string, c *controller.Config) error {
	fmt.Fprintln(out, "No trusted validation policy was found. Required checks must be authorized before work can run.")
	type preset struct {
		marker, label, name, program string
		args                         []string
	}
	var choices []preset
	for _, p := range []preset{{"go.mod", "Go: go test ./...", "go-tests", "go", []string{"test", "./..."}}, {"package.json", "Node: npm test", "node-tests", "npm", []string{"test"}}, {"pyproject.toml", "Python: python3 -m pytest -q", "python-tests", "python3", []string{"-m", "pytest", "-q"}}} {
		info, err := os.Lstat(filepath.Join(workspace, p.marker))
		if err == nil && info.Mode().IsRegular() {
			choices = append(choices, p)
		}
	}
	for i, p := range choices {
		fmt.Fprintf(out, "%d. %s\n", i+1, p.label)
	}
	fmt.Fprintln(out, "custom. Enter a trusted validation executable and JSON argument array")
	selection, err := ask(reader, out, "Choose required project checks (number or custom): ")
	if err != nil {
		return err
	}
	var program, name string
	var args []string
	if selection == "custom" {
		program, err = ask(reader, out, "Validation executable (not a shell command string): ")
		if err != nil {
			return err
		}
		argJSON, err := ask(reader, out, "Validation arguments as a JSON array: ")
		if err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(argJSON), &args); err != nil {
			return fmt.Errorf("validation arguments must be a JSON string array: %w", err)
		}
		if len(args) == 0 {
			return fmt.Errorf("provide explicit validation arguments; no fallback gate will be invented")
		}
		name = "project-check"
	} else {
		if selection == "" {
			return fmt.Errorf("required validation policy was not selected")
		}
		index, err := choiceIndex(selection, len(choices))
		if err != nil {
			return err
		}
		p := choices[index]
		program = p.program
		args = p.args
		name = p.name
	}
	// A validation executable must come from trusted PATH, not package-local bin
	// shims. Advanced explicit custom configuration remains available separately.
	resolved, err := providers.ResolveExecutable(program, workspace)
	if err != nil {
		return err
	}
	git, err := providers.ResolveExecutable("git", workspace)
	if err != nil {
		return err
	}
	c.Validators = map[string]execution.Command{name: {Program: resolved, Args: args}, "diff-check": {Program: git, Args: []string{"diff", "--check", "HEAD~1", "HEAD"}}}
	c.RequiredValidators = []string{name, "diff-check"}
	fmt.Fprintf(out, "Required checks: %s %v; git diff --check HEAD~1 HEAD. No checks have run.\n", program, args)
	yes, err := confirm(reader, out, "Authorize these mandatory project validation checks? [y/N] ")
	if err != nil {
		return err
	}
	if !yes {
		return fmt.Errorf("required validation policy was not authorized")
	}
	if err := preparationPolicy(reader, out, workspace, c); err != nil {
		return err
	}
	if err := c.ValidatePolicy(); err != nil {
		return err
	}
	yes, err = confirm(reader, out, "Save this trusted validation and worktree preparation policy for future runs at "+savePath+"? [y/N] ")
	if err != nil {
		return err
	}
	if !yes {
		return nil
	}
	if err := savePolicy(savePath, *c, false); err != nil {
		return err
	}
	fmt.Fprintln(out, "Trusted validation and worktree preparation policy saved.")
	return nil
}

func configurePreparationPolicy(reader *bufio.Reader, out io.Writer, workspace, savePath string, c *controller.Config) error {
	before, _ := json.Marshal(c.WorktreePreparation)
	next := *c
	next.WorktreePreparation = append([]execution.Command(nil), c.WorktreePreparation...)
	if err := preparationPolicy(reader, out, workspace, &next); err != nil {
		return err
	}
	after, _ := json.Marshal(next.WorktreePreparation)
	if string(before) == string(after) {
		return fmt.Errorf("worktree preparation policy was not changed")
	}
	if err := next.ValidatePolicy(); err != nil {
		return err
	}
	yes, err := confirm(reader, out, "Replace the existing trusted policy with this authorized worktree preparation policy? [y/N] ")
	if err != nil {
		return err
	}
	if !yes {
		return fmt.Errorf("trusted policy update was not authorized")
	}
	if err := savePolicy(savePath, next, true); err != nil {
		return err
	}
	*c = next
	fmt.Fprintln(out, "Trusted worktree preparation policy updated. Existing objectives still require separate policy reauthorization.")
	return nil
}

func savePolicy(savePath string, c controller.Config, overwrite bool) error {
	// Save policy only. Built-in provider choices remain selectable on each new
	// objective; admitted objectives preserve their own provider selection.
	c.Planner = controller.Config{}.Planner
	c.Workers = nil
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(savePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if !overwrite {
		f, err := os.OpenFile(savePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("save policy without overwriting existing files: %w", err)
		}
		_, writeErr := f.Write(b)
		if writeErr == nil {
			writeErr = f.Sync()
		}
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	info, err := os.Lstat(savePath)
	if err != nil {
		return fmt.Errorf("inspect existing trusted policy: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("existing trusted policy is not a regular file")
	}
	f, err := os.CreateTemp(dir, ".planner-policy-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, savePath)
}

func preparationPolicy(reader *bufio.Reader, out io.Writer, workspace string, c *controller.Config) error {
	fmt.Fprintln(out, "Fresh isolated worktrees contain tracked files only; local dependencies may need preparation before a worker starts. No install command is inferred or run during setup.")
	yes, err := confirm(reader, out, "Configure an optional trusted worktree preparation command? [y/N] ")
	if err != nil || !yes {
		return err
	}
	program, err := ask(reader, out, "Preparation executable (not a shell command string): ")
	if err != nil {
		return err
	}
	argJSON, err := ask(reader, out, "Preparation arguments as a JSON array (use [] for no arguments): ")
	if err != nil {
		return err
	}
	var args []string
	if err := json.Unmarshal([]byte(argJSON), &args); err != nil || args == nil {
		return fmt.Errorf("preparation arguments must be a JSON string array")
	}
	resolved, err := providers.ResolveTrustedExecutable(program, workspace)
	if err != nil {
		return fmt.Errorf("worktree preparation: %w", err)
	}
	command := execution.Command{Program: resolved, Args: args}
	display, _ := json.Marshal(command)
	fmt.Fprintf(out, "Worktree preparation: %s\nRuns once in each assigned isolated worktree before its worker, with the configured execution timeout and guards.\n", display)
	yes, err = confirm(reader, out, "Authorize this exact worktree preparation command and arguments? [y/N] ")
	if err != nil {
		return err
	}
	if !yes {
		return fmt.Errorf("worktree preparation policy was not authorized")
	}
	c.WorktreePreparation = []execution.Command{command}
	return nil
}

func customConfigTrust(reader *bufio.Reader, out io.Writer, path, workspace string, explicit bool) error {
	if !providers.ProjectControlled(path, workspace) {
		return nil
	}
	if !explicit {
		return fmt.Errorf("default configuration resolves inside a project; use --config explicitly and review its executable policy")
	}
	yes, err := confirm(reader, out, "This project-local custom configuration can define executables. Trust this exact file for this invocation? [y/N] ")
	if err != nil {
		return err
	}
	if !yes {
		return fmt.Errorf("project-local executable policy was not trusted")
	}
	return nil
}
