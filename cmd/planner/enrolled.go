package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/operations"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planner"
	"github.com/eeyung1/multi-agent-orchestrator/internal/strictjson"
)

// This run hosts its own private attachment listener. There is no administrative
// socket or agent-side selection command. Only this human console grants roles.
func selectEnrolled(ctx context.Context, in *bufio.Reader, out io.Writer, workspace, path string, stale, timeout time.Duration) (selected *planner.Enrolled, release func() error, result error) {
	r, err := agentregistry.Open(workspace, agentregistry.Policy{StaleAfter: stale}, nil)
	if err != nil {
		return nil, nil, err
	}
	config := agentattachment.DefaultConfig()
	if stale < config.IdleTimeout {
		config.IdleTimeout = stale
	}
	s, err := agentattachment.New(r, config)
	if err != nil {
		return nil, nil, err
	}
	l, err := agentattachment.ListenLocal(s, path)
	if err != nil {
		s.Close()
		return nil, nil, err
	}
	serve, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- l.Serve(serve) }()
	cleanup := func() error {
		cancel()
		closeErr := l.Close()
		serveErr := <-done
		return errors.Join(closeErr, serveErr)
	}
	success := false
	defer func() {
		if !success {
			result = errors.Join(result, cleanup())
		}
	}()
	fmt.Fprintln(out, "Enrolled Planner listener ready. Commands: invite <JSON>, invitations, revoke <id>, candidates, select <number>, status [--json], inspect-agent, inspect-task, cancel. Managed fallback: restart without --enrolled-planner-socket.")
	var candidates []agentregistry.View
	for {
		line, err := ask(in, out, "Planner enrollment/selection: ")
		if err != nil {
			return nil, nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if handled, err := operations.Console(ctx, workspace, line, out, operations.Options{Connected: s.Connected}); handled {
			if err != nil {
				fmt.Fprintln(out, "Inspection rejected:", err)
			}
			continue
		}
		command, data, _ := strings.Cut(line, " ")
		switch command {
		case "cancel":
			return nil, nil, errors.New("enrolled Planner selection cancelled; managed adapters remain available")
		case "invite":
			if err := planningInvite(s, data, out); err != nil {
				fmt.Fprintln(out, "Invitation rejected:", err)
			}
		case "invitations":
			list, err := s.Invitations()
			if err != nil {
				return nil, nil, err
			}
			if err := json.NewEncoder(out).Encode(list); err != nil {
				return nil, nil, err
			}
		case "revoke":
			if err := s.Revoke(data); err != nil {
				return nil, nil, err
			}
		case "candidates":
			candidates, err = r.PlannerCandidates()
			if err != nil {
				return nil, nil, err
			}
			connected := candidates[:0]
			for _, v := range candidates {
				if s.Connected(v.Ref()) {
					connected = append(connected, v)
				}
			}
			candidates = connected
			for i, v := range candidates {
				fmt.Fprintf(out, "%d. AgentID=%s provider=%q session=%s incarnation=%d status=%s capabilities=%v\n", i+1, v.Ref().AgentID, v.Session.Identity.Metadata.Provider, v.Ref().SessionID, v.Ref().Generation, v.Session.Status, v.Session.Identity.Capabilities)
			}
			if len(candidates) == 0 {
				fmt.Fprintln(out, "No live READY v3 agents advertise PLANNER and planning. Invite/start a compatible bridge, or cancel and use a managed Planner.")
			}
		case "select":
			if data == "" {
				return nil, nil, errors.New("explicit candidate number required")
			}
			index, err := choiceIndex(data, len(candidates))
			if err != nil {
				return nil, nil, err
			}
			ref := candidates[index].Ref()
			yes, err := confirm(in, out, fmt.Sprintf("Select exact enrolled session %s/%s incarnation %d as Planner? [y/N] ", ref.AgentID, ref.SessionID, ref.Generation))
			if err != nil {
				return nil, nil, err
			}
			if !yes {
				return nil, nil, errors.New("Planner selection was not authorized")
			}
			var nonce [16]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				return nil, nil, err
			}
			binding, err := r.SelectPlanner("planning-"+hex.EncodeToString(nonce[:]), ref, "", true, "interactive human selected exact enrolled Planner session")
			if err != nil {
				return nil, nil, err
			}
			p := &planner.Enrolled{Registry: r, Server: s, Binding: binding, Timeout: timeout}
			fmt.Fprintf(out, "Enrolled Planner selected: %s (run %s). Go ORCHESTRATOR retains authority.\n", ref.AgentID, binding.RunID)
			success = true
			return p, func() error { return errors.Join(p.Close(), cleanup()) }, nil
		default:
			fmt.Fprintln(out, "Use invite, invitations, revoke, candidates, select, or cancel.")
		}
	}
}

func planningInvite(s *agentattachment.Server, data string, out io.Writer) error {
	var q struct {
		AgentID        agentprotocol.AgentID      `json:"agent_id"`
		CredentialFile string                     `json:"credential_file"`
		Replace        bool                       `json:"replace,omitempty"`
		Roles          []agentprotocol.Role       `json:"roles,omitempty"`
		Capabilities   []agentprotocol.Capability `json:"capabilities,omitempty"`
	}
	if strictjson.Decode([]byte(data), &q) != nil || q.CredentialFile == "" {
		return errors.New("provide agent_id, credential_file, and optional replace")
	}
	if q.Roles == nil {
		q.Roles = []agentprotocol.Role{agentprotocol.RolePlanner}
	}
	if q.Capabilities == nil {
		q.Capabilities = []agentprotocol.Capability{agentprotocol.PlanningCapability}
	}
	p, proof, err := s.Invite(agentattachment.InviteRequest{AgentID: q.AgentID, Roles: q.Roles, Capabilities: q.Capabilities, TTL: 5 * time.Minute, Replace: q.Replace}, true)
	if err != nil {
		return err
	}
	if err := agentattachment.WriteCredential(q.CredentialFile, proof); err != nil {
		_ = s.Revoke(p.ID)
		return err
	}
	return json.NewEncoder(out).Encode(p)
}
