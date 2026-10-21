package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"

	"github.com/eeyung1/multi-agent-orchestrator/internal/execution"
	"github.com/eeyung1/multi-agent-orchestrator/internal/objective"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
)

type Worker struct {
	Program string   `json:"program"`
	Args    []string `json:"args"`
}
type Config struct {
	Planner            planner.Provider             `json:"planner"`
	Workers            map[string]Worker            `json:"workers"`
	Validators         map[string]execution.Command `json:"validators"`
	RequiredValidators []string                     `json:"required_validators,omitempty"`
	AllowedScopes      []string                     `json:"allowed_scopes"`
	MaxWorkers         int                          `json:"max_workers"`
	TimeoutSeconds     int                          `json:"timeout_seconds"`
	HeartbeatSeconds   int                          `json:"heartbeat_seconds"`
	RepairAttempts     int                          `json:"repair_attempts"`
}

func LoadConfig(path string) (Config, error) {
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
	return c, c.Validate()
}
func (c *Config) Validate() error {
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
	if len(c.Workers) == 0 || len(c.Validators) == 0 {
		return fmt.Errorf("configure at least one worker capability and validator")
	}
	for name, w := range c.Workers {
		if name == "" || w.Program == "" {
			return fmt.Errorf("invalid worker configuration")
		}
		if _, err := exec.LookPath(w.Program); err != nil {
			return fmt.Errorf("worker %s: %w", name, err)
		}
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
	return nil
}
func (c Config) Policy() objective.Policy {
	p := objective.Policy{AllowedScopes: c.AllowedScopes, Validators: map[string]bool{}, Capabilities: map[string]bool{}, RequiredValidators: c.RequiredValidators}
	for name := range c.Workers {
		p.Capabilities[name] = true
	}
	for name := range c.Validators {
		p.Validators[name] = true
	}
	return p
}
func (c Config) Capabilities() []string {
	var names []string
	for name := range c.Workers {
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
