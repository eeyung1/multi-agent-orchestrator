# Local agent attachment: Phases 4–6

This is the local admission/liveness foundation for independently started
compatible agents or bridges. Phase 5 adds human-selected enrolled Planner
execution through a run-hosted listener; see [enrolled Planner operation](enrolled-planner.md).
Phase 6 adds opt-in enrolled-worker dispatch through the run-hosted listener; see
[enrolled worker operation](enrolled-workers.md). Managed defaults remain available.
"Any compatible agent" means a runtime with a bridge/client implementing the
universal protocol; a closed product without an integration surface cannot attach
automatically. The included bridge is a provider-neutral liveness example, not an
AI model or coding adapter. Its optional `--proposal-file` mode exercises the
Planner protocol with a supplied structured fixture; it does not perform reasoning.
`--worker-file` demonstrates bounded V4 worker file writes, acceptance, progress
and terminal reports. It is a simulation fixture, not an AI coding implementation.

## Try two independent terminals

Use the upgraded Go binaries, an already explicitly enrolled test workspace, and
a new private directory. Existing repository authority enrollment is described in
[setup.md](../setup.md). Do not edit coordination state to enroll a repository.
Unix-domain sockets are supported on Unix systems; other platforms report an
unsupported/unsafe socket error. Paths must be absolute and canonical, without
symlink components (on macOS use canonical `/private/tmp` rather than `/tmp`).

In terminal A, from the orchestrator source checkout:

```bash
mkdir -m 700 /tmp/mao-agents-example
go run ./cmd/agent-listener --workspace ~/Projects/my-project --socket /tmp/mao-agents-example/agents.sock
```

Choose a new directory if that one exists; do not reuse someone else's directory.
The listener starts a trusted stdin console. Enter this explicit authorization:

```text
invite {"agent_id":"demo-local","roles":["WORKER"],"capabilities":["example/heartbeat"],"ttl_seconds":120,"credential_file":"/tmp/mao-agents-example/invitation.json"}
```

The console prints invitation metadata, never its secret. It creates the
credential file with mode 0600 inside the private directory and refuses overwrite.
That file is sensitive: do not commit it, copy it into project ledgers, put its
contents in command arguments/environment/logs, or share it with untrusted code.
Do not place the listener or credential directory inside a worker's scope.

In terminal B, independently start the compatible example client:

```bash
go run ./cmd/agent-bridge --socket /tmp/mao-agents-example/agents.sock --credential-file /tmp/mao-agents-example/invitation.json --agent-id demo-local --provider example-runtime
```

The bridge generates a fresh runtime SessionID, presents the invitation, receives
Go's binding and sends heartbeats every five seconds. It prints the binding
identifiers and heartbeat acknowledgments; no secret is printed. The ORCHESTRATOR
did not launch this process. Ctrl-C requests a bounded graceful disconnect.
The credential is consumed and cannot start another session; remove the inert
credential file when finished.

Terminal A console commands:

| Command | Behavior |
| --- | --- |
| `invitations` | Lists pending unexpired metadata, with no secret/hash. |
| `revoke <id>` | Revokes a pending invitation; repeat is safe. Does not terminate an enrolled session. |
| `agents` | Lists authoritative registry records, incarnation/current/live/status and advertisements. Registry observations may apply expiry. |
| `invite <JSON>` | Explicitly authorizes one enrollment for the specified AgentID and bounded advertised roles/capabilities; `ttl_seconds` is 1–300. |
| `quit` / stdin EOF / Ctrl-C | Stops the listener, closes active connections, attempts fenced disconnections, invalidates all pending invitations and removes its own socket. |

After terminal B stops, `agents` shows its session DISCONNECTED and retains
historical evidence. Enrollment is READY with AssignedRole empty: supported roles
are capabilities, not responsibilities granted by the credential.

To authorize a new runtime for the same AgentID, create a fresh invitation with
`"replace":true` and a new credential filename. Go captures the current registry
reference in that invitation. Successful use supersedes exactly that incarnation;
a delayed invitation cannot replace an even newer session. Reusing a SessionID
is forbidden. An old connection cannot heartbeat or disconnect the replacement.
No automatic reconnect or permission inferred from disconnect/silence exists.

## Admission and binding

```text
independent client -> private Unix connection -> unbound Phase 3 stream
    -> V2 enrollment_request + invitation -> Go admission checks
    -> registry.Enroll -> private AgentID/SessionID/incarnation binding
    -> enrollment_response -> bound heartbeat/disconnect/handoff
```

The invitation uses a random 128-bit public ID and CSPRNG 256-bit secret. Only
SHA-256 verification material is retained in server memory, compared in constant
time for fixed-size secrets. AgentID, roles and capabilities must satisfy the
explicit invitation constraints and Phase 1 identity validation. Provider names
are informational and unknown providers are allowed. Invitations grant local
admission, not cryptographic proof of a provider/vendor's identity.

Invalid, expired, revoked, mismatched or replayed credentials never call registry
enrollment and create no session. Consumption and revocation serialize with the
enrollment decision. The credential is burned before durable publication, so a
storage error cannot leave a reusable invitation. No agent request selects its
registry generation. Pending invitations are process-local and bounded; restart
invalidates them. The explicitly delivered private credential file is not a
persistent server invitation database and cannot revive an invitation on restart.

The server retains its own immutable `agentregistry.Ref`. Every bound message
must match AgentID, SessionID and protocol version; registry operations fence the
current incarnation atomically. Heartbeat updates only last-seen. Disconnect and
handoff mark that exact session DISCONNECTED; handoff transfers no task/claim.
Duplicate enrollment, capability changes, assignment acceptance, progress,
blocker and result messages are not implemented here and terminate the connection.
V3 Planner responses are accepted only for the exact selected bound session and
pending correlated request. An agent cannot select itself or create a request.
Malformed/unsupported messages fail in strict transport decoding before handling.
Errors are categorical and do not echo credentials or raw payloads.

## Limits, shutdown and persistence

Defaults are 16 active connections, 64 pending invitations, a ten-second total
handshake deadline, thirty-second bound idle deadline, and five-minute maximum
invitation lifetime. Limits are capped in Go. Excess connections are closed
without handler goroutines. Phase 3 bounds each frame to 1 MiB and serializes
writes. Idle timeout must not exceed the registry's liveness policy; failed/late
heartbeats cannot revive expired sessions. Timers are injectable for deterministic
tests. No unbounded queues, per-invitation timers or remote services are started.
Protocol deadlines interrupt stream I/O; synchronous registry filesystem writes
still depend on local storage responsiveness and the existing store lock policy.

Listener flags include `--max-connections`, `--max-invitations`,
`--handshake-timeout`, `--idle-timeout` and `--stale-after`. The default registry
policy is ninety seconds; if the registry already exists, `--stale-after` must
match its persisted policy. The bridge's heartbeat interval must be shorter than
the listener idle/liveness deadlines. Console access must be trusted; never pipe
untrusted agent output into this administrative input.

Socket parent directories must be owned by the current UID with mode 0700; sockets
must have mode 0600. Ancestors must be canonical and protected from other users'
writes, except root-owned sticky directories such as `/tmp`. Existing sockets,
including stale ones, are never stolen or automatically removed. Inspect an old
listener before choosing a fresh path or deliberately removing its stale socket.
Cleanup compares the created socket's file identity and preserves replacement
paths, reporting a cleanup error. The private parent directory is retained.

Registry sessions persist through the existing locked atomic state writes;
invitations and transport routing do not. Phase 5 Planner selection/request
records do persist in that same structured state, distinct from connection routing.
Admission/heartbeat/disconnect alone edits no Markdown ledger or task/claim
ownership. Bound V4 worker reports are routed to the controller’s authoritative
scheduler handler only while an exact dispatched assignment is active. Normal EOF, connection failure, idle deadline and graceful shutdown
attempt exact fenced disconnection. A registry cleanup failure stops the listener
and reports an authority/storage error for investigation. No history is reset.

Admission publication, binding and response delivery are not a distributed
transaction. A crash or lost response after publication can leave an admitted
historical session without a client acknowledgment. A reported post-rename fsync
error may also mean publication occurred; the handler tries to quarantine only
the record with this admission provenance. There is no partial successful client
binding on errors. Inspect `agents`/reported storage errors and authorize a fresh
replacement invitation as needed; do not edit state or retry a consumed secret.
Abrupt process death may leave registry status until liveness expiry is observed;
new sessions have no assigned role and are ineligible for dispatch. Upgrade all
writers before admitting V2 identities; older binaries cannot interpret them.

## Security and implementation boundary

The reusable Go client is internal to this module. External integrations can
implement the documented V2 NDJSON/Unix contract; no public SDK is shipped yet.

Unix permissions plus invitation possession establish local admission. This does
not defend against root, malicious processes with the same UID, hostile/untrusted
filesystems, arbitrary native code able to edit state, or stolen credential files.
It is local orchestration, not an OS/container sandbox. Filesystem locking is not
distributed multi-host consensus. The listener exposes no TCP/public endpoint.

Phase 5 human selection and bound Planner exchange are implemented through
`cmd/planner --enrolled-planner-socket`; the standalone listener console remains
an admission/liveness tool. Phase 6 adds bounded V4 worker conversations to the
run-hosted listener with existing Go scheduler/validation authority. Remote/public
networking and automatic merge/deployment remain unimplemented. Existing managed providers continue through their
established adapters and guards. A compatible client may advertise PLANNER if
invited, but that never selects it as Planner or makes it a coordination authority.
Only explicit human selection creates a Planner binding. Human
authorization, final evidence review and merge/deployment authority remain intact.
