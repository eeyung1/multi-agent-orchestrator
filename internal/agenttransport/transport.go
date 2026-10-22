// Package agenttransport moves validated agent protocol envelopes. It has no
// admission, registry, scheduling, role-decision or execution authority.
// A transport endpoint is not an enrolled or authenticated agent session.
package agenttransport

import (
	"context"
	"errors"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
)

var (
	ErrClosed             = errors.New("agent transport closed")
	ErrInvalidMessage     = errors.New("invalid agent transport message")
	ErrUnsupportedMessage = errors.New("unsupported agent transport protocol version or message type")
	ErrFrameTooLarge      = errors.New("agent transport frame exceeds limit")
	ErrTruncatedFrame     = errors.New("agent transport frame ended before newline")
	ErrIO                 = errors.New("agent transport I/O failed")
)

// Transport provides FIFO, at-most-once delivery per direction of one connection,
// without acknowledgments or retries. Send success is not authoritative acceptance
// or a guarantee that the peer handled the message. Concurrent sends/receives are
// serialized separately; their ordering is the order of admission, not goroutine
// start order. Callers must not mutate an envelope while Send is running.
//
// Close is concurrent-safe, idempotent and abortive: it wakes blocked operations
// and discards unread data. Later operations return ErrClosed. Contexts must be
// non-nil. Cancellation while waiting for admission changes no connection state;
// cancellation during stream I/O aborts that connection (see NewStream).
type Transport interface {
	Send(context.Context, agentprotocol.Envelope) error
	Receive(context.Context) (agentprotocol.Envelope, error)
	Close() error
}

func peer(origin agentprotocol.Origin) (agentprotocol.Origin, error) {
	switch origin {
	case agentprotocol.FromAgent:
		return agentprotocol.FromOrchestrator, nil
	case agentprotocol.FromOrchestrator:
		return agentprotocol.FromAgent, nil
	default:
		return "", errors.New("transport requires an explicit local protocol origin")
	}
}

// Discard detailed decoder errors: unknown field names and I/O errors may contain
// sensitive input. Error categories never include payloads or assignment tokens.
func messageError(err error) error {
	if errors.Is(err, agentprotocol.ErrUnsupportedVersion) || errors.Is(err, agentprotocol.ErrUnsupportedMessageType) {
		return errors.Join(ErrInvalidMessage, ErrUnsupportedMessage)
	}
	return ErrInvalidMessage
}

func encode(e agentprotocol.Envelope, origin agentprotocol.Origin) ([]byte, error) {
	if err := e.ValidateFrom(origin); err != nil {
		return nil, messageError(err)
	}
	b, err := agentprotocol.Encode(e)
	if err != nil {
		return nil, messageError(err)
	}
	return b, nil
}

func decode(b []byte, origin agentprotocol.Origin) (agentprotocol.Envelope, error) {
	if len(b) > agentprotocol.MaxEnvelopeBytes {
		return agentprotocol.Envelope{}, ErrFrameTooLarge
	}
	e, err := agentprotocol.Decode(b, origin)
	if err != nil {
		return agentprotocol.Envelope{}, messageError(err)
	}
	return e, nil
}
