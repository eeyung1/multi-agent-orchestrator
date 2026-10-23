# Enrolled workers: Phase 6

Phase 7 [local operations](operations.md) adds read-only human/JSON status and
watch, agent/task inspection, concurrency visibility, attention and the existing
generation-fenced recovery flow. The Worker console accepts `status [--json]`,
`inspect-agent --agent ID` and `inspect-task --objective ID --task ID`. It can show
host-local route observations and configured concurrency; standalone views label
connectivity and unavailable policy explicitly. No status view grants ownership,
returns private claim tokens, or changes these Phase 6 dispatch semantics.

Compatible independently started LOCAL clients/bridges can now receive coding
assignments through the same Go scheduler used by managed workers. Three
independent V4 clients can execute distinct eligible tasks simultaneously. Provider
metadata is informational: a new provider needs a compatible bridge, no scheduler
enum or provider-specific dispatch rule. This does not make an arbitrary existing
chat compatible, supply AI reasoning, or integrate every vendor product.

Go remains the sole authority for scheduling, claims, assignment generations,
worktrees, execution admission, validation, results and recovery. The Planner
proposes; the human approves objectives/policy/recovery and retains final review,
merge and deployment authority. Workers cannot self-assign, grant roles, acquire
arbitrary work, override claims, merge, deploy or infer permission from silence.

## Trusted configuration and local operation

Managed workers remain the default. Enrolled dispatch is enabled only by a trusted
configuration's `enrolled_capabilities` allowlist. Existing `Task.capability` is
one required, case-sensitive, extensible identifier; this phase does not add a
closed capability enum or multi-capability task expressions.

A minimal demo policy (use real project validators for real acceptance):

```json
{
  "planner": {},
  "workers": {},
  "enrolled_capabilities": ["coding"],
  "acceptance_seconds": 30,
  "validators": {
    "whitespace": {"Program": "git", "Args": ["diff", "--check", "HEAD~1", "HEAD"]}
  },
  "required_validators": ["whitespace"],
  "allowed_scopes": ["demo"],
  "max_workers": 3,
  "timeout_seconds": 1800,
  "heartbeat_seconds": 5,
  "repair_attempts": 0
}
```

`acceptance_seconds` defaults to 30 when omitted/zero; maximum 300. The normal
execution timeout bounds the whole exchange. Configured trusted worktree
preparation runs before delivery, through the existing guarded executor. Neither
the Planner nor workers can define preparation/validator executables.

Start with an authenticated managed Planner CLI, for example:

```bash
go run ./cmd/planner --workspace ~/Projects/my-project \
  --config ~/orchestrator-policy.json --planner codex \
  --enrolled-worker-socket /private/path/agents.sock
```

The socket uses the existing private Unix-directory/socket rules. Parent paths
must be canonical and protected; do not reuse a live or stale socket blindly.
The `--registry-stale-after` policy defaults to 90 seconds and must match any
persisted registry policy. Bridges must heartbeat more often than the listener's
idle deadline (normally 30 seconds).

Approve the proposed plan through the normal prompts. For a first demonstration,
request three independent tasks with exact scopes `demo/a`, `demo/b`, `demo/c`,
capability `coding`, and validation `whitespace`. Each should write its own
`result.txt`. Review the plan rather than assuming model output matches the request.

Before execution the trusted worker console supports:

- `invite <JSON>`: explicitly authorize one short-lived, one-use invitation.
- `invitations` / `revoke <id>`: inspect redacted pending invitations or revoke one.
- `workers`: list connected, live, READY V4 sessions advertising WORKER support.
- `authorize <number>`: confirm the exact listed AgentID/SessionID/incarnation as
  assigned WORKER. Advertised support alone never grants this role.
- `run`: schedule eligible work; missing workers leave tasks PENDING with a reason.
- `cancel`: stop without dispatch.

Create invitations for three distinct agents, each with a separate credential
file (repeat with `worker-b` and `worker-c`):

```text
invite {"agent_id":"worker-a","credential_file":"/private/path/a.json","roles":["WORKER"],"capabilities":["coding"]}
```

The file is private 0600 secret material, not a log/ledger entry. It expires and
is consumed on admission; listener restart invalidates outstanding invitations.
Do not copy it into project coordination files.

Start each compatible bridge independently in another terminal. The included
example can exercise the lifecycle using a fixture rather than an AI model:

```json
{"files":{"{scope}/result.txt":"Example independent worker output\n"}}
```

Save that as a local fixture file, then run (use the corresponding invitation):

```bash
go run ./cmd/agent-bridge --socket /private/path/agents.sock \
  --credential-file /private/path/a.json --agent-id worker-a \
  --provider arbitrary-runtime-A --capability coding --worker-file ~/worker-fixture.json
```

`{scope}` expands to the authoritative assigned scope. Fixture writes must stay
inside it, respect symlink checks and remain uncommitted. Optional fixture
`reject` or `blocker` text demonstrates those terminal paths. The example sends
acceptance, progress, result/blocker and heartbeats; it does not reason or spawn
an AI provider. Real bridges translate the typed assignment into their own runtime
and stop every task writer before a terminal report.

Back in the trusted console, use `workers`, `authorize 1`, explicitly answer yes,
and authorize each intended worker. Then `run`. The demo's three eligible,
non-conflicting assignments can be in flight simultaneously. Go checks owned
changes, makes local commits, runs validation and produces the consolidated
report for Planner/human review. Whitespace validation alone is not product
acceptance. No merge/deployment occurs.

For an enrolled Planner, use `--enrolled-planner-socket` instead. Its run-hosted
listener is reused for workers; use the same invitations and role decisions in
the worker console after plan approval. Managed Planner selection is unnecessary
in that mode. Resuming an existing objective needs no replacement Planner; use
`--resume <objective-id> --enrolled-worker-socket /private/fresh/path.sock` for new
worker attachment. Policy changes still require explicit reauthorization.
The standalone `cmd/agent-listener` does not schedule tasks for another process.

## Selection and isolation

A candidate must be the current authoritative registry incarnation, live, READY,
connected through this run's attachment server, V4-capable, advertising WORKER
support, explicitly assigned WORKER, and advertising the task's exact required
capability. Planner-only, unassigned, BUSY, stale and disconnected agents are
excluded. Each enrolled session handles at most one assignment at a time.

For a capability with a configured managed worker, managed execution takes
priority. There is no silent fallback from a failing managed executable. For a
capability enabled only for enrolled dispatch, the oldest available Go-issued
incarnation wins deterministically. Provider names do not affect this ordering.
A project can configure managed capability `codex` alongside enrolled capability
`coding`; tasks choose their authorized capability. A dual-role session explicitly
assigned PLANNER is not also a WORKER.

`max_workers` bounds the controller's active executions. Scheduler reservation
also checks that objective's ASSIGNED count under the state lock. Dependencies,
active/BLOCKED scopes, claims and worker availability can reduce concurrency.
Scope conflicts never give two workers access to one authoritative assignment:

```text
Task A -> Worker A -> Generation A -> Claim A -> Branch A -> Worktree A -> Scope A
Task B -> Worker B -> Generation B -> Claim B -> Branch B -> Worktree B -> Scope B
Task C -> Worker C -> Generation C -> Claim C -> Branch C -> Worktree C -> Scope C
```

## Authoritative lifecycle and failure boundaries

1. The existing scheduler identifies an eligible task. Go selects an eligible
   enrolled session from the trusted capability pool.
2. One state.json transaction reserves the task's normal attempt/generation/private
   claim token and durable dispatch identity, and changes that session READY ->
   BUSY. Reserving BUSY early prevents concurrent controllers selecting it twice.
3. Existing workflow code establishes the operational supervisor session/claim
   and isolated Git branch/worktree, then records their exact identity.
4. Trusted preparation executes in that worktree with the existing assignment,
   enrollment, operational ownership and recon guards. No assignment is sent if
   preparation fails.
5. Go persists SENT before sending the typed assignment to the bound transport.
   The server keeps exact AgentID/SessionID/incarnation and correlation privately.
6. The worker explicitly accepts or rejects. Only ACCEPTED permits progress or a
   terminal blocker/result. Rejection does not pretend execution succeeded.
7. A terminal report relinquishes writing. Go verifies the persisted binding and
   existing task/claim/generation, checks owned Git output, commits owned changes,
   requires strict clean/branch/ancestry/scope review, captures HEAD, validates and
   restores trusted-validator filesystem side effects with unchanged history.
8. Go records exact-HEAD evidence. Final collection atomically completes the task,
   marks dispatch FINISHED and returns the exact current live BUSY session READY.
   Results then await human review. A disconnected/replacement session is never
   revived or modified during metadata collection.

State transactions cannot atomically include Git, operational Markdown writes,
socket delivery or independent execution. Durable stages and exact comparisons
fail closed on partial or uncertain publication. Reservation/worktree startup
failures retain their existing structured/operational evidence; errors are not
permission to silently redispatch. A crash inside pre-binding Git/operational
startup can require human investigation of that partial allocation. No automatic
history reset, stale-lock stealing or orphan worktree deletion is attempted.

Preparation, delivery, rejection, timeout, cancellation, protocol failure,
disconnect, supersession, blocker or validation failure stops the assignment
through the existing operational BLOCKED + structured BLOCKED path where the
operational binding is available. The worker remains BLOCKED or DISCONNECTED,
not automatically READY. No enrolled repair loop redispatches the same generation.
The exact preserved branch/worktree and dispatch history remain evidence.
Cancellation/deadline failure remains failure even if an exchange returns a nil
error after its context is cancelled. Supervision joins the exchange before
returning; it cannot forcibly stop an independently running external process.
Trusted local diagnostics retain worker-handler rejection causes for recovery.
Worker reports retry only a failed state-lock acquisition within the bounded
exchange context. Parallel validation can exceed one lock-acquisition wait; a
retry rereads and rechecks the entire binding under authority. Identity/order/
generation failures are never retried. Acceptance expiry, disconnect or execution
cancellation cancels and joins pending report persistence. No lock is stolen and
no ownership is inferred from a successful socket write.

Follow [normal authorized recovery](../advanced-setup.md#recover-a-blocked-planner-assignment)
rather than editing state/ledgers, deleting claims, changing tokens or removing
the old worktree. Review the preserved work first and ensure its external writers
are stopped. Recovery archives dispatch identity with the old assignment; a retry
gets a fresh scheduler attempt/generation/claim/branch/worktree. A fresh authorized
enrollment is normally used for a failed/disconnected participant. New sessions
inherit neither WORKER role nor an old task. Delayed old messages cannot block,
complete or disconnect a newer assignment/session.

Connections do not survive restart. An existing ASSIGNED task is never
redelivered from persisted dispatch state. On resume, a proven expired/disconnected
session can be blocked under the exact old assignment for human recovery. If
liveness is still unresolved, ownership stays reserved and the report asks for
reconciliation; silence never proves the writer stopped. Durable Go validation
plus published operational COMPLETE supports existing exact-HEAD metadata-only
finalization without trusting any new worker message. Missing/corrupt dispatch
history fails closed. Schema 1/2/3 migration to 4 preserves existing state; stop
older writers before upgrade and do not downgrade/edit state.

## Security and current limits

Invitations admit a local participant; provider metadata is not verified vendor
identity. Transport delivery is not authority. Every worker report is checked
against the exact server binding and durable scheduler dispatch before it has
authoritative effect. Unsupported/unsolicited messages are rejected, not ignored.
Progress cannot change ownership, roles, claims or generations. Claim tokens and
invitation secrets never appear in the worker assignment.

This remains local orchestration, not an OS/container sandbox. An independently
running same-UID process cannot be forcibly contained or proven stopped by a
socket close; a terminal report is a local participant's trust contract. Fencing
prevents stale control-plane writes, not arbitrary filesystem writes by malicious
local code. Recovery requires human review/quiescence and a fresh isolated attempt.
Local filesystem locking is not distributed multi-host consensus.

No public/remote networking, universal vendor bridges, automatic merge/deployment,
graphical operational dashboard is supplied. Phase 7 adds the local text/JSON
operator layer described above. Real-world heterogeneous
AI-product acceptance remains Phase 8; the regression acceptance uses three
independent protocol clients with different arbitrary runtime metadata and a
barrier proving simultaneous in-flight execution through actual local IPC.

## Verification

The three-worker acceptance test holds every client behind a channel barrier
until all three accept their assignments and complete a heartbeat round trip.
Before releasing any result, it inspects three ASSIGNED/ACCEPTED tasks, three
exact BUSY worker sessions, distinct registry incarnations and provider/runtime
metadata, and three exclusive claims, generations, dispatch IDs, branches and
worktrees. A serialized dispatcher cannot cross the barrier. After release,
Go-owned commits, validators, evidence and result collection must complete all
three tasks, return the workers READY and leave the objective AWAITING_REVIEW.
Replayed terminal results must fail. No timing sleep establishes overlap.

Focused regressions also exercise delivery failure before acceptance, explicit
rejection without automatic retry, binding faults on acceptance/progress/blocker/
result, fresh recovery generations, managed priority for a shared capability,
and sequential worker reuse only after validation and dependency release. The
generic bridge command's worker fixture is tested through local IPC separately
from its bounded-path tests.

Run from the repository root with private local Unix sockets available:

```bash
go test ./...
go test -race -timeout=20m ./internal/agentprotocol ./internal/agentregistry ./internal/agenttransport ./internal/agentattachment ./internal/planner ./internal/planning ./internal/scheduler ./internal/controller ./internal/execution ./internal/state ./cmd/planner ./cmd/agent-bridge ./internal/review ./internal/coordination ./internal/workflow ./internal/worktree ./internal/locking
go test -race ./internal/controller -run '^TestEnrolledWorkersParallelAcceptance$' -count=10 -timeout=20m
```
