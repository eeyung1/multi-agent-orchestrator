package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/controller"
	"github.com/eeyung1/multi-agent-orchestrator/internal/operations"
)

// Minimal trusted Phase 6 console. Role assignment is a separate explicit human
// decision, never a capability advertisement or an agent-side socket command.
func prepareEnrolledWorkers(ctx context.Context, in *bufio.Reader, out io.Writer, workspace, path string, stale time.Duration, c *controller.Config) (func() error, error) {
	cleanup := func() error { return nil }
	if c.Runtime == nil {
		if path == "" {
			return nil, errors.New("enrolled_capabilities requires --enrolled-worker-socket or an active enrolled Planner listener")
		}
		r, err := agentregistry.Open(workspace, agentregistry.Policy{StaleAfter: stale}, nil)
		if err != nil {
			return nil, err
		}
		cfg := agentattachment.DefaultConfig()
		if stale < cfg.IdleTimeout {
			cfg.IdleTimeout = stale
		}
		s, err := agentattachment.New(r, cfg)
		if err != nil {
			return nil, err
		}
		l, err := agentattachment.ListenLocal(s, path)
		if err != nil {
			s.Close()
			return nil, err
		}
		serve, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- l.Serve(serve) }()
		cleanup = func() error { cancel(); return errors.Join(l.Close(), <-done) }
		c.Runtime = &controller.EnrolledWorkers{Registry: r, Server: s}
	}
	fmt.Fprintln(out, "Enrolled worker listener ready. Commands: invite <JSON>, workers, authorize <number>, invitations, revoke <id>, status [--json], inspect-agent, inspect-task, run, cancel. Invitations grant no role.")
	var candidates []agentregistry.View
	for {
		line, err := ask(in, out, "Worker enrollment: ")
		if err != nil {
			_ = cleanup()
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			_ = cleanup()
			return nil, err
		}
		if handled, err := operations.Console(ctx, workspace, line, out, operations.Options{Connected: c.Runtime.Server.Connected, MaxWorkers: &c.MaxWorkers, ConfigurationSource: "owning_host_policy"}); handled {
			if err != nil {
				fmt.Fprintln(out, "Inspection rejected:", err)
			}
			continue
		}
		cmd, data, _ := strings.Cut(line, " ")
		switch cmd {
		case "cancel":
			_ = cleanup()
			return nil, errors.New("worker enrollment cancelled")
		case "run":
			return cleanup, nil
		case "invite":
			if err := planningInvite(c.Runtime.Server, data, out); err != nil {
				fmt.Fprintln(out, "Invitation rejected:", err)
			}
		case "invitations":
			list, err := c.Runtime.Server.Invitations()
			if err != nil {
				_ = cleanup()
				return nil, err
			}
			_ = json.NewEncoder(out).Encode(list)
		case "revoke":
			if err := c.Runtime.Server.Revoke(data); err != nil {
				_ = cleanup()
				return nil, err
			}
		case "workers":
			all, err := c.Runtime.Registry.List()
			if err != nil {
				_ = cleanup()
				return nil, err
			}
			candidates = nil
			for _, v := range all {
				supports := false
				for _, role := range v.Session.Identity.SupportedRoles {
					supports = supports || role == agentprotocol.RoleWorker
				}
				if v.Current && v.Live && v.Session.Status == agentregistry.Ready && supports && v.Session.Identity.ProtocolVersion == agentprotocol.Version4 && c.Runtime.Server.Connected(v.Ref()) {
					candidates = append(candidates, v)
					fmt.Fprintf(out, "%d. agent=%s session=%s incarnation=%d provider=%q capabilities=%v assigned_role=%s\n", len(candidates), v.Ref().AgentID, v.Ref().SessionID, v.Ref().Generation, v.Session.Identity.Metadata.Provider, v.Session.Identity.Capabilities, v.Session.AssignedRole)
				}
			}
		case "authorize":
			if data == "" {
				fmt.Fprintln(out, "Explicit worker number required")
				continue
			}
			i, err := choiceIndex(data, len(candidates))
			if err != nil {
				fmt.Fprintln(out, err)
				continue
			}
			ref := candidates[i].Ref()
			yes, err := confirm(in, out, fmt.Sprintf("Authorize exact session %s/%s incarnation %d as WORKER? [y/N] ", ref.AgentID, ref.SessionID, ref.Generation))
			if err != nil {
				_ = cleanup()
				return nil, err
			}
			if !yes {
				continue
			}
			if !c.Runtime.Server.Connected(ref) {
				fmt.Fprintln(out, "Worker disconnected")
				continue
			}
			if err := c.Runtime.Registry.AssignRole(ref, agentprotocol.RoleWorker, "interactive human authorized exact worker session"); err != nil {
				fmt.Fprintln(out, "Worker authorization rejected:", err)
			}
		default:
			fmt.Fprintln(out, "Use invite, workers, authorize, invitations, revoke, run, or cancel.")
		}
	}
}
