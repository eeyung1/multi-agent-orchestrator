package agenttransport

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sync"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
)

type streamTransport struct {
	stream              io.ReadWriteCloser
	reader              *bufio.Reader
	outbound            agentprotocol.Origin
	inbound             agentprotocol.Origin
	readGate, writeGate chan struct{}
	done                chan struct{}
	once                sync.Once
	closeErr            error
}

// NewStream owns an already-open duplex stream; it neither opens connections nor
// launches processes. The trusted caller chooses localOrigin, never wire input.
// The stream MUST support concurrent Read/Write/Close, and Close MUST promptly
// unblock pending Read/Write. Ordinary arbitrary Readers/Writers cannot promise
// this; future pipe adapters must supply this lifetime contract explicitly.
//
// Framing is one strict Envelope plus LF, at most MaxEnvelopeBytes excluding LF.
// EOF between frames is io.EOF; EOF within a frame is ErrTruncatedFrame, even if
// the JSON was complete. Bad input, I/O failure or EOF closes the whole stream;
// the initiating call gets that error, subsequent calls get ErrClosed.
//
// Cancellation after I/O admission also closes the whole stream and returns the
// context error; bytes may already have moved. Queued cancellation does not close
// it. There is no half-close, resynchronization, retry or background read pump.
func NewStream(stream io.ReadWriteCloser, localOrigin agentprotocol.Origin) (Transport, error) {
	inbound, err := peer(localOrigin)
	if err != nil {
		return nil, err
	}
	if stream == nil {
		return nil, errors.New("transport stream is required")
	}
	return &streamTransport{stream: stream, reader: bufio.NewReaderSize(stream, 4096),
		outbound: localOrigin, inbound: inbound, readGate: make(chan struct{}, 1),
		writeGate: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

func (s *streamTransport) acquire(ctx context.Context, gate chan struct{}) error {
	select {
	case <-s.done:
		return ErrClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-s.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	case gate <- struct{}{}:
	}
	select {
	case <-s.done:
		<-gate
		return ErrClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		<-gate
		return err
	}
	return nil
}

// withIO does not leave a goroutine blocked in Read/Write. Its cancellation
// callback closes the interruptible stream, and is joined before returning.
func (s *streamTransport) withIO(ctx context.Context, fn func() error) error {
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = s.Close(); close(finished) })
	err := fn()
	if !stop() {
		<-finished
	}
	if ctx.Err() != nil {
		// Cancellation can race with stopping the callback after I/O completed.
		// A reported active-I/O cancellation always leaves a terminal connection.
		_ = s.Close()
		return ctx.Err()
	}
	select {
	case <-s.done:
		return ErrClosed
	default:
	}
	return err
}

func (s *streamTransport) Send(ctx context.Context, e agentprotocol.Envelope) error {
	if err := s.acquire(ctx, s.writeGate); err != nil {
		return err
	}
	defer func() { <-s.writeGate }()
	b, err := encode(e, s.outbound)
	if err != nil {
		// No bytes written: connection remains usable.
		return err
	}
	b = append(b, '\n')
	err = s.withIO(ctx, func() error {
		n, err := s.stream.Write(b)
		if err != nil || n != len(b) {
			return ErrIO
		}
		return nil
	})
	if errors.Is(err, ErrIO) {
		_ = s.Close()
	}
	return err
}

func (s *streamTransport) Receive(ctx context.Context) (agentprotocol.Envelope, error) {
	if err := s.acquire(ctx, s.readGate); err != nil {
		return agentprotocol.Envelope{}, err
	}
	defer func() { <-s.readGate }()
	var e agentprotocol.Envelope
	err := s.withIO(ctx, func() error {
		b, err := s.readFrame()
		if err != nil {
			return err
		}
		e, err = decode(b, s.inbound)
		return err
	})
	if err != nil {
		_ = s.Close()
		return agentprotocol.Envelope{}, err
	}
	return e, nil
}

func (s *streamTransport) readFrame() ([]byte, error) {
	var frame []byte
	for {
		part, err := s.reader.ReadSlice('\n')
		limit := agentprotocol.MaxEnvelopeBytes
		if err == nil {
			// Only a terminating LF is outside the limit.
			limit++
		}
		if len(frame)+len(part) > limit {
			return nil, ErrFrameTooLarge
		}
		frame = append(frame, part...)
		if err == nil {
			return frame[:len(frame)-1], nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if len(frame) == 0 {
				return nil, io.EOF
			}
			return nil, ErrTruncatedFrame
		}
		return nil, ErrIO
	}
}

func (s *streamTransport) Close() error {
	s.once.Do(func() {
		close(s.done)
		if s.stream.Close() != nil {
			s.closeErr = ErrIO
		}
	})
	return s.closeErr
}
