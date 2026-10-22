//go:build !unix

package agentattachment

import (
	"context"
	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
	"io"
	"net"
)

func listenLocal(string) (net.Listener, func() error, error)        { return nil, nil, ErrSocket }
func dialLocal(context.Context, string) (io.ReadWriteCloser, error) { return nil, ErrSocket }
func WriteCredential(string, agentprotocol.InvitationProof) error   { return ErrSocket }
func ReadCredential(string) (agentprotocol.InvitationProof, error) {
	return agentprotocol.InvitationProof{}, ErrSocket
}
