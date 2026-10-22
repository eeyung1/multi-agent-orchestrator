// agent-bridge is a provider-neutral attachment/liveness example. It performs no
// planning or coding and demonstrates an independently started compatible client.
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
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			return c.Disconnect(shutdown)
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
