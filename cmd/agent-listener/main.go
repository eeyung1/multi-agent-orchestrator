package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentregistry"
	"github.com/eeyung1/multi-agent-orchestrator/internal/strictjson"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "agent listener:", err)
		os.Exit(1)
	}
}

// The console is the trusted human control channel, separate from the agent
// socket. No socket message can create/revoke invitations or assign a role.
func run(parent context.Context, args []string, in io.ReadCloser, out io.Writer) error {
	f := flag.NewFlagSet("agent-listener", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	workspace := f.String("workspace", ".", "explicitly enrolled target workspace")
	path := f.String("socket", "", "absolute private Unix socket path")
	stale := f.Duration("stale-after", 90*time.Second, "registry liveness policy; must match on reopen")
	c := agentattachment.DefaultConfig()
	f.IntVar(&c.MaxConnections, "max-connections", c.MaxConnections, "bounded active local connections")
	f.IntVar(&c.MaxInvitations, "max-invitations", c.MaxInvitations, "bounded pending invitations")
	f.DurationVar(&c.HandshakeTimeout, "handshake-timeout", c.HandshakeTimeout, "maximum admission handshake duration")
	f.DurationVar(&c.IdleTimeout, "idle-timeout", c.IdleTimeout, "maximum silence per bound connection")
	if err := f.Parse(args); err != nil {
		return errors.New("invalid listener arguments")
	}
	if f.NArg() != 0 || *path == "" {
		return errors.New("provide --socket with an absolute path")
	}
	r, err := agentregistry.Open(*workspace, agentregistry.Policy{StaleAfter: *stale}, nil)
	if err != nil {
		return errors.New("cannot open enrolled workspace registry")
	}
	s, err := agentattachment.New(r, c)
	if err != nil {
		return err
	}
	defer s.Close()
	l, err := agentattachment.ListenLocal(s, *path)
	if err != nil {
		return err
	}
	defer l.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- l.Serve(ctx); cancel() }()
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = in.Close(); close(finished) })
	defer func() {
		cancel()
		if !stop() {
			<-finished
		}
	}()
	fmt.Fprintln(out, "Local attachment listener ready. Trusted console commands: invite <JSON>, invitations, revoke <id>, agents, quit.")
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 65536)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "quit" {
			break
		}
		if line == "" {
			continue
		}
		if err := console(s, r, line, out); err != nil {
			fmt.Fprintln(out, "Command rejected:", err)
		}
	}
	cancel()
	err = <-done
	if err != nil {
		return err
	}
	if scanner.Err() != nil && parent.Err() == nil {
		return errors.New("listener console read failed")
	}
	return nil
}

func console(s *agentattachment.Server, r *agentregistry.Registry, line string, out io.Writer) error {
	command, data, _ := strings.Cut(line, " ")
	write := func(v any) error { return json.NewEncoder(out).Encode(v) }
	switch command {
	case "invite":
		var q struct {
			AgentID        string   `json:"agent_id"`
			Roles          []string `json:"roles"`
			Capabilities   []string `json:"capabilities"`
			TTLSeconds     int      `json:"ttl_seconds"`
			Replace        bool     `json:"replace,omitempty"`
			CredentialFile string   `json:"credential_file"`
		}
		if strictjson.Decode([]byte(data), &q) != nil || q.TTLSeconds < 1 || q.TTLSeconds > 300 || q.CredentialFile == "" {
			return errors.New("invalid invitation command")
		}
		request := inviteRequest(q.AgentID, q.Roles, q.Capabilities, time.Duration(q.TTLSeconds)*time.Second, q.Replace)
		p, proof, err := s.Invite(request, true)
		if err != nil {
			return err
		}
		if err := agentattachment.WriteCredential(q.CredentialFile, proof); err != nil {
			_ = s.Revoke(p.ID)
			return err
		}
		return write(p) // Deliberately excludes proof, hash, and credential-file contents.
	case "invitations":
		if data != "" {
			return errors.New("unexpected console arguments")
		}
		p, err := s.Invitations()
		if err != nil {
			return err
		}
		return write(p)
	case "revoke":
		if data == "" || strings.ContainsAny(data, " \t") {
			return errors.New("provide one invitation ID")
		}
		return s.Revoke(data)
	case "agents":
		if data != "" {
			return errors.New("unexpected console arguments")
		}
		v, err := r.List()
		if err != nil {
			return agentattachment.ErrRegistry
		}
		return write(v)
	default:
		return errors.New("unknown console command")
	}
}
