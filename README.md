# LangGraph Multi-Agent Software Development Orchestrator

A planner delegates single-file tasks to parallel frontend/backend agents. Validation writes artifacts through workspace boundary checks and reports failures. Validation does not run generated tests or assess code correctness.

## Universal agent activation protocol

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
scope. The prompt is currently a protocol document; hard enforcement should
live in orchestration/claim tooling rather than relying solely on model
compliance.

### Go activation runtime

The first runtime slice is available through the Go CLI:

```bash
go run ./cmd/orchestrator --workspace /path/to/project --agent Codex --role backend
```

It resolves the target Git repository, acquires the shared bootstrap lock,
creates only missing coordination ledgers, preserves existing history,
registers a unique session in `agentmeeting.md`, and releases the lock. This first phase stops at `ACTIVE`. The next Go runtime slice adds locked
scope claims, heartbeat refreshes and explicit lifecycle transitions. Examples:

```bash
go run ./cmd/orchestrator --workspace /path/to/project --session SESSION --feature F1 --scope internal/foo.go --token CLAIM_TOKEN
go run ./cmd/orchestrator --workspace /path/to/project --session SESSION --heartbeat
go run ./cmd/orchestrator --workspace /path/to/project --session SESSION --state WORKING
```

Coordination writes use a shared atomic lock and rewrite the live ledger through
a temporary file/rename operation. Conflicting exact scopes are rejected.
Worktree creation, hierarchical/overlapping scope detection, claim release,
handoff and agent execution remain subsequent runtime phases.


## Setup

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

Phase 4 composes the runtime primitives into one guarded start operation:

```bash
go run ./cmd/orchestrator --start --workspace /path/to/project --agent Codex --role backend --feature feature-name --scope internal/feature --token claim-token --base main
```

The command activates and registers the agent, acquires its exclusive scope,
creates an isolated feature worktree, records the branch/worktree and claim in
`agentmeeting.md`, then transitions the session to `WORKING`. Claim or
worktree failure moves the session to `BLOCKED`; worktree failure also
releases the acquired claim. Autonomous implementation and validation remain
separate later phases.


### Controlled completion

Phase 5 closes the runtime lifecycle with validation-backed completion:

```bash
go run ./cmd/orchestrator --complete --workspace /path/to/project --worktree /path/to/worktree --session SESSION --feature feature-name --token claim-token --validate "go test ./..."
```

The orchestrator executes the requested validation inside the agent worktree and
records the outcome in `resolution.md`. A successful validation transitions
the session to `COMPLETE` and releases its claim. A failed validation records
the evidence, transitions the session to `BLOCKED`, and deliberately retains
the claim so another agent cannot take ownership of failing work prematurely.


### Controlled agent execution

Phase 6 can launch an implementation command inside an already-owned agent
worktree without giving it access to a different working directory:

```bash
go run ./cmd/orchestrator --execute --workspace /path/to/project --worktree /path/to/worktree --session SESSION --agent-command "your-agent-command" --timeout 30m
```

The child process receives `ORCHESTRATOR_SESSION_ID` and
`ORCHESTRATOR_WORKTREE`, runs with the assigned worktree as its working
directory, and has a bounded timeout. Execution evidence is appended to
`resolution.md`. A non-zero exit or timeout moves the session to `BLOCKED`;
successful execution refreshes its heartbeat but does not mark the feature
complete. Validation-backed completion remains a separate gate.
