# Local orchestration operations: Phase 7

Phase 7 adds a provider-neutral read model and trusted local operator commands.
Go remains the sole authority. A view, a log, agent progress, a candidate listing,
or a recent heartbeat cannot authorize execution, transfer ownership, bypass
validation, or complete a task. No presentation state is persisted.

## Start and attach

Use the existing [quick Planner setup](../setup.md), [enrolled Planner](enrolled-planner.md),
and [enrolled worker](enrolled-workers.md) workflows. Managed defaults remain
supported. A typical enrolled environment starts with trusted policy containing
`enrolled_capabilities`, project validators, scopes and `max_workers`:

```bash
go run ./cmd/planner --workspace ~/Projects/project \
  --config ~/.config/orchestrator/planner.json \
  --enrolled-planner-socket /private/path/agents.sock
```

The private Unix parent/socket permissions and invitation rules still apply.
In the trusted Planner console, issue an invitation, start a compatible independent
client/bridge using its private credential file, list `candidates`, then explicitly
confirm `select <number>`. The agent's PLANNER advertisement alone grants no role.
Approve the proposed plan through the existing Go validation/human approval flow.

The Worker console reuses that listener. Invite the intended workers, start their
independent bridges, list `workers`, and explicitly confirm `authorize <number>`
for each exact session/incarnation. Then `run`. With a managed Planner instead,
use `--enrolled-worker-socket /private/path/agents.sock` for the same worker flow.
Managed and enrolled workers can coexist; configured managed capabilities retain
priority. The fixture bridge's `--proposal-file` and `--worker-file` modes simulate
structured exchanges and bounded writes; they do not supply AI reasoning or
universal integration with vendor products.

The standalone `cmd/agent-listener` still hosts admission/liveness only; it does
not schedule tasks for a separate controller. No administrative socket or remote
control service is introduced by Phase 7.

## Status

In another terminal, use the primary checkout, a subdirectory or a linked worktree:

```bash
go run ./cmd/orchestrator status --workspace ~/Projects/project
go run ./cmd/orchestrator status --workspace ~/Projects/project --json
go run ./cmd/orchestrator operations-help
```

The human view lists objective IDs, counts, Planner bindings, agent identities,
provider/runtime labels, roles, lifecycle/liveness, capabilities, tasks, exact
owners/generations, dispatch phases, claim status, branch/worktree, latest agent
reports, Go blockers, validation metadata, review state and attention items.
Multiple ASSIGNED/ACCEPTED rows with distinct BUSY owners/worktrees show concurrent
assignments. ASSIGNED includes reservations and validation/collection stages;
it does not prove an external process is alive.

New/empty workspaces show `PROJECT_NOT_INITIALIZED` without creating Git, state,
enrollment, registry records or audit events. A missing state file in an existing
coordination directory fails closed. Existing state must be current schema 4;
status never performs automatic migration or repair.

Schema 4 does not persist the run's concurrency policy. Supply the trusted policy
file to display its limit:

```bash
go run ./cmd/orchestrator status --workspace ~/Projects/project \
  --config ~/.config/orchestrator/planner.json
```

Only `max_workers` is projected. This is labeled
`provided_config_not_authority_verified`: reading a file does not prove that it
matches an objective's authorized policy digest. It neither changes policy nor
grants execution permission. Without a supplied policy, the limit is unknown.
The owning Worker console can display its actual configured limit, labeled
`owning_host_policy`. The existing limit applies to the controller/objective,
not a new global distributed quota.

The trusted Planner, Worker and listener consoles also accept `status [--json]`,
`inspect-agent --agent ID`, and `inspect-task --objective ID --task ID`. These
reuse the same projection. Existing invitation/selection/role commands remain
the explicit authoritative control paths.

## Connection and liveness interpretation

Registry liveness is derived at the observation time from the exact current
incarnation, lifecycle and last heartbeat. Status does not expire sessions.
For example, authoritative BUSY can be displayed with `stale: true`; that does
not release ownership or prove the writer stopped. Old sessions remain visible
with `current: false` and their superseding session.

Socket routing is process-local. A separate status process cannot know whether
the owning listener currently has a route, so `connection` is `UNKNOWN` and
`available_workers` is null. `ready_workers` counts current live registry-eligible
WORKER sessions, not proven connected workers. In an owning host console, exact-ref
route observations are `OBSERVED_CONNECTED` or `OBSERVED_ABSENT`; availability
can then be counted. Connection observations themselves grant no authority.
An absent route is scoped to that observing host: it does not prove that a
different local controller/listener lost its connection. Such observations use
`LOCAL_ROUTE_NOT_OBSERVED`, without changing Go task state or declaring the worker
globally disconnected.

## Inspection and candidates

```bash
go run ./cmd/orchestrator inspect-agent --workspace ~/Projects/project \
  --agent worker-a --json
go run ./cmd/orchestrator inspect-agent --workspace ~/Projects/project \
  --agent worker-a --session exact-enrolled-session
go run ./cmd/orchestrator inspect-task --workspace ~/Projects/project \
  --objective objective-id --task task-id --json
go run ./cmd/orchestrator candidates --workspace ~/Projects/project --role PLANNER --json
go run ./cmd/orchestrator candidates --workspace ~/Projects/project --role WORKER --json
```

Agent inspection includes sessions, advertised/supported capabilities and roles,
assigned role and decision provenance, heartbeat/lifecycle, current assignment
references, related tasks, Planner bindings and assignment history. Task inspection
includes dependencies, required capability, owner mode, enrolled identity and
incarnation or managed operational session, generation, claim status, isolation,
dispatch/report, Go blocker, validator names, validated HEAD, result ID/artifacts,
evidence presence, review status and recovery history.

`ownership_retained` distinguishes current ASSIGNED/BLOCKED ownership from a
completed assignment's last owner. A recovered PENDING task has no owner; its
stored generation remains the last issued generation until Go reserves a new one.

Managed subprocesses are not silently declared registry-enrolled. Their tasks
show `owner.mode: managed` and their operational session; enrolled tasks show
both operational identity and the exact AgentID/SessionID/incarnation binding.

Candidates are a read-only registry projection in ascending incarnation order.
Unknown connectivity remains explicit. Actual selection/admission still rechecks
through existing authoritative APIs and the owning host. To select/replace a
Planner or authorize Worker roles, use the existing exact-session human prompts;
this CLI does not pretend to attach a Planner to a different process's transport.

## JSON and watch

JSON has explicit `version: orchestrator-operations/v1`, state schema/protocol,
revision and UTC observation time. Arrays have deterministic ordering. Equal
authoritative input and observation options produce equal JSON; observation time
naturally changes between real-time reads. uint64 generations retain precision.
External consumers must preserve integer precision and treat all text as data.
No public HTTP endpoint or graphical dashboard is provided.

```bash
go run ./cmd/orchestrator status --workspace ~/Projects/project --watch --interval 2s
go run ./cmd/orchestrator status --workspace ~/Projects/project \
  --watch --json --interval 2s --updates 30
```

Watch is synchronous, read-only and context cancellable. Ctrl-C exits gracefully.
The default refresh is two seconds; allowed intervals are 250ms through one hour.
`--updates N` bounds successful frames (maximum 10,000); zero means until cancelled.
JSON watch is NDJSON: one complete snapshot per line. Text watch appends labeled
snapshots without terminal control sequences. Tickers are stopped and no reader
goroutines are left behind. Transient revision contention skips a watch tick;
corruption or unsupported authority stops the view with a nonzero error.

## Attention and safe actions

Attention is derived from existing records, not new task or ownership states:

| Code | Meaning / action |
| --- | --- |
| `REPOSITORY_ENROLLMENT_REQUIRED`, `OBJECTIVE_AUTHORIZATION_REQUIRED` | Use the existing explicit Go/human enrollment or plan approval flow. |
| `AGENT_STALE`, `AGENT_DISCONNECTED`, `CLOCK_REGRESSION` | Reconcile the exact session or local clock; do not infer availability. |
| `PLANNER_UNAVAILABLE` | Inspect failed/fenced selection and use fresh admission/selection in the owning Planner host. |
| `WORKER_SESSION_LOST` | Assignment remains owned; resume/reconcile the controller before explicit recovery. |
| `LOCAL_ROUTE_NOT_OBSERVED` | This observing host has no route; inspect the owning host rather than inferring a global disconnect. |
| `TASK_BLOCKED`, `VALIDATION_FAILED` | Inspect the preserved assignment and Go evidence. |
| `CROSS_RESOURCE_RECONCILIATION` | Reservation/operational identity is incomplete or mismatched; investigate partial allocation. |
| `BLOCK_PUBLICATION_PENDING` | Operational BLOCKED precedes structured blocking; reconcile the controller first. |
| `FINALIZATION_REQUIRED` | Durable validation plus operational COMPLETE can be finalized by existing `cmd/planner --resume`. |
| `REVIEW_REQUIRED` | Human review of exact branch heads and evidence is required. |

`validation_status` summarizes persisted metadata. `RECORDED` does not revalidate
the current worktree. Ordinary status performs no Git status/diff, full recon,
validator execution or expensive worktree scanning. Raw validator output is
omitted because it can contain environment/authentication data. Review the
Go-owned evidence/worktree through the established human review flow when needed.

Explicit recovery is available only for an already-BLOCKED, fully identified
assignment whose operational state supports the existing recovery API. Inspect
the exact generation and preserved work first, and stop all old external writers:

```bash
go run ./cmd/orchestrator recover-task --workspace ~/Projects/project \
  --objective objective-id --task task-id --generation 42 \
  --actor alice --reason 'reviewed preserved work and stopped old writer' \
  --yes --writers-stopped
```

This calls `scheduler.AuthorizeRecovery`, retaining its ownership checks,
operational retirement, history, and generation fence. It does not launch work,
transfer ownership, make a failed worker READY, or reuse its branch/worktree.
Resume through the existing Planner flow. Partially bound allocations are not
made recoverable by a UI override. A crash after operational HANDOFF but before
PENDING is visible as an idempotent recovery retry.

To fence an exact enrolled session intentionally:

```bash
go run ./cmd/orchestrator disconnect-agent --workspace ~/Projects/project \
  --agent worker-a --session exact-session --incarnation 7 \
  --actor alice --reason 'retiring this exact session' --yes
```

This calls `Registry.Disconnect`. It does not kill external processes, release
claims, complete work or disconnect a replacement incarnation. The owning
controller fails closed for lost active workers. Existing registry action APIs
may apply their normal expiry semantics; read-only status never does.

No silence authorizes an action. There is no force-complete, steal-task,
ignore-validation, generation rewrite, automatic merge or deployment action.

## Audit, consistency and privacy

Agent reports are labeled separately from Go-authoritative state/blockers and
human authorization. Public audit entries show existing recorded actor,
operation, revision and timestamp (latest twenty); role/Planner decisions and
session reasons retain their existing attribution. Recovery records the supplied
human actor/reason in existing state/evidence. Output is never authority.

State/registry/tasks/bindings/results come from one atomically published schema-4
snapshot. The reader brackets one atomic ledger read and exact-ref host
observations with equal structured authority revisions, retrying at most three
times. No new lock order, lock stealing or presentation database is introduced.
An unstable one-shot view requests a retry. Real intermediate cross-resource
stages remain explicit; there is no claim that state, Git, sockets and independent
writers form one transaction. State/ledger reads are bounded at 64MiB/16MiB.
Corrupt/unsupported state or a corrupt ledger fails closed without initialization,
migration, reset or repair. Older state must use the existing authoritative
startup/migration flow.

Operational public structs omit private claim tokens, raw state/config objects,
invitation credentials, environment values, and raw validation output. Known
current/historical/ledger claims echoed into public text are redacted, along with
common labeled credentials, bearer/basic authorization and private-key blocks.
Free text remains untrusted and this is not universal semantic DLP: never put
credentials in progress, labels or human reasons. Legacy low-level ownership
interfaces are not substitutes for these redacted UI views.

Absolute local workspace/worktree/artifact paths are intentionally exposed for
local diagnosis. Protect captured JSON and do not publish it indiscriminately.
Local same-UID/root participants are trusted; this is not an OS sandbox. A socket
close cannot prove an independently running writer stopped. Recovery requires
human review/quiescence. No public hosted control plane, Internet attachment,
distributed consensus, universal vendor-product support or graphical dashboard
is implemented. Phases 1–7 are local Go authority and operational foundations;
broader real-product integration remains future work.

## Acceptance and regressions

`TestOperationsCLIParallelAcceptance` uses actual local IPC, an enrolled Planner,
three heterogeneous Worker clients and three non-conflicting assignments. Before
any result, it queries human/JSON status and verifies three distinct BUSY owners,
generations and worktrees. An acknowledged heartbeat orders progress visibility;
a disconnect produces attention, independent tasks finish, explicit recovery
preserves history, and final Go results await human review. Channels and protocol
round trips establish ordering, without sleeps.

```bash
go test ./...
go test -race -count=1 -timeout=20m ./internal/agentprotocol ./internal/agentregistry ./internal/agenttransport ./internal/agentattachment ./internal/planner ./internal/planning ./internal/scheduler ./internal/controller ./internal/execution ./internal/state ./internal/review ./internal/operations ./internal/coordination ./internal/worktree ./internal/objective ./internal/workflow ./internal/locking ./cmd/planner ./cmd/orchestrator ./cmd/agent-listener ./cmd/agent-bridge
go test -race -count=1 ./internal/controller -run '^(TestEnrolledWorkersParallelAcceptance|TestOperationsCLIParallelAcceptance)$' -timeout=10m
```
