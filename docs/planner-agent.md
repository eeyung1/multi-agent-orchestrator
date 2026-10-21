# Planner Agent

## Dynamic activation

Run `go run ./cmd/planner --workspace /path/to/project`. Without configured custom
providers, select an installed Planner and a worker pool interactively. Explicit
selection is available with `--planner codex` (`--provider` is an alias),
`--workers codex,gemini`, and `--max-workers 3`. `--list-providers` reports local
availability without invoking any provider, probing authentication, or installing
anything. Missing/unavailable/unknown providers produce actionable errors.

`internal/providers` owns the closed registry: stable IDs, display names,
executable names, Planner/Worker roles, fixed argv construction and output
normalization. It inspects only absolute PATH entries, rejects executables or
symlinks controlled by the target/launching Git repository, and pins the resolved
absolute executable. Built-in children receive a PATH without relative or
project-local interpreter directories. JSON cannot redefine a built-in's argv.

| ID | Planner | Worker |
| --- | --- | --- |
| `codex` | `exec`, read-only sandbox, final-message file | `exec`, workspace-write sandbox, approvals never escalated |
| `claude` | print JSON, built-in tools disabled, explicit empty MCP set | print text, acceptEdits, explicit empty MCP set |
| `gemini` | headless JSON, plan approval mode, extensions disabled | headless text, auto_edit plus sandbox, extensions disabled |

There are no default approval/sandbox bypass flags. Codex's adapter was checked
against installed `codex exec --help` (acceptance target 0.163.0). Claude/Gemini
permission policy can block a headless command, and Gemini's worker sandbox may
require a supported sandbox backend. Unsupported flags, authentication failures
and denials fail explicitly; there is no automatic downgrade to unrestricted
execution. CLI presence alone is not proof of compatible version/authentication.

References: [Codex CLI](https://developers.openai.com/codex/cli/reference/),
[Claude CLI](https://code.claude.com/docs/en/cli-reference),
[Gemini headless mode](https://geminicli.com/docs/cli/headless/).

If trusted policy is missing, the CLI proposes familiar checks from project file
names or asks for a validation executable plus a JSON argument array. It never
executes discovered scripts during bootstrap. The user explicitly authorizes the
mandatory checks and may save a policy-only file in their user configuration.
There is no diff-only or no-tests fallback. Scope limits, required gates,
concurrency, timeouts and custom adapters remain trusted policy.

Selected worker IDs become capability IDs advertised to the model. Go admission
permits only the selected capabilities; the model cannot introduce a shell or
unselected provider. Maximum concurrency remains a controller policy limit.

Objectives record selected built-in Planner/worker IDs in structured state. On
resume the CLI restores those IDs without another selection prompt, resolves
their executable bindings, and runs the existing full configuration/plan digest
checks. A changed executable binding, provider pool, policy, or instruction set
does not grant new permission or release existing ownership. Legacy/custom
objectives retain their original explicit configuration contract.

Built-in workers leave product edits uncommitted. After their process exits, the
Go controller verifies generation/ownership, branch/base, all committed/staged/
unstaged/untracked paths, then stages explicit owned paths and makes a local
feature commit. This avoids granting a worker write access to shared Git control
metadata merely to commit. Exact-head validation and evidence/review gates still
apply. An unowned change blocks collection and keeps the claim. No integration,
push, merge or deployment is added.

## Recon and implementation boundary

Before PR #28, the Go pipeline required a user-supplied JSON plan, manual
acquisition, a separately launched worker, and manual result collection. Go
already provided enrollment, durable task reservations, assignment generations,
exclusive claims, isolated worktrees, execution heartbeats, and review records.
`go test ./...` passed before implementation.

PR #28 supplied objective/repository interaction, bounded recon,
proposal-only inference, rich task metadata, deterministic admission, concurrent
dispatch, validation/result binding, restart reconstruction, and a single report.

The Python `planning_node` reasons without writing files and could be adapted as
a proposal source. Its existing single-file frontend/backend schema omits
acceptance/dependency/capability information, so this implementation uses a typed
Go provider interface instead. The old Python graph is not invoked. Its writer
now refuses Git repositories, linked worktrees, structured-state ancestors and
coordination metadata, retaining scratch-only generation.

## Ownership of responsibilities

```text
User objective + explicit operational authorization
  -> Planner: inspect context, propose tasks, ask real decision questions
  -> Go controller: admit the exact plan and trusted configuration
  -> Scheduler: reserve eligible tasks, sessions, claims, generations, worktrees
  -> Provider-neutral workers: implement local owned changes
  -> Go controller: supervise, commit built-in output, validate, collect evidence
  -> Planner: consolidated AWAITING_REVIEW package
  -> Human: APPROVED / CHANGES_REQUESTED
  -> Separate human-controlled integration
```

`internal/planner` has no ownership, review-decision or merge API. The CLI's
human interaction is outside the model response. Unknown response fields are
rejected. A response cannot supply a grant, claim token, review approval or
executable validation command.

The Go controller persists the authorized plan/configuration/instruction digests,
human provenance and base SHA. It uses existing scheduler operations rather than
creating a second ownership system. Task definitions carry description,
acceptance criteria, configured validator IDs and a configured worker capability.

Admission rejects duplicate IDs/features, unknown/cyclic dependencies,
noncanonical/escaping/symlink scopes, controller metadata, unauthorized scopes,
unknown validators/capabilities, unordered overlapping tasks, conflicting claims,
existing task/review ownership and overlapping uncommitted work. A root task
scope is refused because it includes coordination metadata; configuration `.`
allows proposing bounded product scopes throughout the repository.

Trusted configuration can declare `required_validators`. Every task must include
them; a model cannot replace required project checks with a cheaper allowed check.

Project instructions are supplied to planning and workers, checked against the
actual worktree before dispatch, and fingerprinted during execution/restart.
Semantic interpretation of arbitrary prose still requires model reasoning and
human plan review; this is not a formal verifier of every project convention.

## Advanced / Custom Providers

Trusted personal configuration supplies executables, argument vectors,
validators, policy limits and model credentials by environment-variable name.
The CLI never implicitly loads executable policy from the target repository.

Explicit trusted custom configuration supports either:

- a chat-completion HTTP endpoint with `model`, `messages`, and one textual
  `choices[].message.content` JSON proposal; or
- a configured `program` and `args` receiving the protocol and recon request as
  JSON on stdin and returning only proposal JSON on stdout.

For a hosted endpoint configure `api_key_env`, never a key value. Provider
responses are bounded to 1 MiB and two minutes. Provider HTTP error bodies are
not echoed. CLI adapters run in a temporary directory, away from product files.
That directory choice is not a security sandbox.

Proposal shape:

```json
{
  "plan": {
    "id": "proposed-id",
    "objective": "The exact user request",
    "tasks": [{
      "id": "verify-module",
      "feature": "verify-module",
      "description": "Implement the requested module verification",
      "scope": "internal/module",
      "depends_on": [],
      "priority": 10,
      "acceptance_criteria": ["The requested behavior has meaningful tests"],
      "validation": ["go-tests"],
      "capability": "coding"
    }]
  },
  "questions": []
}
```

Questions pause planning for real user input. Execution still needs explicit
confirmation of the displayed plan and configured commands. Empty input/EOF is
never approval. Objective IDs are generated internally by the CLI.

Workers receive a JSON assignment on stdin; each literal `{prompt}` occurrence
in configured arguments is replaced with that JSON as one argument. No shell
interpretation is added. The assignment includes the exact task, objective,
acceptance, instructions, scope, branch/worktree, session, token, generation and
collected prerequisite results. Reports omit tokens and generations.

Custom adapters remain separate from the built-in registry. An explicit
`--config` can retain HTTP/CLI Planner and arbitrary capability-to-worker vectors;
project-local configuration requires a separate trust confirmation, and a default
user-config symlink into a project is rejected. A custom adapter cannot register
new built-in definitions or override a built-in's fixed arguments. Custom
workers retain the PR #28 commit contract; built-ins use controller-owned commits.
Automated tests use fake providers and CLI workers; live provider acceptance has
not been performed.

## Execution, dependency and recovery policy

Independent tasks run up to `max_workers`. Ownership and generation are checked
before dispatch, during heartbeats, after child exit and before collection.
Validation uses the same supervised execution path. Output must be committed,
clean, descend from its assigned base and change only owned paths. Evidence is
bound to the exact validated HEAD, and all session claims are released through
the existing completion operation.

A dependent assignment can branch from a collected prerequisite HEAD that
contains all of its prerequisites. Divergent prerequisite heads require a human
integration decision. The Planner never combines them by automatically merging.

`repair_attempts` (0–3) permits bounded repairs of this controller's own
just-finished failed worker/validation command in the same owned assignment.
It does not permit recovery of an absent writer or takeover after a heartbeat
expires. Exhaustion retains claims, records BLOCKED and continues independent
work. Ownership, instruction or authorization changes stop repair.

Restart reads stored plans, tasks, assignments, results and reviews. It never
relaunches an existing ASSIGNED/BLOCKED task. It can finish collection after a
previously recorded validation and published COMPLETE session. Other interrupted
assignments require writer/checkpoint reconciliation. Configuration or instruction
drift requires a fresh decision rather than silently accepting different policy.

State reservations, Git operations and Markdown completion are separate durable
steps, not a cross-file transaction. A leftover state/coordination lock is never
removed on an age-based heuristic. Storage/lock failures remain explicit blockers.

## Review and limitations

The consolidated package lists objective, task statuses, branches, exact heads,
checks/evidence, local artifacts, blockers, incomplete tasks, decisions and next
action. All collected results produce AWAITING_REVIEW. Only explicit human input
records APPROVED/CHANGES_REQUESTED; those decisions do not merge or deploy.

This release does not automatically push branches, create product PRs, obtain
credentials, recover unknown writers, perform destructive cleanup, migrate
competing authorities or provide multi-host coordination. Each task currently
has one bounded directory/file scope. Recon is bounded, excludes common secret
files and dependencies, and cannot guarantee that every sensitive file has been
identified. Review context before approving transmission to an external provider.

Workers inherit local process permissions/environment (built-ins filter PATH).
Provider-specific sandbox/permission modes do not turn Go into an OS sandbox.
POSIX managed process
groups are terminated on cancellation/exit; Windows cancellation is best effort.
Detached processes and unrestricted same-user writes remain outside a security
sandbox. Do not use this system as containment for hostile code.

Real acceptance still requires an actual repository/objective, compatible live
provider/worker configuration, meaningful project validators, interruption and
blocker exercises, exact-head review, and confirmation that the user never had
to assign individual tokens, sessions, scopes or worktrees. Unit tests alone do
not establish production readiness.
