# Agent protocol, registry, transport and local attachment: Phases 1–4

The [README architecture contract](../README.md#target-architecture--in-development)
defines the product goal and roadmap. Phase 1 implements only the provider-neutral
wire/domain model in `internal/agentprotocol`. Nothing in this package connects,
enrolls, persists, schedules, supervises, or authenticates an agent.

Phase 2 adds the authoritative internal Go registry in `internal/agentregistry`,
persisted inside structured state schema 2. Existing managed CLI adapters, Planner
configuration, `controller.WorkerContext` and their operational coordination
lifecycle remain unchanged. Receiving WorkerContext does not enroll a subprocess
in the registry. Phase 3 adds the separate `internal/agenttransport` library for
validated envelope delivery, with bounded in-memory endpoints and generic NDJSON
stream framing. It has no authority handlers. Managed adapter integration remains
future work; see [the transport contract](agent-transport.md).
Phase 4 adds a separate local admission/session handler and Unix listener; see
[local attachment](agent-attachment.md). Existing managed dispatch is unchanged.

## Version and identity

`ProtocolVersion` accepts `orchestrator-agent/v1` and `orchestrator-agent/v2`.
V1 retains its existing serialization/shape behavior; Phase 4 local attachment
requires V2. Missing/unknown versions fail closed, without negotiation/downgrade.
Future wire-shape changes require an explicitly supported version and compatibility
tests; adding a provider name or capability does not require a new version.
This namespace is separate from the existing `orchestrator/v1` structured-state
protocol and from WorkerContext's natural-language `protocol` instructions.

`Identity` has explicit JSON fields:

| Field | Meaning |
| --- | --- |
| `protocol_version` | Agent wire/domain version. |
| `agent_id` | Typed `AgentID`, the logical participant identity. |
| `session_id` | Typed `SessionID`, one enrollment/runtime session. |
| `metadata` | Informational `provider`, optional `provider_version`, `runtime`, `runtime_version`. |
| `capabilities` | Advertised, extensible machine identifiers. |
| `supported_roles` | Explicit `PLANNER`, `WORKER`, or both. |

AgentID and SessionID are distinct Go types and namespaces. A new runtime can
describe the same logical AgentID with a different SessionID. Even equal textual
values do not make the two concepts interchangeable. This model neither issues
identities nor authorizes their reuse. Phase 2 authoritatively admits sessions and
issues/fences their incarnations through internal Go APIs. Transport identity
authentication now uses local one-use invitations; same-session reconnect remains
unimplemented. Neither ID is a secret
credential or an ownership grant.

Provider/runtime values are free metadata, with no known-provider enum or lookup.
An unknown implementation needs no core protocol change. Naming a provider
`PLANNER` or a runtime `ORCHESTRATOR` confers no role or authority. Supported roles
are advertisements, not assigned roles; even an assigned Planner remains a
reasoning participant under Go control.

Agent/session IDs contain 1–256 UTF-8 bytes, with no whitespace or control
characters. Metadata requires a provider name; its fields are bounded to 256
bytes, with no control characters or surrounding whitespace. These values do not
name executables, filesystem paths, or coordination permissions.

## Capabilities, roles and admission vocabulary

Capabilities are case-sensitive ASCII identifiers, 1–128 bytes: the first
character is a letter/digit, followed by letters/digits or `.`, `_`, `-`, `:`, `/`.
Examples are `coding` and `org.example/review:v2`. An identity advertises 1–128
capabilities and at least one supported role. Capabilities and roles preserve
their supplied list order; duplicates are rejected deterministically. Validation
does not trim, lowercase, sort, or mutate caller data. Unknown capability names
are valid; unknown roles, including `ORCHESTRATOR`, are invalid.

`EnrollmentState` defines only `REQUESTED`, `ADMITTED`, and `REJECTED`. An
enrollment response describes ADMITTED or REJECTED, with a reason required for
rejection. These model values alone perform no admission; Phase 4 applies local
admission decisions through Go. Phase 2 maintains separate operational registry states
READY/BUSY/BLOCKED/DISCONNECTED; protocol messages do not apply those transitions
themselves. There is no registry background heartbeat timer or same-session
reconnect transport; Phase 4's example client explicitly sends heartbeats.

## Envelope and message direction

`Envelope` always contains `protocol_version`, `type`, `agent_id`, and
`session_id`. Identity fields name the participant on both inbound and outbound
messages. Additional fields are permitted only for their logical message type:

| Type | Expected origin | Additional fields |
| --- | --- | --- |
| `enrollment_request` | Agent | Required `identity`. |
| `enrollment_response` | Go ORCHESTRATOR | Required `enrollment` containing `state` and optional/required `reason` as above. |
| `capability_registration` | Agent | Required full `identity` advertisement. |
| `role_assignment` | Go ORCHESTRATOR | Required explicit `role`. |
| `assignment` | Go ORCHESTRATOR | Required `role` and `assignment` reference. |
| `assignment_acceptance` | Agent | Required `assignment` reference. |
| `progress`, `blocker`, `result` | Agent | Required `assignment` reference and `detail`. |
| `heartbeat` | Agent | Optional `assignment` reference. |
| `handoff`, `disconnect` | Agent | Optional `assignment` reference and `detail`. |

An identity advertisement must match the envelope's version, agent and session.
An assignment reference must match its session. `detail` and admission reasons
are bounded single-line text without control characters or surrounding
whitespace (64 KiB and 4 KiB respectively). A result detail is an untrusted
summary, not a validation verdict. Handoff/disconnect describe requests/reports;
they do not transfer or release claims. Full task, proposal, artifact and
validation-evidence payload schemas are deliberately deferred.

Use `Encode(envelope)` for validated deterministic JSON and
`Decode(bytes, origin)` at a future receiving boundary. `Validate()` checks
shape; `ValidateFrom(origin)` additionally checks direction. Origin is supplied
by the trusted caller and is deliberately absent from JSON. Agent-origin
admission responses, role assignments and assignment issuance are rejected.
Provider metadata cannot substitute for origin. A future transport must establish
the actual origin/session binding outside message data; this argument is not an
implemented authentication mechanism.

Decode rejects unknown fields (including nested fields), duplicate keys including
case variants, trailing JSON/data, invalid UTF-8, excessive nesting, messages over
1 MiB, invalid types/roles/versions, missing required fields and mismatched
identities. Raw `json.Unmarshal` is not the validated protocol entry point.
Encoding retains struct field and supplied list order. Neither encoding nor
decoding invokes provider code or changes ownership/state.

Example advertisement from a hypothetical implementation, not a live attach
command or proof of enrollment:

```json
{
  "protocol_version": "orchestrator-agent/v1",
  "type": "enrollment_request",
  "agent_id": "developer:local-1",
  "session_id": "session:new-run-1",
  "identity": {
    "protocol_version": "orchestrator-agent/v1",
    "agent_id": "developer:local-1",
    "session_id": "session:new-run-1",
    "metadata": {"provider": "future-implementation", "runtime": "example"},
    "capabilities": ["coding", "org.example/review:v2"],
    "supported_roles": ["PLANNER", "WORKER"]
  }
}
```

## Existing assignment authority

`AssignmentRef` mirrors the scheduler's existing ownership tuple:

| Protocol field | Existing authoritative source |
| --- | --- |
| `objective_id` | `state.Task.ObjectiveID` / admitted objective ID. |
| `task_id` | `state.Task.ID` / acquired task ID. |
| `assignment_generation` | `state.Task.AssignmentGeneration` / `scheduler.Result.AssignmentGeneration`. |
| `token` | `state.Task.Token` / scheduler-issued claim token. |
| `session_id` | `state.Task.SessionID` / coordination session bound to the assignment. |

Generation is a nonzero `uint64`, not the repository authority generation, a
session counter or an agent-supplied increment. Shape validation cannot establish
that a reference was ever issued. `ValidateAgainst(expected)` compares every
field with a trusted scheduler snapshot, without changing either value. Errors
do not include claim tokens. Token-bearing references must not enter public
reports or logs.

The expected reference must come from Go authority, never a second agent-supplied
message. This pure comparison does not replace scheduler status/generation
fencing, enrollment, session ownership, branch/worktree, scope, instruction,
execution, exact-head validation, or review checks. Existing guarded operations
must still check fresh authority at their original boundaries. There is no new
claim store, generation issuer, task acquisition or completion path here.

The scheduler integration regression acquires an actual assignment, serializes
its tuple through this protocol, compares it with structured state, rejects a
forged generation through both comparison and existing scheduler completion,
and verifies that protocol processing leaves authoritative state unchanged.

## Explicit Phase 1 boundary

The protocol model alone grants no authority. Phase 2 now provides an internal Go
registry and Phase 4 supplies local invitation admission. There is no remote
attachment, same-session reconnect, enrolled Planner election or capability-matching
scheduler dispatch. Phase 3's transport library is separate
from this pure model and performs no authoritative handling.
Managed CLI adapters have not been replaced or silently declared protocol-enrolled.
Future phases must preserve Go's sole authority and explicit human authorization;
silence, provider identity, advertised roles/capabilities and connection state
never imply permission. This remains local orchestration, not an OS/container
sandbox or distributed multi-host coordination.

## Phase 2: authoritative registry and liveness

`internal/agentregistry` is a trusted internal Go boundary, not a wire handler or
an authentication system. Agents advertise facts; Go decides admission, assigned
roles and lifecycle changes. AgentID, provider metadata and a numeric incarnation
are not credentials. Future transports must authenticate/bind a participant to
its admitted session before invoking these APIs. No authentication token or
shared secret has been invented for this phase.

### Persistence and compatibility

Registry data lives under `agent_registry` in the existing
`multi-agent-orchestrator/state.json`, with its existing repository identity,
state lock, 0600 temporary file, fsync and atomic rename. Parent-directory fsync
also checks publication durability. There is no separate registry file or
Markdown registry/heartbeat authority. Records retain identity/metadata,
capabilities, supported/assigned roles, admission/role decisions, status,
enrollment/last-seen/state-change timestamps, incarnation, and terminal/supersession
information. All incarnations remain historical evidence; they are not pruned.

The existing managed `Sessions`, `Claims` and `Tasks` continue to represent
managed execution. They are not silently converted to universal enrollments.
Registry session IDs cannot overlap session IDs referenced by managed sessions,
claims, tasks or assignment history. This is checked on both reads and writes
under the same state authority. Later adapter integration must deliberately bind
these lifecycles without maintaining competing copies of the same fact.

State schema 1 is migrated to schema 2 under the existing state lock. Migration
preserves ownership/results/history and adds one audited authority revision; it
does not enroll agents or create registry records. **Stop older controller
binaries before upgrading.** Fresh older binaries reject schema 2, but already
running older writers cannot be retroactively upgraded. A missing registry with
durable registry history is rejected rather than interpreted as an empty registry.
Do not downgrade/edit state to retry. Existing provider configuration needs no
migration and current dispatch still uses its configured adapters.

Reads reject unknown/duplicate JSON fields, malformed identities/roles, invalid
policy/timestamps, missing history, duplicate incarnations, broken supersession
chains and unsupported schemas. Errors do not reset or overwrite malformed
state. Pre-rename write failures retain the prior snapshot. A post-rename
directory-sync failure is reported, and the caller must reread to determine
whether publication occurred; an error is not proof that nothing was committed.
Existing/contended locks are never stolen based on age. These are local filesystem
guarantees, not distributed consensus or containment against unrestricted writers.

### Internal API and role boundary

Open with explicit trusted policy and an optional concurrency-safe clock:

```go
registry, err := agentregistry.Open(workspace,
    agentregistry.Policy{StaleAfter: 90 * time.Second}, nil) // nil uses time.Now
```

This is an internal Go example, not a new Planner config field or attach command.
The positive threshold is persisted in nanoseconds on initialization; reopen must
supply the same policy. There is no silent default or policy replacement API.
The repository must already have explicit ORCHESTRATOR authority enrollment.

| API | Authority/behavior |
| --- | --- |
| `Enroll(Enrollment)` | Validates Phase 1 identity and non-secret Go decision provenance; creates READY with no assigned role and a Go-issued incarnation. |
| `AssignRole(ref, role, decision)` / `ClearRole(ref, decision)` | Explicit Go role decisions; assigned role must be supported. Roles cannot change while BUSY. No global/objective Planner election is performed. |
| `Register(ref, identity)` | Replaces advertised metadata/capabilities/supported roles without changing identity, incarnation, assigned role or last-seen. Cannot withdraw an assigned role or replace BUSY advertisements. |
| `Heartbeat(ref)` | Updates only the current live session's last-seen timestamp; never assigns roles or alters task ownership/claims/assignment generations. |
| `Transition(ref, status, reason)` | Validates explicit lifecycle changes; BUSY requires an assigned protocol role. Marks no scheduler task or worktree as assigned. |
| `Get(sessionID)` / `List()` | Returns records with current/live flags, in generation order for List; observes and durably applies due expiry. |
| `Eligible(role, capability)` | Returns current, live, READY sessions with that explicitly assigned/supported role and exact registered capability. It is a snapshot, not a reservation or execution grant. |

Every mutable session API requires AgentID, SessionID and the exact issued
`session_generation` in `Ref`. `View.Ref()` provides this fence. Capabilities and
roles use Phase 1 validation, including deterministic duplicate rejection and
case-sensitive capability names. Provider names do not select roles. Multiple
planning-capable sessions may be assigned the Planner responsibility here; Phase 5
will enforce objective/session Planner selection centrally, with human approval.

### Incarnation, supersession and ownership fencing

`Enrollment` contains identity, decision provenance and optional `Supersedes`.
It cannot choose its issued generation or assigned role. First admission has no
previous-incarnation fence. Re-enrolling the same AgentID requires a fresh,
never-used SessionID and an exact `Supersedes` reference to the current incarnation.
This compare-and-swap prevents delayed/concurrent admission decisions from
replacing a newer session. New admission starts unassigned; the old incarnation
is atomically retained as DISCONNECTED with `superseded_by` information.

An old heartbeat, advertisement, role decision or lifecycle update cannot restore
old authority. A disconnected current session can only be superseded by this
explicit new-session admission; no same-session resurrection/reconnect is provided.
After uncertain publication, inspect the stored session instead of reusing IDs or
guessing a generation. Identity/capability advertisements and decision strings
are not transport credentials.

| Counter | Meaning |
| --- | --- |
| `agent_registry.last_session_generation` / session `session_generation` | Separate typed enrollment incarnation; advances only on successful new admission. |
| Task `assignment_generation` | Existing scheduler-issued ownership generation; registry operations never mint/change it. |
| `Authority.Generation` | Existing shared state revision/audit counter; advances on registry writes as on other state writes. |

Registry expiry/disconnect/supersession never releases claims, changes scheduler
ownership, or authorizes recovery. Future dispatch must check eligibility and
existing assignment guards atomically at the actual admission boundary. A BUSY
registry flag by itself does not establish a task assignment.

### Lifecycle and deterministic liveness

| From | Allowed explicit next states |
| --- | --- |
| Admission | READY, unassigned. |
| READY | BUSY, DISCONNECTED. |
| BUSY | READY, BLOCKED, DISCONNECTED. |
| BLOCKED | READY, DISCONNECTED. |
| DISCONNECTED | None; explicit fresh-session admission is required. |

Self/unknown transitions fail closed. Last-seen changes only on admission and
accepted heartbeat. A session is live through the exact configured threshold;
it expires when `now > last_seen + StaleAfter`. Every registry observation/update
checks expiry under the state lock and persists DISCONNECTED with reason
`heartbeat_timeout` before processing the requested action. There is no background
monitor, timer, sleep-based liveness test or claim takeover.

A late heartbeat returns a fencing error **while safely persisting the timeout**.
Read-only Get/List/Eligible can write when they observe due expiry; otherwise they
do not advance state. An injected clock is sampled under the lock; clock rollback
behind durable observations fails closed. Terminal expiry cannot be reversed by
a delayed heartbeat or clock change. Storage failure prevents successful
eligibility/admission responses. Decision/role/advertisement updates do not count
as heartbeats.

The implementation uses the existing local state lock for concurrent Go handles
and callers, so admission/supersession, liveness, role and capability operations
are serialized against other state changes. No cross-file registry transaction
is needed. Existing Markdown completion's separate durable-write boundary has
not been changed or presented as a cross-file transaction.

### What remains unimplemented

Phase 4 now adds Unix-local attachment and a generic bridge example. There are
no HTTP/RPC/stdio provider bridges, remote agents, same-session reconnect, dynamic Planner execution, or
scheduler dispatch to enrolled heterogeneous workers. The local trusted console
can list agents; there is no full connected-agent dashboard. Current
Codex/Claude/Gemini subprocesses still use their established
managed lifecycle and are not auto-enrolled. Phase 3 provides an internal transport
interface and stream framing; Phase 4 adds local admission/session binding.
Later phases must integrate provider adapters, execution and eligibility safely.

## Phase 3: transport validation boundary

`internal/agenttransport` sends/receives `agentprotocol.Envelope`. Incoming bytes
pass through the same strict `agentprotocol.Decode`, including duplicate/unknown
field rejection, version/shape checks and trusted direction validation, before
Receive returns a typed message. Outgoing envelopes must pass ValidateFrom and
Encode. Protocol wire fields, version, identities and AssignmentRef are unchanged.
Unsupported versions/types now have exported sentinel errors; the transport maps
them to redacted error categories without exposing decoder input or claim tokens.

The returned message remains untrusted with respect to authority: delivery is not
enrollment, a heartbeat write, a role decision, task admission or result acceptance.
The transport imports no registry/scheduler/provider APIs. Phase 4 now binds
local connections to admitted AgentID/SessionID/registry incarnations through a
separate Go handler. It rechecks current registry state; future task handling
must also enforce existing scheduler ownership gates. Phase 3 itself introduced
no admission credential or authentication flow.

See [agent-transport.md](agent-transport.md) for concurrency, ordering,
backpressure, cancellation, close/EOF, and adapter migration semantics.

## Phase 4: V2 local enrollment and bound lifecycle

V2 extends the existing envelope; it does not introduce a separate handshake
format. Fields added here are rejected on V1 or on unrelated message types:

| Message | V2 fields and rules |
| --- | --- |
| `enrollment_request` | Optional model field `invitation: {id, secret}`; mandatory for local admission. ID is 32 lowercase hex characters; secret is 64. `identity` remains required and must match the envelope. No request chooses an incarnation. |
| `enrollment_response` | ADMITTED requires nonzero `enrollment.session_generation`, issued by Go. REJECTED must not carry a generation. |
| `session_acknowledgment` | Go-only message with `acknowledgment: {for, session_generation}`. `for` is heartbeat, disconnect or handoff; generation is nonzero. No identity/role/assignment payload is allowed. |

Only the attachment handler interprets invitations. The protocol/transport merely
validate their shape and direction. Never log enrollment envelopes or invitation
proofs. The secret is a local bearer credential, not vendor identity or a task
claim. The handler keeps its own `agentregistry.Ref`, verifies every post-bind
identity and current incarnation, and accepts only unassigned heartbeat and
disconnect/handoff messages. Advertisements stay advertisements; enrollment assigns
no Planner/Worker responsibility. Later-phase messages terminate the connection.

Heartbeat acknowledgment follows the durable last-seen update. Disconnect/handoff
acknowledgment follows fenced durable disconnection; the client then closes the
transport. The server waits for that close under the existing idle deadline so an
abortive transport close does not discard a queued terminal acknowledgment.
This acknowledges the lifecycle operation, not durable task/result acceptance.

No structured-state schema or assignment identity changes. V2 identities are
stored in the existing registry. Upgrade all writers before admitting V2 sessions:
older binaries reject identities using unsupported protocol versions. Existing
V1 models, provider configuration and managed workflow behavior remain supported
by the upgraded binary. See [attachment operations and crash boundaries](agent-attachment.md).
