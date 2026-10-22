package agenttransport

import (
	"context"
	"errors"
	"sync"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
)

// MaxMemoryCapacity bounds queued frames, each of which is also size-bounded.
const MaxMemoryCapacity = 64

type memoryLink struct {
	mu       sync.Mutex
	closed   bool
	changed  chan struct{}
	queues   [2][][]byte
	capacity int
}

type memoryEndpoint struct {
	link     *memoryLink
	side     int
	outbound agentprotocol.Origin
	inbound  agentprotocol.Origin
}

// NewMemoryPair returns agent and ORCHESTRATOR endpoints, in that order.
// Capacity is per direction, must be 1..MaxMemoryCapacity, and is never unbounded.
// No goroutines are started. Both sides share one abortive close boundary.
// Messages are serialized snapshots; receiver mutations cannot affect senders.
func NewMemoryPair(capacity int) (Transport, Transport, error) {
	if capacity < 1 || capacity > MaxMemoryCapacity {
		return nil, nil, errors.New("memory transport capacity must be between 1 and 64")
	}
	l := &memoryLink{capacity: capacity, changed: make(chan struct{})}
	return &memoryEndpoint{l, 0, agentprotocol.FromAgent, agentprotocol.FromOrchestrator},
		&memoryEndpoint{l, 1, agentprotocol.FromOrchestrator, agentprotocol.FromAgent}, nil
}

func (l *memoryLink) notify() {
	close(l.changed)
	l.changed = make(chan struct{})
}

func (m *memoryEndpoint) Send(ctx context.Context, e agentprotocol.Envelope) error {
	l := m.link
	// Check terminal state before validation, including for malformed messages.
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := encode(e, m.outbound)
	if err != nil {
		return err
	}
	for {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return ErrClosed
		}
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return err
		}
		if len(l.queues[m.side]) < l.capacity {
			l.queues[m.side] = append(l.queues[m.side], b)
			l.notify()
			l.mu.Unlock()
			return nil
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (m *memoryEndpoint) Receive(ctx context.Context) (agentprotocol.Envelope, error) {
	l := m.link
	for {
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			return agentprotocol.Envelope{}, ErrClosed
		}
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return agentprotocol.Envelope{}, err
		}
		queue := &l.queues[1-m.side]
		if len(*queue) > 0 {
			b := (*queue)[0]
			(*queue)[0] = nil
			*queue = (*queue)[1:]
			l.notify()
			l.mu.Unlock()
			return decode(b, m.inbound)
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return agentprotocol.Envelope{}, ctx.Err()
		case <-changed:
		}
	}
}

func (m *memoryEndpoint) Close() error {
	l := m.link
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		l.queues = [2][][]byte{}
		l.notify()
	}
	return nil
}
