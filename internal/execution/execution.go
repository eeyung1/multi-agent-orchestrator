package execution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/coordination"
)

type Command struct {
	Program string
	Args    []string
}
type Request struct {
	Primary, Worktree, SessionID string
	Command                      Command
	Timeout                      time.Duration
	HeartbeatInterval            time.Duration
	Context                      context.Context
	Guard                        func() error
	Input                        string
	RedactArguments              bool
	Environment                  []string
	// AfterExit runs after the child/process group has exited and ownership has
	// been rechecked, before a failed command transitions the session to BLOCKED.
	// It may perform guarded cleanup; its failure cannot turn a command into PASS.
	AfterExit func(Result) error
}
type Result struct {
	ExitCode int
	Output   string
}

func Run(req Request) (Result, error) { return run(req, nil) }

// Supervise applies the same ownership, guard, timeout and operational heartbeat
// checks to a bounded attached-agent exchange. operation must honor cancellation;
// no subprocess is spawned. External processes are not an OS sandbox.
func Supervise(req Request, operation func(context.Context) error) (Result, error) {
	if operation == nil {
		return Result{}, errors.New("supervised operation required")
	}
	req.Command = Command{Program: "enrolled-worker-exchange"}
	return run(req, operation)
}
func run(req Request, operation func(context.Context) error) (Result, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return Result{}, fmt.Errorf("session is required")
	}
	if strings.TrimSpace(req.Command.Program) == "" {
		return Result{}, fmt.Errorf("agent program is required")
	}
	info, err := os.Stat(req.Worktree)
	if err != nil || !info.IsDir() {
		return Result{}, fmt.Errorf("worktree is not a directory: %s", req.Worktree)
	}
	if req.Timeout <= 0 {
		req.Timeout = 30 * time.Minute
	}
	if req.HeartbeatInterval <= 0 {
		req.HeartbeatInterval = 30 * time.Second
	}
	if err := coordination.VerifyExecution(req.Primary, req.SessionID, req.Worktree); err != nil {
		return Result{}, fmt.Errorf("execution ownership check: %w", err)
	}
	if req.Guard != nil {
		if err := req.Guard(); err != nil {
			return Result{}, err
		}
	}
	parent := req.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, req.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, req.Command.Program, req.Command.Args...)
	configureProcess(cmd)
	cmd.Dir = req.Worktree
	cmd.Stdin = strings.NewReader(req.Input)
	env := req.Environment
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(env, "ORCHESTRATOR_SESSION_ID="+req.SessionID, "ORCHESTRATOR_WORKTREE="+req.Worktree)
	var buffer boundedOutput
	cmd.Stdout = &buffer
	cmd.Stderr = &buffer
	var runErr error
	done := make(chan struct{})
	go func() {
		if operation != nil {
			runErr = operation(ctx)
		} else {
			runErr = cmd.Run()
			cleanupProcess(cmd)
		}
		close(done)
	}()
	ticker := time.NewTicker(req.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			goto finished
		case <-ticker.C:
			if req.Guard != nil {
				if err := req.Guard(); err != nil {
					cancel()
					<-done
					return Result{ExitCode: -1, Output: buffer.String()}, fmt.Errorf("assignment guard failed: %w", err)
				}
			}
			if err := coordination.VerifyExecution(req.Primary, req.SessionID, req.Worktree); err != nil {
				cancel()
				<-done
				return Result{ExitCode: -1, Output: buffer.String()}, fmt.Errorf("execution ownership lost: %w", err)
			}
			if err := coordination.Heartbeat(req.Primary, req.SessionID); err != nil {
				cancel()
				<-done
				return Result{ExitCode: -1, Output: buffer.String()}, fmt.Errorf("execution heartbeat failed: %w", err)
			}
		}
	}
finished:
	// An attached operation can race its deadline or return nil after observing
	// cancellation. Its return value cannot turn a cancelled execution into PASS.
	runErr = errors.Join(runErr, ctx.Err())
	if req.Guard != nil {
		if err := req.Guard(); err != nil {
			return Result{ExitCode: -1, Output: buffer.String()}, fmt.Errorf("stale result rejected: %w", err)
		}
	}
	if err := coordination.VerifyExecution(req.Primary, req.SessionID, req.Worktree); err != nil {
		return Result{ExitCode: -1, Output: buffer.String()}, fmt.Errorf("execution result rejected after ownership loss: %w", err)
	}
	output := buffer.String()
	exitCode := 0
	if runErr != nil {
		exitCode = -1
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		}
	}
	if req.AfterExit != nil {
		if err := req.AfterExit(Result{ExitCode: exitCode, Output: output}); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("after-exit cleanup/check: %w", err))
			if exitCode == 0 {
				exitCode = -1
			}
		}
		if req.Guard != nil {
			if err := req.Guard(); err != nil {
				return Result{ExitCode: exitCode, Output: output}, errors.Join(runErr, fmt.Errorf("stale result rejected after cleanup: %w", err))
			}
		}
		if err := coordination.VerifyExecution(req.Primary, req.SessionID, req.Worktree); err != nil {
			return Result{ExitCode: exitCode, Output: output}, errors.Join(runErr, fmt.Errorf("execution ownership rejected after cleanup: %w", err))
		}
	}
	if runErr != nil {
		if err := coordination.Transition(req.Primary, req.SessionID, "BLOCKED"); err != nil {
			return Result{ExitCode: exitCode, Output: output}, errors.Join(runErr, fmt.Errorf("agent execution failed and blocking session failed: %w", err))
		}
		if err := appendExecution(req, exitCode, output); err != nil {
			return Result{ExitCode: exitCode, Output: output}, errors.Join(runErr, err)
		}
		if ctx.Err() == context.DeadlineExceeded {
			return Result{ExitCode: exitCode, Output: output}, fmt.Errorf("agent execution timed out: %w", runErr)
		}
		return Result{ExitCode: exitCode, Output: output}, fmt.Errorf("agent execution failed: %w", runErr)
	}
	if err := coordination.Heartbeat(req.Primary, req.SessionID); err != nil {
		return Result{}, err
	}
	if err := appendExecution(req, exitCode, output); err != nil {
		return Result{}, err
	}
	return Result{ExitCode: exitCode, Output: output}, nil
}

type boundedOutput struct {
	bytes.Buffer
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (1 << 20) - b.Len()
	if len(p) > remaining {
		b.truncated = true
	}
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
func (b *boundedOutput) String() string {
	result := strings.TrimSpace(b.Buffer.String())
	if b.truncated {
		result += "\n[output truncated at 1 MiB]"
	}
	return result
}
func appendExecution(req Request, exitCode int, output string) error {
	path := filepath.Join(req.Primary, "multi-agent-orchestrator", "resolution.md")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	safe := strings.ReplaceAll(output, "\n", "; ")
	display := strings.Join(append([]string{req.Command.Program}, req.Command.Args...), " ")
	if req.RedactArguments {
		display = req.Command.Program + " [managed assignment arguments omitted]"
	}
	_, err = fmt.Fprintf(f, "\n## Agent execution — %s\n- Session: %s\n- Worktree: %s\n- Command: %s\n- Exit code: %d\n- Output: %s\n- Recorded UTC: %s\n", time.Now().UTC().Format("20060102T150405Z"), req.SessionID, req.Worktree, display, exitCode, safe, time.Now().UTC().Format(time.RFC3339))
	return err
}
