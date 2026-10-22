package agentattachment

import (
	"context"
	"errors"
	"net"
	"sync"
)

var ErrSocket = errors.New("unsafe, unavailable or unsupported local attachment socket")

// LocalListener opens only Unix-domain IPC, never a TCP/public listener.
type LocalListener struct {
	server      *Server
	listener    net.Listener
	cleanup     func() error
	mu          sync.Mutex
	connections map[net.Conn]bool
	closed      bool
	serving     bool
	wg          sync.WaitGroup
	err         error
}

func ListenLocal(s *Server, path string) (*LocalListener, error) {
	if s == nil {
		return nil, ErrSocket
	}
	l, cleanup, err := listenLocal(path)
	if err != nil {
		return nil, err
	}
	return &LocalListener{server: s, listener: l, cleanup: cleanup, connections: map[net.Conn]bool{}}, nil
}

// Serve handles each admitted connection independently, up to MaxConnections.
// Excess accepted connections are immediately closed without starting a goroutine.
// It joins all handlers and durable disconnect attempts before returning.
func (l *LocalListener) Serve(ctx context.Context) error {
	l.mu.Lock()
	if l.serving {
		l.mu.Unlock()
		return ErrClosed
	}
	l.serving = true
	l.mu.Unlock()
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = l.Close(); close(finished) })
	defer func() {
		if !stop() {
			<-finished
		}
	}()
	defer l.Close()
	for {
		conn, err := l.listener.Accept()
		if err != nil {
			l.mu.Lock()
			closed := l.closed
			l.mu.Unlock()
			if !closed {
				l.mu.Lock()
				l.err = ErrSocket
				l.mu.Unlock()
				_ = l.Close()
			}
			break
		}
		l.mu.Lock()
		if l.closed || len(l.connections) >= l.server.c.MaxConnections {
			l.mu.Unlock()
			_ = conn.Close()
			continue
		}
		l.connections[conn] = true
		l.wg.Add(1)
		l.mu.Unlock()
		go func() {
			defer l.wg.Done()
			defer func() { _ = conn.Close(); l.mu.Lock(); delete(l.connections, conn); l.mu.Unlock() }()
			if err := l.server.handleStream(ctx, conn); errors.Is(err, ErrRegistry) {
				l.mu.Lock()
				l.err = ErrRegistry
				l.mu.Unlock()
				_ = l.Close()
			}
		}()
	}
	l.wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (l *LocalListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		_ = l.listener.Close()
		for c := range l.connections {
			_ = c.Close()
		}
		if l.cleanup() != nil {
			l.err = ErrSocket
		}
		l.server.Close()
	}
	return l.err
}
