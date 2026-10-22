# Agent protocol: Phase 1

The [README architecture contract](../README.md#target-architecture--in-development)
defines the product goal and roadmap. Phase 1 implements only the provider-neutral
wire/domain model in `internal/agentprotocol`. Nothing in this package connects,
enrolls, persists, schedules, supervises, or authenticates an agent.

Existing managed CLI adapters, Planner configuration, `controller.WorkerContext`,
structured-state schema and coordination lifecycle are unchanged. A subprocess
receiving WorkerContext is not thereby universally enrolled. Phases 2+ must
integrate this model with Go authority before using it for live participants.

## Version and identity

`ProtocolVersion` currently accepts exactly `orchestrator-agent/v1`. Missing or
unknown versions fail closed; no version negotiation or downgrade is implemented.
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
identities nor authorizes their reuse: stable identity verification, session
admission and reconnect fencing belong to later phases. Neither ID is a secret
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
rejection. These are model values, not implemented transitions or durable
admission decisions. READY/BUSY/BLOCKED/DISCONNECTED registry state, heartbeat
timers, stale detection and reconnect behavior are Phase 2 work.

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

There is no authoritative agent registry, dynamic enrollment service, external
attachment, reconnect handling, connected-agent Planner election, capability
matching scheduler, transport abstraction implementation, or new networking.
Managed CLI adapters have not been replaced or silently declared protocol-enrolled.
Future phases must preserve Go's sole authority and explicit human authorization;
silence, provider identity, advertised roles/capabilities and connection state
never imply permission. This remains local orchestration, not an OS/container
sandbox or distributed multi-host coordination.
