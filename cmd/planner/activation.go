package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/controller"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
	"github.com/eeyung1/multi-agent-orchestrator/internal/providers"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

func activate(reader *bufio.Reader, out io.Writer, workspace string, c *controller.Config, plannerID, workerIDs string, saved state.Objective) error {
	if c.Planner.Adapter != "" && plannerID == "" {
		if len(c.Planner.Args) > 0 || c.Planner.Endpoint != "" || c.Planner.Model != "" || c.Planner.APIKeyEnv != "" {
			return fmt.Errorf("built-in Planner cannot be redefined by custom configuration")
		}
		plannerID = c.Planner.Adapter
		resolved, err := providers.Resolve(plannerID, workspace)
		if err != nil {
			return err
		}
		if c.Planner.Program != "" && c.Planner.Program != resolved.Path {
			return fmt.Errorf("configured built-in executable is not registry-resolved")
		}
	}
	if saved.PlannerProvider != "" {
		if plannerID != "" && plannerID != saved.PlannerProvider {
			return fmt.Errorf("saved objective uses Planner %s; switching requires a new authorization", saved.PlannerProvider)
		}
		plannerID = saved.PlannerProvider
	}
	if len(saved.WorkerProviders) > 0 {
		if workerIDs != "" && !samePool(strings.Split(workerIDs, ","), saved.WorkerProviders) {
			return fmt.Errorf("saved objective uses worker pool %v; switching requires a new authorization", saved.WorkerProviders)
		}
		workerIDs = strings.Join(saved.WorkerProviders, ",")
	}
	if plannerID == "" && c.Planner.Program == "" && c.Planner.Endpoint == "" {
		available := available(workspace, true)
		if len(available) == 0 {
			return fmt.Errorf("no compatible Planner CLI is available; install/authenticate Codex, Claude Code or Gemini CLI yourself, or use --config for a trusted custom provider")
		}
		fmt.Fprintln(out, "Available Planner agents:")
		printChoices(out, available)
		choice, err := ask(reader, out, "Select Planner [1]: ")
		if err != nil {
			return err
		}
		index, err := choiceIndex(choice, len(available))
		if err != nil {
			return err
		}
		plannerID = available[index].ID
	}
	if plannerID != "" {
		detected, err := providers.Resolve(plannerID, workspace)
		if err != nil {
			return err
		}
		c.Planner = planner.Provider{Adapter: plannerID, Program: detected.Path}
		fmt.Fprintf(out, "Planner activated: %s\n", detected.Name)
	} else {
		fmt.Fprintln(out, "Planner activated: trusted custom provider (not a built-in adapter)")
	}
	if workerIDs == "" && len(c.Workers) == 0 {
		available := available(workspace, false)
		if len(available) == 0 {
			return fmt.Errorf("no compatible worker CLI is available; install/authenticate a supported CLI or explicitly configure custom workers")
		}
		fmt.Fprintln(out, "Available worker agents:")
		printChoices(out, available)
		choice, err := ask(reader, out, "Select worker pool (comma-separated numbers) [1]: ")
		if err != nil {
			return err
		}
		if choice == "" {
			choice = "1"
		}
		var ids []string
		for _, part := range strings.Split(choice, ",") {
			index, err := choiceIndex(strings.TrimSpace(part), len(available))
			if err != nil {
				return err
			}
			ids = append(ids, available[index].ID)
		}
		workerIDs = strings.Join(ids, ",")
	}
	if workerIDs != "" {
		c.Workers = map[string]controller.Worker{}
		for _, id := range strings.Split(workerIDs, ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				return fmt.Errorf("worker pool contains an empty provider")
			}
			if _, exists := c.Workers[id]; exists {
				return fmt.Errorf("duplicate worker provider %s; use max-workers for concurrency", id)
			}
			detected, err := providers.Resolve(id, workspace)
			if err != nil {
				return err
			}
			c.Workers[id] = controller.Worker{Adapter: id, Program: detected.Path}
			fmt.Fprintf(out, "Worker capability enabled: %s (%s)\n", id, detected.Name)
		}
	} else {
		for name, w := range c.Workers {
			if w.Adapter != "" {
				if name != w.Adapter || len(w.Args) > 0 {
					return fmt.Errorf("built-in worker cannot be redefined by custom configuration")
				}
				resolved, err := providers.Resolve(w.Adapter, workspace)
				if err != nil {
					return err
				}
				if w.Program != "" && w.Program != resolved.Path {
					return fmt.Errorf("configured built-in worker executable is not registry-resolved")
				}
				w.Program = resolved.Path
				c.Workers[name] = w
			}
		}
		fmt.Fprintf(out, "Using configured worker capabilities: %v (custom definitions remain separate from built-ins)\n", c.Capabilities())
	}
	return nil
}

func available(workspace string, planning bool) []providers.Detected {
	var out []providers.Detected
	for _, d := range providers.Discover(workspace) {
		if d.Path != "" && ((planning && d.Planner) || (!planning && d.Worker)) {
			out = append(out, d)
		}
	}
	return out
}
func printChoices(out io.Writer, list []providers.Detected) {
	for i, d := range list {
		fmt.Fprintf(out, "%d. %s [%s]\n", i+1, d.Name, d.ID)
	}
}
func choiceIndex(choice string, n int) (int, error) {
	if choice == "" {
		choice = "1"
	}
	number, err := strconv.Atoi(choice)
	if err != nil || number < 1 || number > n {
		return 0, fmt.Errorf("invalid provider selection %q", choice)
	}
	return number - 1, nil
}
func samePool(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range a {
		id = strings.TrimSpace(id)
		if seen[id] {
			return false
		}
		seen[id] = true
	}
	for _, id := range b {
		if !seen[id] {
			return false
		}
	}
	return true
}

func listProviders(out io.Writer, workspace string) {
	for _, d := range providers.Discover(workspace) {
		status := "available (authentication/version not probed)"
		if d.Path == "" {
			status = d.Unavailable
		}
		fmt.Fprintf(out, "%s [%s] Planner=%t Worker=%t: %s\n", d.Name, d.ID, d.Planner, d.Worker, status)
	}
}
