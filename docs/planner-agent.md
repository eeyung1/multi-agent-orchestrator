# Planner Agent

## Recon and implementation boundary

Before this feature, the Go pipeline required a user-supplied JSON plan, manual
acquisition, a separately launched worker, and manual result collection. Go
already provided enrollment, durable task reservations, assignment generations,
exclusive claims, isolated worktrees, execution heartbeats, and review records.
`go test ./...` passed before implementation.

Missing connections were objective/repository interaction, bounded recon,
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
  -> Provider-neutral workers: implement and commit local owned changes
  -> Go controller: supervise, validate, bind evidence, collect results
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

## Provider contracts

Trusted personal configuration supplies executables, argument vectors,
validators, policy limits and model credentials by environment-variable name.
The CLI never implicitly loads executable policy from the target repository.

The proposal provider supports either:

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

Codex is an example configuration, not a runtime dependency. An adapter for any
compatible CLI can use the same contract. Automated tests use fake providers and
CLI workers; live Codex/provider acceptance has not been performed.

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

Workers inherit local process permissions/environment. POSIX managed process
groups are terminated on cancellation/exit; Windows cancellation is best effort.
Detached processes and unrestricted same-user writes remain outside a security
sandbox. Do not use this system as containment for hostile code.

Real acceptance still requires an actual repository/objective, compatible live
provider/worker configuration, meaningful project validators, interruption and
blocker exercises, exact-head review, and confirmation that the user never had
to assign individual tokens, sessions, scopes or worktrees. Unit tests alone do
not establish production readiness.
