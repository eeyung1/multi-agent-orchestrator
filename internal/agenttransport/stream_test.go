package agenttransport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eeyung1/multi-agent-orchestrator/internal/agentprotocol"
)

// Two crossed pipes exercise duplex framing without a socket, listener, process,
// or network. Closing either side interrupts both of its pending I/O operations.
type pipeStream struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p *pipeStream) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeStream) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeStream) Close() error                { return errors.Join(p.r.Close(), p.w.Close()) }

func pipes(t *testing.T) (*pipeStream, *pipeStream) {
	t.Helper()
	ar, ow := io.Pipe()
	or, aw := io.Pipe()
	a, o := &pipeStream{ar, aw}, &pipeStream{or, ow}
	t.Cleanup(func() { _ = a.Close(); _ = o.Close() })
	return a, o
}

func streamPair(t *testing.T) (Transport, Transport) {
	t.Helper()
	ap, op := pipes(t)
	a, err := NewStream(ap, agentprotocol.FromAgent)
	if err != nil {
		t.Fatal(err)
	}
	o, err := NewStream(op, agentprotocol.FromOrchestrator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close(); _ = o.Close() })
	return a, o
}

type inputStream struct{ io.Reader }

func (*inputStream) Write(b []byte) (int, error) { return len(b), nil }
func (*inputStream) Close() error                { return nil }

func receiver(t *testing.T, wire string) Transport {
	t.Helper()
	s, err := NewStream(&inputStream{strings.NewReader(wire)}, agentprotocol.FromOrchestrator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func wire(t *testing.T, e agentprotocol.Envelope) string {
	t.Helper()
	b, err := agentprotocol.Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func TestStreamBidirectionalFramesPreserveAssignmentAndProvider(t *testing.T) {
	a, o := streamPair(t)
	ctx := context.Background()
	done := make(chan error, 1)
	go func() { done <- a.Send(ctx, advertisement()) }()
	got, err := o.Receive(ctx)
	if err != nil || !reflect.DeepEqual(got, advertisement()) {
		t.Fatalf("advertisement changed: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	go func() { done <- o.Send(ctx, assignment()) }()
	got, err = a.Receive(ctx)
	if err != nil || !reflect.DeepEqual(got, assignment()) {
		t.Fatalf("assignment changed: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStreamMultipleOrderedFrames(t *testing.T) {
	a, o := streamPair(t)
	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 10; i++ {
			e := heartbeat()
			e.AgentID = agentprotocol.AgentID(fmt.Sprint(i))
			if err := a.Send(ctx, e); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 10; i++ {
		got, err := o.Receive(ctx)
		if err != nil || got.AgentID != agentprotocol.AgentID(fmt.Sprint(i)) {
			t.Fatalf("FIFO %d: %v", i, err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStreamStrictFramingAndErrorRedaction(t *testing.T) {
	valid := wire(t, heartbeat())
	for _, tc := range []struct {
		name, data string
		want       error
	}{
		{"malformed", "{\n", ErrInvalidMessage},
		{"empty", "\n", ErrInvalidMessage},
		{"unknown field", strings.Replace(valid, `"type":`, `"private-claim-fixture":true,"type":`, 1), ErrInvalidMessage},
		{"duplicate", strings.Replace(valid, `"type":`, `"type":"disconnect","TYPE":`, 1), ErrInvalidMessage},
		{"invalid UTF8", string([]byte{0xff, '\n'}), ErrInvalidMessage},
		{"trailing JSON", strings.TrimSuffix(valid, "\n") + " {}\n", ErrInvalidMessage},
		{"unsupported version", strings.Replace(valid, "orchestrator-agent/v1", "future/v99", 1), ErrUnsupportedMessage},
		{"unsupported type", strings.Replace(valid, `"heartbeat"`, `"self-authorize"`, 1), ErrUnsupportedMessage},
		{"registry incarnation", strings.Replace(valid, `"type":`, `"session_generation":99,"type":`, 1), ErrInvalidMessage},
		{"wrong direction", wire(t, assignment()), ErrInvalidMessage},
		{"oversized newline", strings.Repeat("x", agentprotocol.MaxEnvelopeBytes+1) + "\n", ErrFrameTooLarge},
		{"oversized no newline", strings.Repeat("x", agentprotocol.MaxEnvelopeBytes+1), ErrFrameTooLarge},
		{"clean EOF", "", io.EOF},
		{"partial JSON EOF", "{", ErrTruncatedFrame},
		{"complete JSON without LF", strings.TrimSuffix(valid, "\n"), ErrTruncatedFrame},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := receiver(t, tc.data)
			e, err := s.Receive(context.Background())
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(e, agentprotocol.Envelope{}) {
				t.Fatalf("expected %v and no message, got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), "private-claim-fixture") {
				t.Fatal("decoder error leaked input")
			}
			if _, err := s.Receive(context.Background()); !errors.Is(err, ErrClosed) {
				t.Fatal("bad frame/EOF did not close transport")
			}
		})
	}
}

func TestStreamFrameSizeBoundary(t *testing.T) {
	valid := strings.TrimSuffix(wire(t, heartbeat()), "\n")
	// Padding remains JSON whitespace but counts against the frame byte limit.
	for _, extra := range []int{0, 1} {
		s := receiver(t, valid+strings.Repeat(" ", agentprotocol.MaxEnvelopeBytes-len(valid)+extra)+"\n")
		_, err := s.Receive(context.Background())
		if extra == 0 && err != nil {
			t.Fatalf("exact limit rejected: %v", err)
		}
		if extra == 1 && !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("limit+1 accepted: %v", err)
		}
	}
}

func TestStreamEOFOnlyAfterCompleteFrames(t *testing.T) {
	s := receiver(t, wire(t, heartbeat())+wire(t, advertisement()))
	for _, want := range []agentprotocol.Envelope{heartbeat(), advertisement()} {
		got, err := s.Receive(context.Background())
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("buffered frame lost: %v", err)
		}
	}
	if _, err := s.Receive(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("EOF: %v", err)
	}
}

type observedStream struct {
	io.ReadWriteCloser
	reading, writing    chan struct{}
	readOnce, writeOnce sync.Once
}

func (s *observedStream) Read(b []byte) (int, error) {
	s.readOnce.Do(func() { close(s.reading) })
	return s.ReadWriteCloser.Read(b)
}
func (s *observedStream) Write(b []byte) (int, error) {
	s.writeOnce.Do(func() { close(s.writing) })
	return s.ReadWriteCloser.Write(b)
}

func observed(t *testing.T) (Transport, *observedStream) {
	t.Helper()
	ap, _ := pipes(t)
	p := &observedStream{ReadWriteCloser: ap, reading: make(chan struct{}), writing: make(chan struct{})}
	s, err := NewStream(p, agentprotocol.FromAgent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, p
}

func TestStreamBlockedIOWakesOnCancellationAndClose(t *testing.T) {
	for _, send := range []bool{false, true} {
		for _, closeLocal := range []bool{false, true} {
			t.Run(fmt.Sprintf("send=%t/close=%t", send, closeLocal), func(t *testing.T) {
				s, p := observed(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() {
					if send {
						done <- s.Send(ctx, heartbeat())
					} else {
						_, err := s.Receive(ctx)
						done <- err
					}
				}()
				if send {
					<-p.writing
				} else {
					<-p.reading
				}
				want := error(context.Canceled)
				if closeLocal {
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					want = ErrClosed
				} else {
					cancel()
				}
				if err := <-done; !errors.Is(err, want) {
					t.Fatalf("expected %v, got %v", want, err)
				}
				if err := s.Send(context.Background(), heartbeat()); !errors.Is(err, ErrClosed) {
					t.Fatal(err)
				}
				if _, err := s.Receive(context.Background()); !errors.Is(err, ErrClosed) {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestStreamQueuedCancellationDoesNotAbortActiveIO(t *testing.T) {
	for _, send := range []bool{false, true} {
		t.Run(fmt.Sprintf("send=%t", send), func(t *testing.T) {
			s, p := observed(t)
			active := make(chan error, 1)
			go func() {
				if send {
					active <- s.Send(context.Background(), heartbeat())
				} else {
					_, err := s.Receive(context.Background())
					active <- err
				}
			}()
			if send {
				<-p.writing
			} else {
				<-p.reading
			}
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &waitingContext{Context: parent, waiting: make(chan struct{})}
			done := make(chan error, 1)
			go func() {
				if send {
					done <- s.Send(ctx, heartbeat())
				} else {
					_, err := s.Receive(ctx)
					done <- err
				}
			}()
			<-ctx.waiting
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			select {
			case <-s.(*streamTransport).done:
				t.Fatal("queued cancellation aborted active I/O")
			default:
			}
			_ = s.Close()
			if err := <-active; !errors.Is(err, ErrClosed) {
				t.Fatal(err)
			}
		})
	}
}

func TestStreamConcurrentWritesAreFrameSafe(t *testing.T) {
	a, o := streamPair(t)
	const count = 32
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		go func(i int) {
			e := heartbeat()
			e.AgentID = agentprotocol.AgentID(fmt.Sprint(i))
			errs <- a.Send(context.Background(), e)
		}(i)
	}
	seen := map[agentprotocol.AgentID]bool{}
	for i := 0; i < count; i++ {
		got, err := o.Receive(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if seen[got.AgentID] {
			t.Fatal("duplicated frame")
		}
		seen[got.AgentID] = true
	}
	for i := 0; i < count; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestStreamInvalidOutgoingMessageDoesNotWriteOrClose(t *testing.T) {
	a, o := streamPair(t)
	if err := a.Send(context.Background(), assignment()); !errors.Is(err, ErrInvalidMessage) {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Send(context.Background(), heartbeat()) }()
	if _, err := o.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type faultStream struct {
	read  bool
	short bool
}

func (s *faultStream) Read([]byte) (int, error) { return 0, errors.New("private-claim-fixture") }
func (s *faultStream) Write(b []byte) (int, error) {
	if s.short {
		return len(b) - 1, nil
	}
	return 0, errors.New("private-claim-fixture")
}
func (s *faultStream) Close() error { return errors.New("private-claim-fixture") }

func TestStreamIOErrorsAreTerminalAndRedacted(t *testing.T) {
	for _, tc := range []faultStream{{read: true}, {}, {short: true}} {
		s, err := NewStream(&tc, agentprotocol.FromAgent)
		if err != nil {
			t.Fatal(err)
		}
		if tc.read {
			_, err = s.Receive(context.Background())
		} else {
			err = s.Send(context.Background(), heartbeat())
		}
		if !errors.Is(err, ErrIO) || strings.Contains(err.Error(), "private-claim-fixture") {
			t.Fatalf("I/O error: %v", err)
		}
		if err := s.Close(); !errors.Is(err, ErrIO) || strings.Contains(err.Error(), "private-claim-fixture") {
			t.Fatal(err)
		}
		if _, err := s.Receive(context.Background()); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	}
}

func TestStreamConstructionAndExpiredDeadline(t *testing.T) {
	if _, err := NewStream(nil, agentprotocol.FromAgent); err == nil {
		t.Fatal("nil stream accepted")
	}
	if _, err := NewStream(&inputStream{bytes.NewReader(nil)}, "unknown"); err == nil {
		t.Fatal("unknown origin accepted")
	}
	s := receiver(t, wire(t, heartbeat()))
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()
	if _, err := s.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, err := s.Receive(context.Background()); err != nil {
		t.Fatal("pre-expired deadline consumed/closed stream")
	}
}
