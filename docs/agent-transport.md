# Agent transport: Phase 3

The [README architecture contract](../README.md#target-architecture--in-development)
remains the product architecture. `internal/agenttransport` implements a message
boundary, not an attachment service or authority handler. Phase 4 now supplies
the separate [local attachment handler](agent-attachment.md). Phase 1 provides
identity/envelopes; Phase 2 provides the authoritative internal registry and
liveness. This phase adds the interface, in-memory transport and generic NDJSON
stream framing. It does not integrate current providers with the registry.

## Transport and adapter responsibilities

```text
provider/runtime <-> provider adapter <-> protocol envelopes
                                            |
                                         transport
                                            |
                              Go admission/session handler (Phase 4)
                                            |
                              registry / scheduler / execution guards
```

Transport answers how messages move. Adapters translate a provider's invocation,
input and output into the protocol. Provider argument construction, process
launch, scheduling, authorization, worktrees and role decisions do not belong
in transport. No provider names or closed provider enum are used here.

Existing Codex/Claude/Gemini execution is unchanged. Planner adapters currently
consume one request and produce a provider-specific proposal. Workers receive
WorkerContext; execution combines stdout and stderr as bounded command output.
Those streams are not protocol NDJSON. A later managed bridge must translate
these contracts deliberately, separate logs from protocol frames, and retain
the existing supervised execution/admission guards. Directly piping current CLI
output into NewStream is not a supported integration.

## Interface and validation

```go
type Transport interface {
    Send(context.Context, agentprotocol.Envelope) error
    Receive(context.Context) (agentprotocol.Envelope, error)
    Close() error
}
```

Contexts must be non-nil. Callers may concurrently Send, Receive and Close;
operations in each direction are serialized independently. Callers must not
mutate an envelope or its nested data while Send is running. Received envelopes
are independent values and contain the same identities/references that were sent.
No PID, file descriptor, provider name or connection address supplies identity.

Send validates message shape and trusted direction before serialization. Receive
strictly decodes bytes with `agentprotocol.Decode`: unsupported versions/types,
unknown/duplicate JSON fields, malformed payloads, mismatched identities and
incorrect directions fail before a typed envelope is returned. Local origin is
chosen by trusted Go construction, never read from the message. Origin is a
direction constraint, not a role assignment or credential.

## In-memory endpoints

`NewMemoryPair(capacity)` returns `(agent, orchestrator, error)`. Each direction
has a bounded queue of 1–64 frames, each bounded by the protocol's 1 MiB limit.
Zero, negative and larger capacities are rejected. Send blocks when that
direction's queue is full; Receive blocks when empty. Context cancellation and
Close wake waiters. There are no background goroutines or unbounded queues.
Serialized snapshots prevent later sender/receiver mutations from crossing the
boundary.

Either endpoint's Close aborts the entire pair, discards queued frames and wakes
all waiters. Repeated Close succeeds; later Send/Receive return ErrClosed.
Memory cancellation does not close the connection; an operation cancelled before
queue admission does not enqueue/dequeue a frame. An operation already admitted
may succeed even when cancellation races with it.

## Generic stream framing

`NewStream(stream, localOrigin)` owns an already-open `io.ReadWriteCloser`.
It opens no connection and launches no process. The stream must support
concurrent Read/Write/Close, and Close must promptly unblock Read/Write. This is
an explicit caller contract; arbitrary Readers/Writers do not provide cancellable
I/O. A future adapter combining separate process pipes must supply that contract
and close all relevant handles. The constructor cannot verify it mechanically.

One frame is one JSON envelope followed by LF. The byte limit is
`agentprotocol.MaxEnvelopeBytes` (1 MiB), excluding the terminating LF.
Optional JSON whitespace, including CR before LF, counts toward the limit. Blank
lines are invalid. A bounded reader rejects oversized frames without reading an
unbounded line. There is at most one active read and one active write, with no
message pump or pending-message queue. Concurrent writes cannot interleave frames.
Underlying I/O supplies backpressure; callers waiting for the direction gate can
cancel without affecting the active operation.

Cancellation after I/O admission closes the entire stream, interrupts both
directions, joins its cancellation callback and returns the context error.
Bytes may already have moved, so the outcome is uncertain to the caller. A
pre-cancelled call or one cancelled while waiting for the direction gate leaves
the connection unchanged. The package cannot promise prompt cancellation if the
supplied stream violates its Close/unblock contract.

Clean EOF between frames returns `io.EOF`. EOF before LF returns
`ErrTruncatedFrame`, even if the JSON text was complete. Malformed input,
oversize, truncation, EOF and I/O failures terminate the connection; the detecting
operation returns that error and subsequent operations return ErrClosed. There
is no half-close, frame resynchronization or retry. Outgoing validation errors
write no bytes and leave the connection usable. Explicit Close is idempotent and
abortive, discards unread input and wakes waiters; a stream close failure is
reported as ErrIO.

## Delivery and errors

Frames are FIFO per direction, ordered by admission; concurrent goroutine start
order is not defined. Each admitted frame is delivered at most once by this
connection. Send success means local queue admission or complete frame write,
not peer handling, persistence, enrollment or authoritative result acceptance.
Close/cancellation can discard unread data. There are no acknowledgments,
automatic retries, durable delivery, cross-direction ordering or exactly-once
distributed guarantees. Retry/idempotency decisions belong above transport,
under Go state and generation fences.

Use `errors.Is` to distinguish ErrClosed, ErrInvalidMessage,
ErrUnsupportedMessage (also ErrInvalidMessage), ErrFrameTooLarge,
ErrTruncatedFrame, ErrIO, context cancellation/deadline, and clean io.EOF.
Transport errors do not include payloads, claim tokens, arbitrary decoder field
names or underlying I/O error text. Simultaneous close/cancellation/failure may
determine which terminal error an in-flight operation observes; later operations
consistently return ErrClosed. Close returning an error still leaves it terminal.

## Admission and session binding outside transport

Transport is not authentication. A process capable of writing bytes is not
automatically enrolled. AgentID and SessionID are identifiers, not credentials.
Even a well-formed role/assignment/enrollment response is only data at this layer.
Transport never calls registry mutation methods, issues an incarnation, mints a
claim or scheduler generation, assigns roles, or admits execution.

The Phase 4 Go admission handler authorizes local participants using one-use
invitations and privately binds the transport to `agentregistry.Ref` (AgentID,
SessionID, Go-issued session generation). There is no transport self-bind API or
duplicate binding/ownership identity type here. V2 enrollment responses and
lifecycle acknowledgments report registry incarnation, but requests do not select
it. The handler retains its trusted binding and rechecks current registry state.
V4 worker messages also need their durable scheduler dispatch correlation and
existing private ownership checks. Operational claim tokens stay inside Go.
Transport itself deliberately permits different syntactically valid session
identities; syntax/direction validation alone cannot establish session ownership.

Phase 4 implements independently started compatible local clients over Unix IPC,
with bounded admission and session handling outside this package. Public/remote
listeners, HTTP/RPC/stdio provider bridges and same-session reconnect remain
unimplemented. Phase 6 adds enrolled-worker routing and scheduler handling outside
the transport package. Phase 5 adds enrolled Planner request/proposal
routing in the attachment handler and authoritative selection in the registry;
transport itself still has no role or request-adoption authority. Transport alone
still does not update registry liveness; Phase 4's handler applies bound messages.
Human authorization, review and final
merge/deployment authority remain intact. Local orchestration is not an
OS/container sandbox; local filesystem locks are not multi-host consensus.
