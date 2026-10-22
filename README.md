# Multi-Agent Orchestrator

The product goal is a universal, agent-neutral orchestration system in which
multiple heterogeneous AI coding agents can enroll simultaneously. Codex, Claude,
Gemini, OpenCode, Kimi, Antigravity, and future compatible agents are examples of
agent endpoints, not architectural dependencies. The final system must not depend
on a fixed provider list: compatibility, capabilities and explicit enrollment
determine participation; provider identity does not grant authority.

**Currently implemented:** authoritative local Go orchestration with configured
adapters and managed worker processes. **Target architecture / in development:**
universal agent enrollment and a transport-neutral protocol, including independent
agents requesting admission. Dynamic attachment is the gap this milestone is
intended to close, not a permanent product limitation.

> **New user?** Start with [setup.md](setup.md) for the currently implemented workflow: installation, repository enrollment, provider configuration, isolated agents, scheduling, recovery, and human review. See [Target architecture / in development](#target-architecture--in-development) and [Universal Agent Enrollment & Transport Protocol](#universal-agent-enrollment--transport-protocol) for the architecture being developed. These roadmap sections are requirements, not instructions for an already-implemented attach service.

## Currently implemented

The control-plane foundations already include Go authority, objective/task
scheduling, exclusive claims, assignment generations, isolated branches/worktrees,
supervised execution, heartbeats, validation, evidence/result collection, review
gates, blocked-assignment recovery, configured provider adapters, and parallel
eligible task scheduling.

Repository authority enrollment and local session registration exist today. They
are not yet a universal agent enrollment handshake, capability registry, or
reconnect protocol. Built-in providers currently come from a closed registry;
trusted custom CLI adapters and HTTP planning configuration offer additional
integration paths. Workers are primarily invoked as managed subprocesses. An
approved task selects a configured capability; the scheduler does not yet match
tasks against a dynamic registry of independently connected available agents.

### Start the managed Planner workflow

Start the Planner, choose a repository, and describe the objective:

```bash
go run ./cmd/planner --workspace ~/Projects/my-project
```

Install/authenticate at least one supported CLI: Codex, Claude Code, or Gemini CLI.
The Planner lists installed supported provider CLIs and lets you select a Planner and
one or more worker providers. Selecting a built-in requires no provider JSON.
On first run it asks you to authorize mandatory project validators and offers to
save that policy, including an optional explicitly authorized worktree preparation
command. Fresh worktrees exclude local dependencies such as `node_modules`;
projects using them may need preparation before workers and validation can run.
See [setup.md](setup.md).
The Planner inspects the repository, proposes a task/dependency plan and asks for
explicit approval. The Go controller then handles eligible acquisition,
sessions, claim tokens, assignment generations, isolated worktrees, concurrent
workers, heartbeats, validation and result collection.

```bash
go run ./cmd/planner --list-providers
go run ./cmd/planner --workspace ~/Projects/my-project --planner codex --workers codex,gemini --max-workers 3
```

See [Connecting the Planner and Worker Agents](setup.md#connecting-the-planner-and-worker-agents)
for provider configuration, managed process launch, parallel tasks, and a first
multi-agent run. Existing independent chats/processes cannot yet dynamically
enroll/attach to this managed workflow; the enrollment milestone below addresses
that missing capability.

Detection inspects trusted absolute PATH entries without running agents or
checking authentication. Project-local executables and relative PATH entries
are excluded. Built-in argument vectors are defined in Go; model output cannot
invent an executable. Custom providers remain an explicit advanced option.

| Role | Responsibility |
| --- | --- |
| Planner | Conversational/reasoning layer; recon, proposals, questions, consolidated report |
| Go ORCHESTRATOR | Deterministic enrollment, admission, ownership, scheduling and enforcement |
| Workers | Implement assigned work in isolated worktrees; Go commits checked built-in worker output |
| User | Authorize genuine decisions, review results, retain integration/merge/deployment authority |

Model output never grants ownership or permission. Validation selects configured
checks rather than executing model-generated commands. Results include exact
branch HEADs and evidence. `AWAITING_REVIEW` becomes `APPROVED` or
`CHANGES_REQUESTED` only through a human decision; approval never merges or deploys.

Managed validators may regenerate tracked reports or create temporary files.
After strict clean worker-output review, the controller captures HEAD, verifies
unchanged history/branch after each check, and restores only validation-phase
filesystem side effects before accepting evidence. Failed checks still fail;
history changes and unsafe cleanup block the assignment. See the
[validation boundary contract](docs/planner-agent.md#execution-dependency-and-recovery-policy).

Restart the same command to select a saved objective. Its provider selections
are restored and the existing policy digest still applies. Existing assignments are
not duplicated or stolen after heartbeat expiry. Independent pending work can
continue while another task is blocked. Divergent prerequisite branches require
human integration; the Planner does not merge them automatically.

This remains local orchestration, not a sandbox. Live provider/multi-agent
acceptance remains necessary. See [Planner architecture and limitations](docs/planner-agent.md).

## Target architecture / in development

This section defines the product architecture to build. It does not claim that
the enrollment registry, external attachment, or all transports exist today.
A user must ultimately be able to make multiple compatible agents available,
enroll/select one planning-capable agent as Planner, enroll others as Workers,
run eligible independent assignments simultaneously, and receive consolidated
results through the Planner. Go remains the sole coordination authority and
the human retains sensitive authorization and final review/merge/deployment.

```text
                         HUMAN
                           |
                           v
                       PLANNER
                    (any enrolled
                    capable agent)
                           |
                           v
                +---------------------+
                |   GO ORCHESTRATOR   |
                |   SOLE AUTHORITY    |
                +----------+----------+
                           |
              +------------+------------+
              |            |            |
              v            v            v
           WORKER A      WORKER B      WORKER C
          any agent      any agent      any agent
              |            |            |
          Worktree A    Worktree B    Worktree C
              |            |            |
              +------ simultaneous -----+
```

Workers communicate assignments, progress and results with ORCHESTRATOR through
the common contract. They do not coordinate ownership directly with the Planner.
The Planner's position in this diagram never makes it the coordination authority.

### Terminology

| Term | Meaning |
| --- | --- |
| Agent | An enrolled execution/reasoning participant. |
| Provider | The implementation, vendor or runtime behind an agent. |
| Planner | A protocol role for reasoning, task proposals and presenting results. |
| Worker | A protocol role for executing bounded assignments. |
| ORCHESTRATOR | The authoritative Go control plane. |
| Transport | The communication mechanism between agent and ORCHESTRATOR. |
| Adapter | A translation layer for a provider/runtime. |
| Enrollment | Authoritative admission of an agent session by Go ORCHESTRATOR. |

Provider and role are separate concepts. Current CLI flags/legacy role labels
are implementation interfaces, not the definition of the future role protocol.

### Roles and authority

| Participant | Responsibility and boundary |
| --- | --- |
| Human | Chooses/authorizes objectives; selects or approves the Planner where required; approves sensitive policy/recovery decisions; reviews final evidence; retains merge/deployment authority. |
| Planner | Performs recon through the supported runtime, reasons about the objective, asks/handles clarifications, proposes task decomposition, and presents consolidated results/review information. It does not admit agents, schedule work, issue claims, or authorize execution/recovery. |
| Go ORCHESTRATOR | Admits/enrolls agents; maintains authoritative agent/session state; assigns roles; owns objectives/tasks, scheduling and task ownership; issues/fences generations and claims/tokens; creates isolated branches/worktrees; controls execution admission and supervision; monitors heartbeats/liveness; handles disconnect/recovery/handoff; runs validation; records authoritative evidence/results; controls recovery state under required human authorization. |
| Workers | Advertise capabilities, receive bounded assignments, operate only within assigned scope/worktree, and report progress/blockers/results. They cannot self-assign, override another claim, merge/deploy, or grant themselves authority. |

### Planner selection is a role decision

When several enrolled agents support planning, the user can select which one
becomes Planner for an objective/session. These are **target examples**, not
current built-in adapter or attachment instructions:

| Run | Selected Planner | Enrolled Workers |
| --- | --- | --- |
| Example 1 | Kimi | OpenCode, Codex, Claude, Antigravity |
| Example 2 | Codex | Kimi, Gemini |

An enrolled capable agent can become Planner without changing Go coordination
authority. Roles are protocol roles, never hard-coded provider identities.

### Parallel assignment invariant

```text
ONE TASK -> ONE CURRENT OWNER -> ONE ASSIGNMENT GENERATION -> ONE CLAIM
         -> ONE BRANCH -> ONE ISOLATED WORKTREE -> ONE BOUNDED SCOPE
```

Different independent tasks may execute simultaneously, for example:

| Task | Worker agent | Branch | Worktree |
| --- | --- | --- | --- |
| Task A | OpenCode | Branch A | Worktree A |
| Task B | Kimi | Branch B | Worktree B |
| Task C | Codex | Branch C | Worktree C |

All three may run simultaneously when dependencies permit, scopes do not
conflict, worker capabilities match, and concurrency policy permits. Running
concurrently never gives two agents write authority over the same bounded
task/scope. Reassignment must fence the previous generation and preserve its
history; an expired heartbeat or disconnect alone does not authorize takeover.

## Universal Agent Enrollment & Transport Protocol

**Roadmap / in development.** This milestone extends the existing control-plane
foundations with dynamic agent admission and a common logical contract. The
following lifecycle, messages and states are design requirements, not a claim
that matching wire formats, endpoints or CLI commands have shipped.

### Required enrollment lifecycle

```text
DISCOVER
  -> ENROLL
  -> REGISTER IDENTITY + CAPABILITIES
  -> ASSIGN ROLE (PLANNER or WORKER)
  -> READY / AVAILABLE
  -> HEARTBEAT
  -> ASSIGNMENT
  -> EXECUTION
  -> PROGRESS / BLOCKER / RESULT
  -> VALIDATION
  -> COMPLETE / HANDOFF / DISCONNECT
```

This is a logical lifecycle, not the current persisted state enum. Heartbeats
continue while available and during execution; progress may repeat and blockers
require explicit handling. Planner assignments produce proposals/review
information rather than worker implementation authority. Agent acceptance and
reported completion never replace Go admission, validation or result authority.

### Transport-neutral contract

ORCHESTRATOR must interact with agents through one logical contract rather than
provider-specific lifecycle logic. An enrolled agent must communicate:

| Contract element | Information/interaction |
| --- | --- |
| Protocol version | Version compatibility established during enrollment. |
| Agent/session identity | Stable agent identity and the specific admitted session; reconnects must be fenced. |
| Provider/runtime metadata | Implementation metadata, separate from authority and role. |
| Capabilities and supported roles | Advertised abilities and whether Planner/Worker roles are supported; Go admits their use. |
| Availability and liveness | Availability plus heartbeat/liveness information. |
| Assignment acceptance | Acceptance of the exact Go-issued bounded assignment and generation. |
| Progress and blocker | Updates tied to the authorized assignment/session. |
| Result | Output/evidence references bound to the assignment for Go validation/collection. |
| Disconnect/handoff state | Explicit lifecycle information subject to Go ownership/recovery rules. |

Adapters may translate a provider CLI/API into this contract. They must not own
scheduling, grant roles/claims, accept stale results, or bypass authorization and
validation semantics. A provider name must not need to be added to scheduler
logic before a compatible agent can participate.

The transport must not define orchestration semantics. Potential mechanisms
include a locally managed CLI subprocess, an independently started local agent
process, IPC/local socket, stdio bridge, HTTP/RPC bridge, and future remote
transport. **These are possible implementations, not a list of shipped
transports.** Existing managed CLI invocation and HTTP planning requests are
current adapter mechanisms, not a completed universal transport protocol. Future
remote transport does not turn local filesystem locks into distributed consensus.

### Dynamic enrollment end goal

An agent should be able to start independently, discover/connect to ORCHESTRATOR,
request enrollment, register identity/capabilities/supported roles, and become
AVAILABLE after authoritative admission. No provider-specific scheduler change
should be required. An available planning-capable agent can be selected as
Planner under the required human authorization; available Workers receive only
Go-issued assignments. Discovery, a capability advertisement, or a connection
alone grants no permission. This missing dynamic enrollment/attachment capability
is what this milestone develops.

### Phase 1 — Protocol and identity

Define protocol versioning, agent identity, session identity, capabilities,
supported roles, and the enrollment state model. Separate runtime metadata from
authority and define admission/compatibility checks.

### Phase 2 — Registry and liveness

Build the authoritative Go agent registry with READY/BUSY/BLOCKED/DISCONNECTED
lifecycle, heartbeats, reconnect/session fencing, and stale-agent detection.
Liveness observations must not silently release ownership or authorize recovery.

### Phase 3 — Transport abstraction

Define a common transport interface and preserve existing managed CLI adapters
behind it. Transport/adapter code translates messages; Go retains all scheduling,
ownership, admission and result-authority semantics.

### Phase 4 — External/local attachment

Allow independently started compatible local agents to connect through an
explicit registration/enrollment handshake, advertise capabilities, and exchange
assignment/result messages. Define disconnect/handoff behavior through the same
authority contract; do not equate opening a connection with being enrolled.

### Phase 5 — Planner enrollment/selection

Let the user select any planning-capable enrolled agent for an objective/session.
Supply recon/objective context through the protocol, handle clarifications and
proposals, and present consolidated results. Keep the Planner non-authoritative
and preserve human plan/policy/recovery authorization.

### Phase 6 — Heterogeneous parallel workers

Match eligible tasks to available enrolled worker capabilities. Support
simultaneous independent assignments, per-agent liveness/progress, and generation
fencing across disconnect/reconnect. Preserve the one-owner/claim/worktree/scope
invariant and existing validation/evidence gates.

### Phase 7 — Operational UX

Expose connected agents, roles, capabilities, availability, current assignments,
progress and blockers. Provide clear attach/enroll instructions and distinguish
discovered, connected, admitted and available states without requiring manual
coordination-ledger edits.

### Phase 8 — Real-world acceptance

Demonstrate one objective with one Planner and at least two different worker
agent implementations. Acceptance must show simultaneous independent execution,
separate isolated worktrees, validation/result collection, and human review,
without manual coordination-state edits. Preserve evidence of actual overlap
and exact validated results; unit tests or two sequential workers alone do not
establish this milestone's acceptance.

## Non-negotiable invariants

These requirements govern the target protocol and its integration with existing
controls; documenting them does not mean universal enrollment is implemented.

1. Go ORCHESTRATOR is the sole coordination authority.
2. Agent/provider identity never grants authority.
3. Planner is the reasoning interface, not the scheduler or coordination authority.
4. Workers cannot self-authorize.
5. Silence never implies permission.
6. One task has one current owner, assignment generation, claim, branch, isolated
   worktree and bounded scope; previous attempts remain historical evidence.
7. Parallelism occurs across independent bounded assignments.
8. Stale/disconnected agents cannot complete newer generations.
9. Provider-specific adapters cannot bypass the universal protocol.
10. The human retains final merge/deployment authority.
11. Local orchestration is not an OS/container security sandbox.
12. Local filesystem locking is not distributed multi-host consensus.

## Advanced / Manual interfaces

The sections below document lower-level Go operations and the legacy Python
scratch generator. Normal onboarding uses the Planner above. The Python graph
does not participate in managed ownership and now refuses writes into Git
repositories/managed worktrees. Its scratch validation only checks artifact
writing, not generated application correctness. Historical milestone notes are
retained below as implementation history; use the current-state and target
sections above to assess today's functionality and the next milestone.

## Universal agent activation protocol

This existing portable prompt/manual interface predates universal dynamic enrollment.
Registering a local session is not attaching a live process to managed Planner
scheduling. The prompt's generic authority language does not supersede Go as
the sole authority in managed runs; workers must not directly edit ledgers or
self-acquire managed tasks. The new enrollment protocol must preserve that
boundary rather than treating prompt instructions as an admission handshake.

`agent_activation_prompt.md` defines the portable coordination protocol for
coding agents joining a project. The authority role is **ORCHESTRATOR**, never a
hard-coded person's name. On receipt of the prompt an agent enters
`ACTIVATING` and immediately performs safe discovery and registration rather
than waiting for another start signal.

For a newly coordinated project, the first agent safely bootstraps the
authoritative `multi-agent-orchestrator/` coordination directory under the
exclusive bootstrap lock. The shared files are `agentmeeting.md` for live
presence/claims/heartbeats, `coordination.md` for ORCHESTRATOR decisions and
cross-agent control, and `resolution.md` for append-only completion evidence.
Existing files and coordination mechanisms are joined rather than overwritten.

The lifecycle is `ACTIVATING → ACTIVE → WORKING → COMPLETE`, with `BLOCKED`
and `HANDOFF` recorded explicitly. Product edits begin only after the agent
has reconstructed current state and acquired an eligible task and exclusive
scope. The prompt defines the portable protocol, while the Go runtime enforces the core local coordination lifecycle: registration, locked claims, overlapping-scope rejection, isolated worktrees, lifecycle transitions, execution, validation-backed completion, handoff, rollback, and claim release.

### Go activation runtime

The first runtime slice is available through the Go CLI:

```bash
go run ./cmd/orchestrator --workspace /path/to/project --agent Codex --role backend
```

It resolves the target Git repository, acquires the shared bootstrap lock,
creates only missing coordination ledgers, preserves existing history,
registers a unique session in `agentmeeting.md`, and releases the lock. Activation stops at `ACTIVE`; the same Go runtime also provides locked scope claims, heartbeat refreshes, lifecycle transitions, worktree isolation, execution, validation, handoff, rollback, and completion. Examples:

```bash
go run ./cmd/orchestrator --workspace /path/to/project --session SESSION --feature F1 --scope internal/foo.go --token CLAIM_TOKEN
go run ./cmd/orchestrator --workspace /path/to/project --session SESSION --heartbeat
go run ./cmd/orchestrator --workspace /path/to/project --session SESSION --state WORKING
```

Coordination writes use a shared atomic lock and rewrite the live ledger through a temporary file/rename operation. Exact and hierarchical overlapping scopes are rejected. Claims require registered sessions, handoffs require registered targets, and orchestrator-managed worktrees can be rolled back safely after startup failures.


## Setup

The following Python setup/examples are for the **legacy scratch generator**,
not the managed Go workflow or the universal enrollment milestone. They do not
enroll live agents into Go scheduling and cannot write into managed Git projects.

```bash
python3 -m venv .venv
source .venv/bin/activate
python -m pip install -e '.[dev,groq,gemini]'
cp .env.example .env
```

## First test with Gemini and Groq keys

Start with Gemini for all agents, then optionally move frontend generation to Groq. Update your existing `.env` with this configuration, replacing the key placeholders locally:

```dotenv
GEMINI_API_KEY=your-gemini-key
GROQ_API_KEY=your-groq-key

MODEL_PROVIDER=gemini
MODEL_NAME=gemini-2.5-flash-lite
TEMPERATURE=0.1
MAX_TOKENS=2048
PLANNER_MAX_TOKENS=800

LLM_MAX_ATTEMPTS=4
LLM_RETRY_BASE_SECONDS=5
LLM_RETRY_MAX_SECONDS=60
```

For this first test, comment out previous role-specific `MODEL_*` settings and custom `MODEL_CLASS`, `MODEL_BASE_URL`, `MODEL_API_KEY`, and `MODEL_KWARGS` values. Keep only one active assignment per variable. Exported shell variables override `.env` values.

Gemini lists Flash-Lite with free-tier usage, subject to your project's eligibility and quotas. Check your account before running: [Gemini pricing](https://ai.google.dev/gemini-api/docs/pricing) and [rate limits](https://ai.google.dev/gemini-api/docs/rate-limits).

Run the offline regression tests first; these use mocked models and consume no API quota:

```bash
source .venv/bin/activate
python -m pip install -e '.[dev,groq,gemini]'
python -m pytest -q
```

Then run a small live generation in a separate workspace:

```bash
python -m orchestrator.cli \
  --workspace /tmp/orchestrator-smoke-test \
  --prompt "Build a tiny static counter in exactly one index.html file with inline CSS and JavaScript. Include increment and reset buttons. No backend, dependencies, or other files."
```

Expect an overall `success` status and `/tmp/orchestrator-smoke-test/index.html`. Preview it with:

```bash
python -m http.server 8080 --directory /tmp/orchestrator-smoke-test
```

Open `http://localhost:8080` and test both buttons. Stop the preview server with Ctrl+C. A successful orchestration confirms generation and file writing; it does not guarantee the generated application works correctly.

After the Gemini test succeeds, optionally add these role overrides to `.env` and rerun the command to test Groq:

```dotenv
FRONTEND_MODEL_PROVIDER=groq
FRONTEND_MODEL_NAME=openai/gpt-oss-20b
FRONTEND_MAX_TOKENS=2048
```

The `openai/` model prefix does not change the provider: this configuration uses your Groq API key. Check [Groq's current models](https://console.groq.com/docs/models) and [account limits](https://console.groq.com/docs/rate-limits) for availability. Older Llama examples may require Enterprise access.

If your account has a 1,000 output-token/minute cap, the example 2,048-token request may exceed it. Reduce `FRONTEND_MAX_TOKENS`, keep tasks small, and allow quota windows to reset between runs. Reasoning tokens can consume the output budget. A truncated response may require a smaller task or a model/account with a larger available budget.

### Troubleshooting the first run

- **Missing integration:** install the provider extra in the same virtual environment used to run the CLI.
- **401/403:** check the provider key and model permissions.
- **Model not found:** select a model ID available to your account and update the matching `MODEL_NAME` variable.
- **429/resource exhausted:** check whether the limit is per minute or per day. Retries help transient limits but cannot restore exhausted daily quota.
- **Truncated output:** simplify the requested file or raise its token budget within your account limits.
- **Unexpected provider:** remove stale role overrides and check exported shell variables.

### Getting more models or capacity

One provider key can access multiple permitted models; change the model ID rather than creating a key for each model. Extra keys do not multiply quota: Gemini limits apply per project, and Groq limits apply per organization. See their rate-limit links above.

For another hosted option, [OpenRouter offers free models with limited daily usage](https://openrouter.ai/pricing). Install the OpenAI integration and use the compatible-provider configuration below with OpenRouter's endpoint and a free model ID from its catalog. Local Ollama is another option when you have suitable hardware. Higher cloud quotas generally require waiting for resets or upgrading your plan.

## Configure models in `.env`

These settings configure only the legacy Python scratch generator. Its model
factory and role prefixes do not define the Go agent registry, protocol roles,
or transport contract. For today's Go provider configuration, use [setup.md](setup.md).

Edit `.env` in the repository root. If you already have one, update it directly; you do not need to copy `.env.example` again. Save your changes and start a new orchestrator run to use them.

### Choose the default model

Set the provider, the exact model ID, and its API key. These defaults apply to every agent without a role override:

```dotenv
MODEL_PROVIDER=gemini
MODEL_NAME=gemini-2.5-flash-lite
GEMINI_API_KEY=your-gemini-key
```

To use another model from the same provider, replace `MODEL_NAME` with that provider's model ID. There is no model registry to update in Python. Keep one active assignment per variable rather than appending duplicate settings.

The following examples match the integrations supported by the factory. Model IDs are examples; choose an ID available to your account or local server.

| Provider | `MODEL_PROVIDER` | Example `MODEL_NAME` | Credential in `.env` | Install integration |
| --- | --- | --- | --- | --- |
| Groq | `groq` | `openai/gpt-oss-20b` | `GROQ_API_KEY` | `pip install -e '.[groq]'` |
| Gemini | `gemini` | `gemini-2.5-flash` | `GEMINI_API_KEY` (or `GOOGLE_API_KEY`) | `pip install -e '.[gemini]'` |
| OpenAI | `openai` | `gpt-4o-mini` | `OPENAI_API_KEY` | `pip install -e '.[openai]'` |
| DeepSeek | `deepseek` | `deepseek-chat` | `DEEPSEEK_API_KEY` | `pip install -e '.[deepseek]'` |
| Anthropic | `anthropic` | `claude-sonnet-4-5` | `ANTHROPIC_API_KEY` | `pip install -e '.[anthropic]'` |
| Ollama | `ollama` | `qwen2.5-coder:7b` | None for a local server | `pip install -e '.[ollama]'` |

Install each integration once in your active virtual environment. You can install several together, for example `pip install -e '.[groq,gemini,deepseek]'`. Subsequent model changes only require editing `.env`.

### Assign different models to agents

For example, use Groq by default, Gemini for planning, and DeepSeek for backend work:

```dotenv
MODEL_PROVIDER=groq
MODEL_NAME=openai/gpt-oss-20b
GROQ_API_KEY=your-groq-key

PLANNER_MODEL_PROVIDER=gemini
PLANNER_MODEL_NAME=gemini-2.5-flash
GEMINI_API_KEY=your-gemini-key

BACKEND_MODEL_PROVIDER=deepseek
BACKEND_MODEL_NAME=deepseek-chat
DEEPSEEK_API_KEY=your-deepseek-key

# Frontend inherits the global Groq settings.
# To override it too, uncomment and set both:
# FRONTEND_MODEL_PROVIDER=groq
# FRONTEND_MODEL_NAME=another-model-id
```

Each setting resolves independently: role-specific value, then global value, then legacy fallback/default. When switching a role's provider, set its model name too so it does not inherit a model ID belonging to another provider. Remove or comment out both role settings to restore the global defaults.

Supported role prefixes are `PLANNER`, `FRONTEND`, `BACKEND`, and `VALIDATION`. Validation currently writes files and checks errors without invoking a model, so `VALIDATION_MODEL_*` settings have no effect on the current graph.

Blank variables behave as unset. Exported process environment variables take precedence over `.env`; update or unset them if edits to `.env` appear to be ignored.

### Set token limits and temperature

Optional global parameters also accept role overrides:

```dotenv
TEMPERATURE=0.1
MAX_TOKENS=4096

PLANNER_TEMPERATURE=0
PLANNER_MAX_TOKENS=800
FRONTEND_MAX_TOKENS=4096
BACKEND_MAX_TOKENS=4096
```

Omitted parameters use the integration's defaults. Only set parameters supported by your selected model. The factory maps `MAX_TOKENS` to Ollama's `num_predict`. A token cap limits an individual response, not total usage per minute across agents. Choose budgets appropriate to your account; reasoning tokens may also consume the response budget.

### Use a local Ollama model

Install the Ollama integration from the table, make the model available on your running Ollama server, and use:

```dotenv
MODEL_PROVIDER=ollama
MODEL_NAME=qwen2.5-coder:7b
OLLAMA_BASE_URL=http://localhost:11434
```

Remove any previous role overrides if all agents should use this local model.

### Add a new OpenAI-compatible provider

Install the compatible client with `pip install -e '.[openai]'`, then configure the service entirely in `.env`:

```dotenv
MODEL_PROVIDER=myprovider
MODEL_NAME=your-provider-model-id
MODEL_BASE_URL=https://your-provider.example/v1
MYPROVIDER_API_KEY=your-provider-key
```

Replace the URL and model ID with the service's values. The credential variable uses the uppercase provider label plus `_API_KEY`. For a role-only configuration, use `BACKEND_MODEL_PROVIDER`, `BACKEND_MODEL_NAME`, and `BACKEND_MODEL_BASE_URL`, alongside the provider's API key. `BACKEND_MODEL_API_KEY` can override the credential for that role.

A global `MODEL_BASE_URL` is inherited by every role. When mixing endpoints, prefer role-specific endpoint settings or provider-specific variables such as `OPENAI_BASE_URL`.

### Add another LangChain integration

For a provider with a different API, install its LangChain integration package and configure the importable chat class:

```dotenv
MODEL_PROVIDER=myprovider
MODEL_CLASS=my_langchain_package:ChatCustom
MODEL_NAME=your-model-id
MYPROVIDER_API_KEY=your-provider-key
MODEL_KWARGS='{"custom_option": true}'
PLANNER_MODEL_KWARGS='{"custom_option": false}'
```

The package, class, and option names above are placeholders: substitute the values required by the integration. `MODEL_CLASS` uses `module:ClassName` syntax. Global JSON kwargs merge with role JSON kwargs, with role values winning. Explicit model, key, endpoint, token, and temperature settings override JSON kwargs. Integrations with nonstandard constructor parameters can receive them through `MODEL_KWARGS`.

A global `MODEL_CLASS` applies to every role; use a role-specific setting such as `BACKEND_MODEL_CLASS` when only that agent needs the custom integration. No repository code changes are needed.

See [.env.example](.env.example) for the complete configuration reference, including retry settings and legacy fallbacks.

## Run

Put `blueprint.md`, `README.md`, or `specs.md` in the target workspace, then run:

```bash
python -m orchestrator.cli --workspace /path/to/project
# Or provide a direct request:
python -m orchestrator.cli --workspace /path/to/project --prompt 'Build a stopwatch'
```

The CLI exits nonzero on planning, generation, or file-write failures. Workers return artifacts without writing files; validation writes them in the state-selected workspace. Successful files may still be written when another task fails. The orchestrator can overwrite existing target files.

## Output and retries

No tool calling or structured-output API is required. The planner emits compact `ARCHITECTURE`, `FRONTEND`, `BACKEND`, and `END` sections, with `filepath | description` tasks. Workers emit `### FILE: relative/path` and a fenced code block. Parsers also accept plain JSON objects, and a single fenced/raw file when its expected path is known. Unsafe, mismatched, duplicate paths and unclosed fences are rejected. Provider-reported token truncation fails explicitly.

The planner requests at most 180 words and seven files. This reduces output usage but cannot guarantee a particular model's behavior or account quota. Reasoning can consume output tokens. A per-request `MAX_TOKENS` setting does not enforce tokens per minute across parallel workers or other processes. Configure smaller tasks and suitable token budgets for constrained accounts.

Transient provider failures use bounded exponential backoff with jitter and `Retry-After` support. `LLM_MAX_ATTEMPTS`, `LLM_RETRY_BASE_SECONDS`, and `LLM_RETRY_MAX_SECONDS` support role overrides. Exhausted retries become state errors. Authentication, parsing, and configuration failures fail immediately. Built-in remote integrations have SDK retries disabled to avoid multiplying attempts.

## Tests

```bash
pytest -q
```

Tests use deterministic chat mocks, exercise actual LangGraph fan-out/fan-in, and never call a live model. Generated code quality and live provider availability require separate testing.

Integration references: [LangChain provider integrations](https://reference.langchain.com/python/integrations/overview), [Ollama](https://reference.langchain.com/python/langchain-ollama/langchain_ollama).


### Controlled Go workflow

The controlled start flow composes the runtime primitives into one guarded start operation:

```bash
go run ./cmd/orchestrator --start --workspace /path/to/project --agent Codex --role backend --feature feature-name --scope internal/feature --token claim-token --base main
```

The command activates and registers the agent, acquires its exclusive scope,
creates an isolated feature worktree, records the branch/worktree and claim in
`agentmeeting.md`, then transitions the session to `WORKING`. Claim or
worktree failure moves the session to `BLOCKED`; worktree failure also
releases the acquired claim. Implementation execution and validation-backed completion are available as explicit later gates or through the end-to-end `--run` command.


### Controlled completion

Controlled completion closes the runtime lifecycle with validation-backed completion:

```bash
go run ./cmd/orchestrator --complete --workspace /path/to/project --worktree /path/to/worktree --session SESSION --feature feature-name --token claim-token --validate-program go --validate-arg test --validate-arg ./...
```

The orchestrator executes validation inside the agent worktree. After success,
it acquires the coordination lock, verifies the session is `WORKING` and owns
`--token`, then appends and syncs successful evidence in `resolution.md`. One
atomic replacement of `agentmeeting.md` publishes `COMPLETE` together with removal
of **all claims owned by that session**, across features/scopes. Claims belonging
to other sessions are preserved, even when tokens match. The existing `--token`
flag remains required for ownership verification and the evidence reference; it
is not a restriction on which of that session's claims are released.

Failed validation records `BLOCKED` evidence, transitions the session to `BLOCKED`,
and retains **all** its claims. Recording, locking and publication errors are
returned rather than reported as successful completion. Direct `--state COMPLETE`
transitions reject outstanding claims; completed sessions cannot acquire claims
or receive handoffs. Individual `--release --session SESSION --token TOKEN`
remains token-specific and does not complete the session.

**Storage limitation:** resolution evidence and the live ledger are separate
files, not a cross-file transaction. A ledger-write failure or crash after evidence
was appended can leave successful validation/completion evidence without a live
COMPLETE transition. The prior session state and claims are retained if publication
fails; inspect `agentmeeting.md` and the returned error before treating evidence
as completion. Resolve the storage/lock problem and retry from the valid live
state; evidence can be duplicated on retry. Do not erase append-only history or
steal a stale lock automatically. State and claim removal share one ledger rename,
so there is no intermediate COMPLETE-with-claims ledger from this operation.
Filesystem crash durability and writers bypassing the coordination lock remain
outside this guarantee.


### Controlled agent execution

Controlled execution can launch an implementation command inside an already-owned agent
worktree without giving it access to a different working directory:

```bash
go run ./cmd/orchestrator --execute --workspace /path/to/project --worktree /path/to/worktree --session SESSION --agent-program codex --agent-arg exec --agent-arg TASK --timeout 30m
```

The child process receives `ORCHESTRATOR_SESSION_ID` and
`ORCHESTRATOR_WORKTREE`, runs with the assigned worktree as its working
directory, and has a bounded timeout. Execution evidence is appended to
`resolution.md`. A non-zero exit or timeout moves the session to `BLOCKED`;
successful execution refreshes its heartbeat but does not mark the feature
complete. Validation-backed completion remains a separate gate.


### End-to-end Go run

The end-to-end run composes the guarded lifecycle into one command while preserving the
individual gates:

```bash
go run ./cmd/orchestrator --run --workspace /path/to/project --agent Codex --role backend --feature feature-name --scope internal/feature --token claim-token --base main --agent-program codex --agent-arg exec --agent-arg TASK --validate-program go --validate-arg test --validate-arg ./... --timeout 30m
```

The sequence is `activate → claim → isolated worktree → WORKING → execute →
validate → resolution evidence → [COMPLETE + release all session claims]`.
The bracketed changes are published in one locked ledger replacement. This also
releases claims added by the implementation command. Execution failure or timeout
stops before validation and leaves the session `BLOCKED`; validation failure
likewise retains all its claims. Completion storage errors propagate through
`--run`, subject to the cross-file evidence limitation above.

### Command execution safety

Go runtime execution and validation use structured executable/argument vectors rather than shell command strings. The orchestrator does not invoke `sh -c` for user-supplied commands; shell metacharacters in arguments are therefore not interpreted by the orchestrator. Use repeated `--agent-arg` and `--validate-arg` flags to preserve argument boundaries.


## Historical milestone status — 2026-10-20

The core local Go orchestration milestone was **completed** with this guarded lifecycle. This historical completion does not mean the Universal Agent Enrollment & Transport Protocol is complete:

`activate → register → claim → isolated worktree → WORKING → execute → validate → resolution evidence → [COMPLETE + release all session claims]`

Implemented and CI-covered capabilities include bootstrap locking, atomic coordination writes, registered-session claim integrity, hierarchical scope-conflict rejection, heartbeats and lifecycle transitions, handoff validation, isolated Git worktrees, guarded startup rollback, validation-backed completion, end-to-end execution, and structured executable/argument invocation without orchestrator-side shell interpretation.

The repository CI runs both `go test ./...` and `python -m pytest -q`. The final core-security PR (#14) passed CI at exact head `f2039d78fbf9cc0fad065b99e63881ab4a894f06` before merge.

### Boundary of this milestone

This milestone provided **local/shared-filesystem orchestration**, not a distributed multi-host lock service or OS/container security sandbox. A child process runs with the operating-system permissions of the orchestrator process, and the coordination lock is filesystem-local. Richer durable structured state and configured provider adapters were delivered in subsequent work described above. Distributed coordination and stronger process/environment isolation remain separate from the completed local-runtime milestone and from transport-neutral agent enrollment.


## Single-authority orchestration foundation

This subsequent local-runtime milestone established **ORCHESTRATOR as the sole managed
coordination authority for an enrolled project**, while the user remains the
final integration/merge authority. Repository instructions continue to govern
build/test/project conventions, but conflicting ownership protocols must be
reconciled before managed work starts.

New enrollments now create versioned structured local state at
`multi-agent-orchestrator/state.json`. The store records canonical repository
identity, authority/schema generation, sessions, claims, and events using a
locked atomic replacement. Existing operational Markdown state is never treated
as unowned: initialization fails closed and requires explicit migration when
legacy session/claim rows are present.

Activation and Markdown coordination use the same coordination lock, removing
the prior lost-update window between registration and claim rewrites. Markdown
remains readable history/projection during migration; it is not a source from
which workers may grant themselves permission.

This section records the historical Phase A/Phase B foundation. Objective
scheduling, managed worker spawning, supervision, blocked recovery and result
collection were implemented subsequently. Universal agent enrollment remains
the new milestone; arbitrary third-party adapters, product PR publication,
distributed locking and OS/container confinement are not implied by the
foundation. See the historical design in
[docs/single-authority-orchestration.md](docs/single-authority-orchestration.md).


## Authority enrollment preflight

Managed `--start` and `--run` workflows now require explicit authority enrollment.
Inspect a target repository first:

```bash
go run ./cmd/orchestrator --workspace /path/to/project --preflight
```

A clean repository reports `NATIVE`. Project-local agent claim tools or
coordination directives are reported as `MIGRATION_REQUIRED`; discovery
inspects files but does not execute unknown project tooling.

A native repository can then be explicitly enrolled with decision provenance:

```bash
go run ./cmd/orchestrator \
  --workspace /path/to/project \
  --enroll \
  --enrollment-disposition NATIVE \
  --enrollment-provenance "owner approved ORCHESTRATOR authority"
```

Enrollment provenance is persisted in structured authority state. Repositories
with unresolved findings cannot be falsely enrolled as native. The legacy
reconciliation/migration path described below followed this native preflight
foundation; unsupported writers and unresolved authority still fail closed.


## Guarded execution and supervision

Managed execution now verifies that the session is `WORKING`, has active
claims, and is bound to the exact worktree before launching a worker. While the
worker is running the controller periodically rechecks ownership and refreshes
the session heartbeat. If ownership is lost, heartbeat publication fails, the
command times out, or the worker exits unsuccessfully, the result is not
accepted as successful work.

These guards fence result publication and controller actions; they are not an
OS/container sandbox and cannot physically prevent an unrestricted same-user
process from writing elsewhere. Failed workers remain visible through the
existing `BLOCKED` lifecycle/evidence path rather than silently surrendering
their ownership.


## Objective planning and automatic task acquisition

The controller can now accept a bounded JSON objective plan containing stable task IDs,
features, repository-relative scopes, priorities and dependencies. It validates the
dependency graph, rejects invalid or cyclic plans, determines which tasks are eligible,
filters tasks that conflict with currently represented structured-state claims, and
selects the highest-priority eligible task deterministically.

Use `--acquire-next --plan-file <plan.json>` to let the controller choose the task.
The controller generates the claim token instead of requiring the worker/user to invent
one, then delegates the selected task through the normal enrolled start/worktree
lifecycle. `--completed-tasks a,b` supplies validated prerequisite task IDs for this
foundation.

This paragraph describes the original acquisition foundation: it did not yet
persist objective/task completion or atomically reserve eligible tasks across
controllers. The durable scheduler described next superseded that limitation;
it is not a missing capability of the current managed workflow.


### Durable objective scheduler

Objective plans are now materialized into structured state. Acquisition runs under the
structured-state lock, persists the objective and task definitions, selects one eligible
task, advances its attempt counter, creates an assignment generation, and reserves that
task before the normal agent/worktree start path runs. A second controller therefore
cannot legitimately reserve the same task from the same state store.

Task completion is generation-fenced: the objective ID, task ID, controller-generated
token, bound session, and assignment generation must all match before a task can become
COMPLETE. When every persisted task for an objective is complete, the objective becomes
COMPLETE as well. Failed start attempts are retained as BLOCKED rather than silently
forgotten.

The structured-state transaction cannot include Git worktree creation or Markdown ledger
writes. Acquisition therefore uses a durable reservation followed by those external
operations and records a BLOCKED rollback state if startup fails. This is restart-visible
intent, not a claim that filesystem/Git operations are database-atomic.


## Result collection and human review gate

Validated task outputs can be collected into structured state with `--collect-result`.
Collection resolves the exact Git `HEAD` from the assigned worktree and binds the
result to the objective, task, session and assignment generation. Validation evidence,
artifacts, blockers and known limitations are preserved with the result. Stale sessions
or generations cannot publish a replacement result.

When every task in an objective has a collected result, the objective becomes
`AWAITING_REVIEW`, not merged or deployed. `--review-package --objective-id <id>`
returns the deterministic task/result package for inspection. The human integration
authority records either `APPROVED` or `CHANGES_REQUESTED` with
`--review-decision` and `--review-actor`.

Approval is an auditable review decision only. It does not merge branches, push code,
deploy releases, or grant agents merge authority. Those integration actions remain
outside this gate and require the user's explicit authorization.


## Legacy authority reconciliation

Repositories with pre-existing coordination tools must be reconciled before the
ORCHESTRATOR is treated as the single ownership authority. `--reconcile-legacy`
inventories recognized legacy coordination writers, reads their historical claim
ledger when the format is understood, fingerprints the observed authority state,
and reports ambiguity instead of treating absence from structured state as release.

`--migrate-legacy` is fail-closed. Coordination instructions that still direct
agents to another writer are ambiguous and must first be explicitly fenced or
updated. Active/WORKING legacy claims are never auto-released or silently imported;
they must be quiesced or handed off explicitly. Historical claims require
`--acknowledge-legacy-history` plus provenance before a fresh structured authority
state is created with enrollment `MIGRATED`.

Migration records a fingerprint of the reconciled legacy state in authority
provenance. It does not delete legacy tools or rewrite project instructions
automatically. Unsupported writers and uncertain ownership remain blockers. This
preserves the rule: silence, missing Go claims, or an expired heartbeat never imply
permission to take another agent's work.
