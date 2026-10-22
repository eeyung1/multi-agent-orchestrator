# Human-selected enrolled Planner: Phase 5

Choose either a managed Planner (the existing default) or an independently
started compatible local Planner bridge. Both return the same structured
proposal contract. Go remains the coordination authority; human plan, policy,
recovery, final review and merge/deployment decisions remain mandatory.

## Start a run and attach a compatible Planner

Use a test Git workspace and your existing trusted managed-worker/validator
configuration. Workers remain managed adapters; they are not selected from the
agent registry. As in the existing workflow, repository authority enrollment,
custom configuration trust and missing validation/preparation policy require
explicit human decisions. Authenticate any managed worker CLIs yourself.

On Unix, create a new private directory outside task scopes (use canonical
`/private/tmp` paths on macOS). From the orchestrator checkout, terminal A:

```bash
mkdir -m 700 /tmp/mao-planning-example
go run ./cmd/planner --workspace ~/Projects/my-project \
  --enrolled-planner-socket /tmp/mao-planning-example/agents.sock \
  --objective "Write a bounded example document"
```

This explicitly chooses enrolled mode instead of `--planner`/`--provider`.
Use `--config` and/or the existing worker-selection prompts as needed. Managed
workers are configured before the enrollment console opens. The listener belongs
to this Planner run; do not start `cmd/agent-listener` on the same path. A Planner
run cannot route to connections held by a separate listener process.

At the trusted enrollment console, authorize an invitation:

```text
invite {"agent_id":"my-planning-agent","credential_file":"/tmp/mao-planning-example/planner-invitation.json"}
```

This console's invitations default to PLANNER support and the exact `planning`
capability, with a five-minute expiry. Optional `roles` and `capabilities` arrays
explicitly authorize broader advertisements, for example `"roles":["PLANNER","WORKER"]`
and `"capabilities":["planning","coding"]`. They grant admission only, not a role.
The 0600 file contains a sensitive one-use credential; its contents are never
printed. `invitations` lists metadata; `revoke <id>` revokes a pending invitation.
Optional `"replace":true` authorizes fresh-session supersession for that AgentID,
not automatic Planner selection. See [admission/security](agent-attachment.md).

In terminal B, start your compatible V3 bridge independently with that socket,
credential file and AgentID. It must speak the documented
[Planner request/response contract](agent-protocol.md#phase-5-v3-planner-session-contract)
and maintain heartbeats while reasoning. No arbitrary existing chat/process can
join without such a bridge or integration surface.

Back in terminal A:

```text
candidates
select 1
```

The list shows eligible connections hosted by this run: AgentID, informational
provider, SessionID, incarnation, READY status and capabilities. Go requires a
current, live V3 enrollment with PLANNER support, `planning`, and either no
assigned role or PLANNER. WORKER-only, BUSY, stale, disconnected or superseded
sessions are excluded. Metadata alone never makes an agent eligible.

Review the exact session shown in the confirmation and explicitly answer yes.
An empty answer/refusal does not select it. Selection atomically assigns the role
and stores the exact AgentID/SessionID/incarnation plus a fresh run/binding ID.
Only one active Planner exists per run. Another session cannot win selection
by sending a message or heartbeat. Concurrent selection is compare-and-swap
fenced in persistent Go state.

If no eligible client appears, invite/start a compatible bridge or `cancel` and
restart without `--enrolled-planner-socket` to use managed Codex/Claude/Gemini or
your trusted custom Planner. There is no silent fallback/provider substitution.

## Exercise the generic bridge without a provider integration

`cmd/agent-bridge --proposal-file` is a protocol demonstration, **not an AI
Planner**. It reads a supplied JSON proposal for each request and echoes Go's
correlation. Real integrations must reason over the typed request and translate
their provider's output into that schema.

For a test policy with managed worker capability `coding` and validator `check`,
save this as `/tmp/mao-planning-example/proposal.json`. Replace those names with
your actual configured names; Go rejects invented checks/capabilities. The
objective must match the exact objective supplied to Planner.

```json
{
  "plan": {
    "id": "suggested-only",
    "objective": "Write a bounded example document",
    "tasks": [{
      "id": "example-doc",
      "feature": "example-doc",
      "scope": "docs/example.md",
      "description": "Write a concise example document for this project",
      "acceptance_criteria": ["The document describes the project's purpose"],
      "validation": ["check"],
      "capability": "coding"
    }]
  },
  "questions": []
}
```

Start the independent fixture bridge in terminal B, then list/select/confirm it
in terminal A as above:

```bash
go run ./cmd/agent-bridge \
  --socket /tmp/mao-planning-example/agents.sock \
  --credential-file /tmp/mao-planning-example/planner-invitation.json \
  --agent-id my-planning-agent --provider compatible-example \
  --proposal-file /tmp/mao-planning-example/proposal.json
```

This mode advertises V3, PLANNER and `planning`; no provider-specific behavior is
selected. It retains the existing heartbeat loop and graceful disconnect. The
file is read afresh for each request; questions can exercise clarification, but
a static repeated question will reach the existing clarification limit.

## Request, approval and execution flow

After selection, Planner performs Go recon and separately asks permission to
send bounded project context. Go sends the selected connection one typed request
containing objective, recon, existing coordination context and configured policy
constraints, with fresh Go-generated request correlation. Other clients receive
no request. Heartbeats remain separate from reasoning and grant no new authority.

The bound handler verifies identity, current incarnation, exact selection and
pending correlation before recording a response. Go checks the proposal and
handles clarifications. It replaces the suggested objective ID, validates scopes,
dependencies, capabilities/checks and recon, displays the concrete plan, and asks
the normal explicit execution-authorization question. Answer no to stop a
protocol-only exercise without admitting tasks.

On yes, Go atomically rechecks the live selected session and exact completed
proposal digest while admitting tasks. A disconnect/replacement during the
approval prompt blocks adoption. A completed response cannot authorize a second
objective. Existing managed workers, worktrees, validators, result collection,
consolidated runtime report and human review then run normally. This phase does
not add a second Planner summarization RPC or dispatch work to enrolled workers.

## Failure, restart and replacement

Wrong identity/correlation, duplicate/unsolicited responses, malformed messages,
supersession and disconnect fail closed. An unselected agent cannot submit an
accepted proposal. Registry expiry invalidates selection when observed; response
and adoption checks always observe liveness. Receipt of a stale message can never
revive a binding. Silence never authorizes a replacement.

`--planning-timeout` bounds each request (default five minutes). Clients must
heartbeat more frequently than the listener's thirty-second idle deadline;
heartbeats cannot extend the planning request deadline. `--registry-stale-after`
defaults to ninety seconds and must match an existing registry policy. Cancellation
or timeout stops the binding and records request failure; no plan is authorized.

Bindings, replacement history, latest request status/digest and adoption evidence
persist in schema 3 `state.json` using the existing local lock/atomic write/fsync
path. Request contents, proposal contents and invitation secrets are not stored
there. Schema 1/2 migration preserves existing authority and creates no selection.
Stop older writers before upgrade. Never edit ledgers/state to repair a session.

Connections and pending invitations are process-local. Restart does not replay
requests or revive a transport; an interrupted binding remains historical until
disconnect/expiry is observed. Start a fresh run, issue a new replacement
invitation where needed, and explicitly select the new session. Previously
adopted objectives resume through the normal `--resume <objective-id>` path
without selecting a Planner or invoking it again. Existing execution-policy
reauthorization still applies if configuration changed.

The trusted Go replacement API requires the prior binding ID and explicit human
authorization; outstanding requests must first be stopped. The CLI selects once
per run: to replace after a planning failure, restart and reauthorize. Neither
same-session reconnect nor live in-place CLI replacement is provided.

This is local orchestration, not an OS/container sandbox or distributed lock.
Unix permissions/invitations do not protect against hostile same-UID/root code.
No remote/public networking, universal vendor bridges, automatic merge/deployment,
or Phase 6 heterogeneous enrolled-worker scheduling is implemented.
