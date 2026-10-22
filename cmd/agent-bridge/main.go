// agent-bridge is a provider-neutral attachment/liveness example. It performs no
// coding. Optional proposal-file mode demonstrates structured Planner exchange.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentattachment"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"github.com/eeyung1/multi-agent-orchestrator/internal/planning"
	"github.com/eeyung1/multi-agent-orchestrator/internal/strictjson"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "agent bridge:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("agent-bridge", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	path := f.String("socket", "", "private local Unix socket")
	credential := f.String("credential-file", "", "private one-time invitation file; contents never logged")
	agent := f.String("agent-id", "", "authorized logical agent ID")
	provider := f.String("provider", "compatible-example", "informational provider/runtime metadata")
	capability := f.String("capability", "example/heartbeat", "advertised capability; must be invited")
	role := f.String("role", "WORKER", "advertised supported role; grants no assigned role")
	proposalFile := f.String("proposal-file", "", "optional structured proposal JSON fixture; enables v3 PLANNER/planning example, not an AI implementation")
	interval := f.Duration("heartbeat-interval", 5*time.Second, "heartbeat interval shorter than listener idle timeout")
	if err := f.Parse(args); err != nil {
		return errors.New("invalid bridge arguments")
	}
	if f.NArg() != 0 || *interval <= 0 {
		return errors.New("invalid bridge configuration")
	}
	proof, err := agentattachment.ReadCredential(*credential)
	if err != nil {
		return err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return errors.New("session identity generation failed")
	}
	id := agentprotocol.Identity{ProtocolVersion: agentprotocol.Version2, AgentID: agentprotocol.AgentID(*agent), SessionID: agentprotocol.SessionID("local-" + hex.EncodeToString(b)),
		Metadata: agentprotocol.Metadata{Provider: *provider, Runtime: "compatible-local-bridge"}, Capabilities: []agentprotocol.Capability{agentprotocol.Capability(*capability)}, SupportedRoles: []agentprotocol.Role{agentprotocol.Role(*role)}}
	if *proposalFile != "" {
		id.ProtocolVersion = agentprotocol.Version3
		id.SupportedRoles = []agentprotocol.Role{agentprotocol.RolePlanner}
		id.Capabilities = []agentprotocol.Capability{agentprotocol.PlanningCapability}
	}
	handshake, cancel := context.WithTimeout(ctx, 10*time.Second)
	c, err := agentattachment.DialLocal(handshake, *path, id, proof)
	cancel()
	if err != nil {
		return err
	}
	defer c.Close()
	if err := json.NewEncoder(out).Encode(c.Binding()); err != nil {
		return err
	}
	planningErrors := make(chan error, 1)
	if *proposalFile != "" {
		planningCtx, stopPlanning := context.WithCancel(ctx)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			for {
				request, err := c.ReceivePlanning(planningCtx)
				if err != nil {
					planningErrors <- err
					return
				}
				proposal, err := readProposal(*proposalFile)
				if err == nil {
					err = c.RespondPlanning(planningCtx, request, proposal)
				}
				if err != nil {
					planningErrors <- err
					return
				}
			}
		}()
		defer func() { stopPlanning(); _ = c.Close(); <-finished }()
	}
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	shutdown := func() error {
		deadline, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		return c.Disconnect(deadline)
	}
	for {
		select {
		case err := <-planningErrors:
			if ctx.Err() != nil {
				return shutdown()
			}
			return err
		case <-ctx.Done():
			return shutdown()
		case <-ticker.C:
			call, cancel := context.WithTimeout(ctx, *interval)
			err := c.Heartbeat(call)
			cancel()
			if err != nil {
				return err
			}
			fmt.Fprintln(out, "Heartbeat acknowledged")
		}
	}
}

// Fixture only: real bridges translate the typed request into their runtime and
// return a structured proposal. No shell command or provider is selected here.
func readProposal(path string) (planning.Proposal, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > agentprotocol.MaxEnvelopeBytes {
		return planning.Proposal{}, errors.New("invalid proposal fixture file")
	}
	f, err := os.Open(path)
	if err != nil {
		return planning.Proposal{}, errors.New("cannot read proposal fixture")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, agentprotocol.MaxEnvelopeBytes+1))
	var p planning.Proposal
	if err != nil || len(b) > agentprotocol.MaxEnvelopeBytes || strictjson.Decode(b, &p) != nil || p.Validate() != nil {
		return planning.Proposal{}, errors.New("invalid proposal fixture")
	}
	return p, nil
}
