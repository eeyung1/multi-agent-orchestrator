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
// must explicitly authorize required checks; discovered scripts are never run.
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
	// A bootstrap executable must come from trusted PATH, not package-local bin
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
	if err := c.ValidatePolicy(); err != nil {
		return err
	}
	yes, err = confirm(reader, out, "Save this trusted validation policy for future runs at "+savePath+"? [y/N] ")
	if err != nil {
		return err
	}
	if !yes {
		return nil
	}
	// Save policy only. Built-in provider choices remain selectable on each new
	// objective; admitted objectives preserve their own provider selection.
	policy := *c
	policy.Planner = controller.Config{}.Planner
	policy.Workers = nil
	b, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(savePath), 0700); err != nil {
		return err
	}
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
	if closeErr != nil {
		return closeErr
	}
	fmt.Fprintln(out, "Trusted validation policy saved.")
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
