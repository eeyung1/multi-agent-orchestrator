# Agent protocol, registry, transport, attachment and operations: Phases 1–7

The [README architecture contract](../README.md#target-architecture--in-development)
defines the product goal and roadmap. Phase 1 implements only the provider-neutral
wire/domain model in `internal/agentprotocol`. Nothing in this package connects,
enrolls, persists, schedules, supervises, or authenticates an agent.

Phase 2 adds the authoritative internal Go registry in `internal/agentregistry`,
persisted inside structured state (now schema 4). Existing managed CLI adapters, Planner
configuration, `controller.WorkerContext` and their operational coordination
lifecycle remain unchanged. Receiving WorkerContext does not enroll a subprocess
in the registry. Phase 3 adds the separate `internal/agenttransport` library for
validated envelope delivery, with bounded in-memory endpoints and generic NDJSON
stream framing. It has no authority handlers. Managed adapter integration remains
future work; see [the transport contract](agent-transport.md).
Phase 4 adds a separate local admission/session handler and Unix listener; see
[local attachment](agent-attachment.md). Existing managed dispatch is unchanged.

Phase 7 adds a separate local [operational projection](operations.md), versioned
`orchestrator-operations/v1`. This is an output contract, not an agent wire version,
role grant, or assignment protocol. It reads schema 4 without initialization,
migration, expiry or ownership writes. No V1–V4 message behavior, authority schema,
transport, or fencing semantics changes. Public status/inspection omits claim
credentials, invitation proofs, environment/configuration secrets and raw validator
output. Progress remains an explicitly labeled agent report; Go state and human
decisions remain distinguishable.

## Version and identity

`ProtocolVersion` accepts `orchestrator-agent/v1`, `orchestrator-agent/v2`,
`orchestrator-agent/v3`, and `orchestrator-agent/v4`. Earlier versions retain
their wire behavior. Attachment accepts V2/V3/V4; enrolled Planner interaction
requires V3/V4, and enrolled-worker dispatch requires V4. Missing/unknown versions fail closed, without negotiation/downgrade.
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
they do not transfer or release claims. Planner request/proposal payloads are
defined in V3 below. Full worker task, artifact and validation-evidence payloads
remain deferred.

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
attachment or same-session reconnect. Human Planner selection and capability-matched
local worker dispatch are supplied by the later integration layers, not this model. Phase 3's transport library is separate
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

State schemas 1, 2 and 3 are migrated to schema 4 under the existing state lock. Migration
preserves ownership/results/history and adds one audited authority revision; it
does not enroll agents or create registry records. **Stop older controller
binaries before upgrading.** Fresh older binaries reject schema 4, but already
running older writers cannot be retroactively upgraded. A missing registry with
durable registry history is rejected rather than interpreted as an empty registry.
Do not downgrade/edit state to retry. Existing provider configuration needs no
migration. Configured managed dispatch stays compatible; enrolled dispatch is opt-in.

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
planning-capable sessions may advertise or be assigned Planner responsibility, but
that alone does not create a planning binding. Phase 5 centrally enforces one
human-selected active binding per planning run.

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
no HTTP/RPC/stdio provider bridges, remote agents or same-session reconnect.
Phase 6 now adds local enrolled-worker dispatch; see below. Phase 5 adds local enrolled
Planner selection and execution; see below. The local trusted console
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
separate Go handler. It rechecks current registry state; Phase 6 task handling
also enforces the existing scheduler ownership gates. Phase 3 itself introduced
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
identity and current incarnation, and accepts unassigned heartbeat and
disconnect/handoff messages, plus V3 Planner responses for an explicitly selected
session with a pending Go-issued request. Advertisements stay advertisements; enrollment assigns
no Planner/Worker responsibility. Unsupported task-lifecycle messages terminate the connection.

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

## Phase 5: V3 Planner session contract

V3 preserves the V2 admission/lifecycle messages and adds two messages. V1/V2
reject Planner message types and Planner field presence, including null values.
No existing managed adapter must migrate its configuration.

| Message | Origin | Required payload |
| --- | --- | --- |
| `planner_request` | Go | `planning_request: {correlation, request}` |
| `planner_response` | Agent | `planning_response: {correlation, proposal}` |

`correlation` contains `run_id`, `binding_id`, and `request_id`. Go generates the
binding/request IDs using cryptographic randomness; the agent only echoes them.
Identity remains in the envelope, not in provider names or connection addresses.
No Planner message carries claims, assignment generations, role grants, enrollment
credentials or registry incarnation choices. Strict nested decode/direction and
1 MiB framing limits apply. Unknown authority/executable fields are rejected.

The dependency-free `internal/planning` package defines the shared managed and
enrolled data shapes; existing `planner.Request/Proposal`, `objective.Plan/Task`,
and `recon.Report/File/Existing` names remain aliases. `request` includes the exact
user objective; bounded recon (workspace, HEAD, inventory, instructions, context,
existing tasks/claimed scopes, limitations and instruction digest); configured
worker capabilities and validator names; allowed scopes and required validators.
It is structured context, never executable shell prompts. Bridges must treat
repository contents as untrusted, return proposals/questions only, preserve the
user objective and use configured worker/check names. They cannot invent user
approval, execution commands, coordination changes, merge or deployment.

`proposal` is the existing `{plan: {id, objective, tasks}, questions}` contract.
Tasks describe IDs/features, scopes, dependencies, description, priority,
acceptance criteria, configured validation names and worker capability. Questions
support the existing bounded clarification loop. Wire validation checks shape;
Go objective admission still checks paths, scope, policy, conflicts, dependency
ordering, recon drift and explicit human approval. Go replaces the suggested plan
ID. Clarification responses are recorded as CLARIFICATION and cannot authorize
an objective. Response receipt itself never admits tasks.

Registry APIs `PlannerCandidates`, `SelectPlanner`, `BeginPlannerRequest`,
`AcceptPlannerResponse`, `CheckPlanner` and `StopPlanner` use the existing locked
state transaction. Supported roles remain advertisements; selection separately
assigns PLANNER with human provenance. One active binding exists per run and a
session cannot serve multiple active runs. Replacement requires the exact prior
binding ID plus renewed human authorization; an outstanding request must first
be stopped. Historical bindings are retained. These APIs are trusted Go actions,
not agent socket operations. Same-handle registry calls queue under a local mutex
before acquiring the existing filesystem lock, preventing local sessions from
starving each other's lock-acquisition deadline. Independent handles/processes
still reread and fence under that filesystem lock; the mutex is not authority.

Requests move READY -> BUSY -> READY on valid completion; heartbeats continue
independently. Only one request is pending per binding. Duplicate, unknown,
previous-run, wrong-session/incarnation, identity-switched or superseded responses
are rejected. Timeout/cancellation stops the binding and records failure. Registry
expiry/disconnect/supersession or role/capability loss atomically invalidates the
selection and pending request; new sessions inherit neither role nor selection.

Completed evidence includes correlation and a deterministic digest over the
entire proposed plan except its Go-replaced ID. `controller.AdmitEnrolled` keeps
normal validation/human gates; `scheduler.Register` rechecks selection, liveness,
exact completed request and plan digest under the same lock that adopts tasks.
It records the adopted objective, preventing reuse for another objective. This
is an adoption fence, not dispatch to enrolled workers.

Schema 3 persists bindings, historical replacements, latest request status,
digest and adoption evidence inside existing `state.json`; it stores no invitation
secret or request/proposal content. Locked schema 1/2 migration preserves existing
ownership/registry data, selects no Planner and adds one audited revision. Stop
older writers before upgrading; current schema 4 also retains these bindings,
and older binaries reject it. Corrupt or missing
binding state with durable selection history fails closed. A process restart does
not restore transports or revive a pending request: use fresh admission and human
selection. See [enrolled Planner operation](enrolled-planner.md).

## Phase 6: V4 enrolled-worker contract

`orchestrator-agent/v4` preserves V2 admission/acknowledgments and V3 Planner
exchange while adding a `work` payload and five directional message types:

| Type | Direction | Payload |
| --- | --- | --- |
| `worker_assignment` | Go -> exact worker | correlation + typed assignment |
| `worker_acceptance` | worker -> Go | correlation + explicit `accepted` boolean; rejection reason required |
| `worker_progress` | worker -> Go | correlation + bounded informational detail |
| `worker_blocker` | worker -> Go | correlation + terminal blocker detail |
| `worker_result` | worker -> Go | correlation + terminal implementation summary |

`work.correlation` contains `objective_id`, `task_id`, `assignment_generation`
(the existing scheduler generation), and Go-generated `dispatch_id`. Envelope
AgentID/SessionID name the enrolled participant. The server also verifies its
private registry incarnation. None of these identifiers is a credential or lets
an agent choose ownership. The random dispatch ID is independent of the private
claim token. No operational claim token, invitation secret, credentials or
unrelated assignment identity is sent in the assignment.

`work.assignment` contains objective text, the existing typed task (description,
scope, acceptance criteria, validation names and required capability), exact
Go-selected worktree/branch, relevant project instructions, and bounded execution
rules. Changes are left uncommitted for Go's owned-path check and commit. A
terminal report or rejection is a declaration that all task writers have stopped
and relinquished write access. This is a local trust contract, not OS enforcement.

Only Go sends assignments; agents cannot send role grants or assignments.
Acceptance is required before progress/blocker/result. Duplicate acceptance,
unknown correlation, identity switches, stale generation/session/incarnation,
unsolicited or replayed terminal messages close the route and fail closed.
Reserved legacy `assignment`/`progress`/`result` messages remain unsupported by
attachment. V1/V2/V3 reject V4 fields, including `work: null`; strict JSON and the
transport frame bound apply to every message.

The attachment layer routes a single bounded pending conversation per session;
it does not adopt tasks or results. Its trusted callback checks/updates the
existing scheduler task dispatch under the state lock. Heartbeats continue
through the same sole receive loop and cannot extend the execution deadline.
Progress stores only the latest bounded detail, with an audit event; it grants
no ownership. A blocker becomes BLOCKED only through Go's existing exact
operational-session and structured-task blocking path.

Schema 4 adds a dispatch record to `Task` and its `AssignmentHistory`: enrolled
AgentID, SessionID, Go-issued incarnation, exact correlation, phase, latest message type and latest
detail. Only a terminal `worker_result` may enter Go validation; a rejection or
blocker cannot be treated as successful output. The registry retains a dispatch-history count so missing dispatch records
fail closed. Phases are RESERVED -> SENT -> ACCEPTED -> REPORTED -> FINISHED;
failures become FAILED. Reservation atomically sets the worker BUSY and task
ASSIGNED in the existing state resource. Completion atomically records the Go
result and returns the exact current live worker READY. No replacement session is
modified. See [lifecycle, crash boundaries and recovery](enrolled-workers.md).

Schemas 1/2/3 migrate under the existing lock to schema 4, preserving tasks,
claims, results, enrolled identity and Planner evidence. Migration invents no
worker dispatch or role assignment. Stop all older writers before upgrading;
older binaries reject schema 4. Transports are process-local and never restored
from durable dispatch records.
