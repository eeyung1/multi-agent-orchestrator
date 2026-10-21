# Single-authority orchestration

## Authority boundary

An enrolled project has one managed coordination authority: **ORCHESTRATOR**.
The user authorizes the objective and remains the final integration/merge
authority. ORCHESTRATOR owns scheduling, claims, isolated worktrees, lifecycle,
handoffs, and evidence within that authorization. Workers execute assignments
and return results; they cannot self-authorize by editing project files.

Repository instructions remain inputs for build, test, architecture, and
contribution conventions. Conflicting ownership or coordination instructions
must be reconciled before enrollment. Silence, an empty ledger, an expired
heartbeat, or filesystem access never grants permission.

## Structured-state foundation

Phase B introduces `multi-agent-orchestrator/state.json` as a versioned,
transactionally replaced local structured-state foundation. It records canonical
repository identity, protocol/schema version, authority generation, enrollment
state, sessions, claims, and durable events. Updates occur under one state lock
and publish through fsync plus atomic rename. Failed updates do not advance the
authority generation.

The store deliberately fails closed when a repository has pre-existing
operational Markdown session/claim rows but no structured state. Such a project
requires explicit migration/reconciliation; an empty new database must never
make existing ownership disappear.

Scopes admitted to structured state are canonical repository-relative paths.
Absolute paths and traversal outside the repository are rejected.

The Markdown files remain readable projections/history during migration. They
are not a permission source and must not be edited by workers to grant
themselves ownership.

## Legacy coordination

Enrollment must classify legacy coordination as native, compatible adapter,
authorized migration, or blocked. Unknown tooling is inspected, not executed
merely for discovery. Project tooling is not deleted simply because it contains
coordination behavior; non-coordination functions should be preserved.

A later enrollment/preflight phase will inventory and reconcile third-party
claim stores such as project-specific coordination scripts. This foundation
only detects existing ORCHESTRATOR Markdown operational state and refuses an
ambiguous empty-state migration.

## Concurrency and migration boundary

Activation and Markdown coordination now use the same coordination lock,
eliminating the previous lost-registration race caused by separate bootstrap
and coordination locks writing `agentmeeting.md`. Structured state has its own
transaction lock because it is a separate atomic resource.

The long-term migration is to make structured state authoritative for all
lifecycle mutations and render Markdown from it. Until that migration is
complete, callers must not claim that Markdown and structured state form one
cross-file transaction.

## Deferred work

This phase intentionally does not implement objective decomposition, autonomous
task scheduling, worker spawning, process supervision/recovery, PR publication,
deployment, distributed locking, or an OS/container sandbox. Those capabilities
build on this authority/state foundation and require their own review gates.
