package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
	"github.com/eeyung1/multi-agent-orchestrator/internal/providers"
)

type Worker struct {
	Adapter string   `json:"adapter,omitempty"`
	Program string   `json:"program"`
	Args    []string `json:"args"`
}
type Config struct {
	// Explicit trusted allowlist; managed workers win when both match.
	EnrolledCapabilities []agentprotocol.Capability   `json:"enrolled_capabilities,omitempty"`
	AcceptanceSeconds    int                          `json:"acceptance_seconds,omitempty"`
	Runtime              *EnrolledWorkers             `json:"-"`
	Planner              planner.Provider             `json:"planner"`
	Workers              map[string]Worker            `json:"workers"`
	Validators           map[string]execution.Command `json:"validators"`
	RequiredValidators   []string                     `json:"required_validators,omitempty"`
	AllowedScopes        []string                     `json:"allowed_scopes"`
	MaxWorkers           int                          `json:"max_workers"`
	TimeoutSeconds       int                          `json:"timeout_seconds"`
	HeartbeatSeconds     int                          `json:"heartbeat_seconds"`
	RepairAttempts       int                          `json:"repair_attempts"`

	// WorktreePreparation is trusted human policy, never part of a model proposal.
	WorktreePreparation []execution.Command `json:"worktree_preparation,omitempty"`
}

func LoadConfig(path string) (Config, error) {
	c, err := ReadConfig(path)
	if err != nil {
		return c, err
	}
	return c, c.Validate()
}

// ReadConfig reads trusted policy before interactive built-in activation. It
// deliberately does not invent a worker or any fallback validation command.
func ReadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if len(b) > 1<<20 {
		return Config{}, fmt.Errorf("configuration exceeds 1 MiB")
	}
	var c Config
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	var more any
	if err := d.Decode(&more); err != io.EOF {
		return c, fmt.Errorf("configuration contains trailing data")
	}
	return c, nil
}
func (c *Config) Validate() error {
	if err := c.ValidatePolicy(); err != nil {
		return err
	}
	if err := c.Planner.ValidateDefinition(); err != nil {
		return err
	}
	if len(c.Workers) == 0 && len(c.EnrolledCapabilities) == 0 {
		return fmt.Errorf("select at least one worker capability")
	}
	for name, w := range c.Workers {
		if name == "" || w.Program == "" {
			return fmt.Errorf("invalid worker configuration")
		}
		if w.Adapter != "" {
			if name != w.Adapter || len(w.Args) != 0 {
				return fmt.Errorf("built-in worker capabilities/arguments cannot be overridden; use a separate custom provider")
			}
			if _, err := providers.Lookup(w.Adapter); err != nil {
				return err
			}
			if !filepath.IsAbs(w.Program) {
				return fmt.Errorf("built-in worker executable must be registry-resolved")
			}
		} else if _, err := exec.LookPath(w.Program); err != nil {
			return fmt.Errorf("worker %s: %w", name, err)
		}
	}
	return nil
}

func (c *Config) ValidatePolicy() error {
	seen := map[agentprotocol.Capability]bool{}
	for _, cap := range c.EnrolledCapabilities {
		if cap.Validate() != nil || seen[cap] {
			return fmt.Errorf("invalid or duplicate enrolled capability")
		}
		seen[cap] = true
	}
	if c.AcceptanceSeconds < 0 || c.AcceptanceSeconds > 300 {
		return fmt.Errorf("acceptance_seconds must be 0 (30s default) to 300")
	}
	if c.MaxWorkers == 0 {
		c.MaxWorkers = 3
	}
	if c.MaxWorkers < 1 || c.MaxWorkers > 16 {
		return fmt.Errorf("max_workers must be between 1 and 16")
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 1800
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 86400 {
		return fmt.Errorf("timeout_seconds must be between 1 and 86400")
	}
	if c.HeartbeatSeconds == 0 {
		c.HeartbeatSeconds = 30
	}
	if c.HeartbeatSeconds < 1 {
		return fmt.Errorf("heartbeat_seconds must be positive")
	}
	if c.RepairAttempts < 0 || c.RepairAttempts > 3 {
		return fmt.Errorf("repair_attempts must be between 0 and 3")
	}
	if len(c.AllowedScopes) == 0 {
		c.AllowedScopes = []string{"."}
	}
	if len(c.Validators) == 0 {
		return fmt.Errorf("configure at least one validator; validation cannot be skipped")
	}
	for name, v := range c.Validators {
		if name == "" || v.Program == "" {
			return fmt.Errorf("invalid validator configuration")
		}
		if _, err := exec.LookPath(v.Program); err != nil {
			return fmt.Errorf("validator %s: %w", name, err)
		}
	}
	for _, name := range c.RequiredValidators {
		if _, ok := c.Validators[name]; !ok {
			return fmt.Errorf("required validator is not configured: %s", name)
		}
	}
	for i, command := range c.WorktreePreparation {
		if _, err := providers.ResolveTrustedExecutable(command.Program, ""); err != nil {
			return fmt.Errorf("worktree preparation command %d: %w", i+1, err)
		}
	}
	return nil
}
func (c Config) Policy() objective.Policy {
	p := objective.Policy{AllowedScopes: c.AllowedScopes, Validators: map[string]bool{}, Capabilities: map[string]bool{}, RequiredValidators: c.RequiredValidators}
	for name := range c.Workers {
		p.Capabilities[name] = true
	}
	for _, cap := range c.EnrolledCapabilities {
		p.Capabilities[string(cap)] = true
	}
	for name := range c.Validators {
		p.Validators[name] = true
	}
	return p
}
func (c Config) Capabilities() []string {
	var names []string
	for name := range c.Policy().Capabilities {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func (c Config) Checks() []string {
	var names []string
	for name := range c.Validators {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c Config) ValidateAdapters(workspace string) error {
	for i, command := range c.WorktreePreparation {
		if _, err := providers.ResolveTrustedExecutable(command.Program, workspace); err != nil {
			return fmt.Errorf("worktree preparation command %d: %w", i+1, err)
		}
	}
	if err := c.Planner.ValidateAdapter(workspace); err != nil {
		return err
	}
	for _, w := range c.Workers {
		if w.Adapter != "" {
			if err := providers.ValidateBinding(w.Adapter, w.Program, workspace); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c Config) ProviderSelection() (string, []string) {
	var workers []string
	for _, w := range c.Workers {
		if w.Adapter != "" {
			workers = append(workers, w.Adapter)
		} else {
			return c.Planner.Adapter, nil
		}
	}
	sort.Strings(workers)
	return c.Planner.Adapter, workers
}
