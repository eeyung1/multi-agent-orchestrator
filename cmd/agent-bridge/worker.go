package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
	"github.com/eeyung1/multi-agent-orchestrator/internal/strictjson"
)

// Deliberately a deterministic example fixture, not an AI/provider adapter. Keys
// are repository-relative paths, restricted to Go's exact assignment scope.
type workerFixture struct {
	Files   map[string]string `json:"files"`
	Reject  string            `json:"reject,omitempty"`
	Blocker string            `json:"blocker,omitempty"`
}

func serveWorker(ctx context.Context, c *agentattachment.Client, path string) error {
	for {
		w, err := c.ReceiveWork(ctx)
		if err != nil {
			return err
		}
		f, err := readWorkerFixture(path)
		if err != nil {
			return err
		}
		response := agentprotocol.WorkerMessage{Correlation: w.Correlation}
		yes := f.Reject == ""
		response.Accepted = &yes
		response.Detail = f.Reject
		if err := c.ReportWork(ctx, agentprotocol.WorkerAcceptance, response); err != nil {
			return err
		}
		if !yes {
			continue
		}
		response.Accepted = nil
		response.Detail = "Fixture assignment accepted; performing bounded local writes"
		if err := c.ReportWork(ctx, agentprotocol.WorkerProgress, response); err != nil {
			return err
		}
		if f.Blocker != "" {
			response.Detail = f.Blocker
			if err := c.ReportWork(ctx, agentprotocol.WorkerBlocker, response); err != nil {
				return err
			}
			continue
		}
		if err := writeFixtureFiles(ctx, w, f); err != nil {
			return err
		}
		response.Detail = "Fixture writes finished; all task writers stopped; changes left uncommitted"
		if err := c.ReportWork(ctx, agentprotocol.WorkerResult, response); err != nil {
			return err
		}
	}
}
func readWorkerFixture(path string) (workerFixture, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > agentprotocol.MaxEnvelopeBytes {
		return workerFixture{}, errors.New("invalid worker fixture")
	}
	f, err := os.Open(path)
	if err != nil {
		return workerFixture{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, agentprotocol.MaxEnvelopeBytes+1))
	var w workerFixture
	if err != nil || len(b) > agentprotocol.MaxEnvelopeBytes || strictjson.Decode(b, &w) != nil {
		return workerFixture{}, errors.New("invalid worker fixture")
	}
	return w, nil
}

func writeFixtureFiles(ctx context.Context, w agentprotocol.WorkerMessage, f workerFixture) error {
	paths := make([]string, 0, len(f.Files))
	for p := range f.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		clean, err := state.CanonicalScope(w.Assignment.Worktree, strings.ReplaceAll(p, "{scope}", w.Assignment.Task.Scope))
		scope := w.Assignment.Task.Scope
		if err != nil || clean == "." || (clean != scope && !strings.HasPrefix(clean, scope+"/")) {
			return errors.New("fixture path outside assigned scope")
		}
		full := filepath.Join(w.Assignment.Worktree, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(f.Files[p]), 0644); err != nil {
			return err
		}
	}

	return nil
}
