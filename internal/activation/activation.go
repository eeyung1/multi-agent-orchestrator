package activation

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	coordinationDir = "multi-agent-orchestrator"
	lockDir         = ".agent-bootstrap-lock"
)

type Result struct {
	SessionID      string
	State          string
	CoordinationDir string
}

func Activate(workspace, agent, role string) (Result, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return Result{}, fmt.Errorf("workspace is not a directory: %s", root)
	}
	primary, err := primaryRoot(root)
	if err != nil {
		return Result{}, err
	}

	coord := filepath.Join(primary, coordinationDir)
	if err := os.MkdirAll(coord, 0o755); err != nil {
		return Result{}, err
	}

	lock := filepath.Join(primary, lockDir)
	if err := os.Mkdir(lock, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Result{}, fmt.Errorf("coordination bootstrap is already locked: %s", lock)
		}
		return Result{}, err
	}
	defer os.RemoveAll(lock)

	session, err := sessionID(agent)
	if err != nil {
		return Result{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)

	if err := ensureFile(filepath.Join(coord, "agentmeeting.md"), agentMeetingTemplate); err != nil {
		return Result{}, err
	}
	if err := ensureFile(filepath.Join(coord, "coordination.md"), coordinationTemplate); err != nil {
		return Result{}, err
	}
	if err := ensureFile(filepath.Join(coord, "resolution.md"), resolutionTemplate); err != nil {
		return Result{}, err
	}

	line := fmt.Sprintf("| %s | %s | ACTIVE | %s | activation | - | bootstrap |\n", session, role, now)
	if err := appendFile(filepath.Join(coord, "agentmeeting.md"), line); err != nil {
		return Result{}, err
	}

	return Result{SessionID: session, State: "ACTIVE", CoordinationDir: coord}, nil
}

func primaryRoot(root string) (string, error) {
	current := root
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no git repository found from %s", root)
		}
		current = parent
	}
}

func sessionID(agent string) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	clean := strings.NewReplacer(" ", "-", "|", "-", "/", "-").Replace(strings.TrimSpace(agent))
	if clean == "" {
		return "", errors.New("agent identity is empty")
	}
	return fmt.Sprintf("%s-%s-%s", clean, time.Now().UTC().Format("20060102"), hex.EncodeToString(b[:])), nil
}

func ensureFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

func appendFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

const agentMeetingTemplate = `# Agent Meeting

## Active Agents

| Agent/session ID | Role | Status | Heartbeat UTC | Feature | Branch/worktree | Claim reference |
| --- | --- | --- | --- | --- | --- | --- |
`

const coordinationTemplate = `# Coordination

## Project Context

## Standing Permissions

## Owner Decisions

## Blockers and Proposed Decisions

## Automated Actions
`

const resolutionTemplate = `# Resolution

Append-only completed-work evidence.
`
