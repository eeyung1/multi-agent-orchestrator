# Multi-Agent Orchestrator — Public Setup Guide

This guide is for anyone who receives the repository link and wants to coordinate multiple coding agents on a local Git project.

The Go ORCHESTRATOR is the coordination authority. It manages enrollment, sessions, exclusive scopes, isolated Git worktrees, durable objective/task reservations, guarded execution, result evidence, and the human review gate. It does **not** automatically merge or deploy code.

> Current boundary: this is a local orchestration system. It is not an OS/container security sandbox and its locks are not a distributed multi-machine lock service.

## 1. Requirements

Install:

- Git
- Go 1.24 or newer
- one or more coding-agent CLIs you want to run (Codex, Claude Code, OpenCode, etc.)
- access to the target Git repository

Verify:

```bash
git --version
go version
```

### If your Go version is too old

This project requires **Go 1.24 or newer**. On some systems, especially machines
using an older distro/package-manager Go installation, commands such as
`go test ./...` or `go run ./cmd/orchestrator` may fail with messages similar to:

```text
go: downloading go1.24...
go: toolchain not available
```

or:

```text
go: download go1.24: toolchain not available
```

First check what Go executable and version your shell is actually using:

```bash
which go
go version
go env GOTOOLCHAIN
```

If `go version` is below 1.24, install a current Go release from the official Go
downloads page for your operating system. After installation, open a new terminal
and verify that the new executable appears first on `PATH`:

```bash
which go
go version
```

The version must report Go 1.24 or newer before continuing.

#### Linux without sudo: install Go in your home directory

If you cannot replace the system Go installation, install a current Go toolchain
under your own home directory. Download the appropriate archive from the official
Go downloads page, then use the downloaded filename below:

```bash
rm -rf ~/.local/go
mkdir -p ~/.local

tar -C ~/.local -xzf ~/Downloads/go1.24.x.linux-amd64.tar.gz

echo 'export PATH="$HOME/.local/go/bin:$PATH"' >> ~/.bashrc
export PATH="$HOME/.local/go/bin:$PATH"

which go
go version
```

Replace `go1.24.x.linux-amd64.tar.gz` with the actual current archive you
downloaded and use the architecture appropriate for your machine.

If `which go` still shows something such as `/usr/bin/go`, your old system Go
is still taking precedence. Ensure the new Go bin directory is at the **front** of
`PATH`, then start a new shell:

```bash
source ~/.bashrc
hash -r
which go
go version
```

#### Toolchain auto-download setting

With a sufficiently recent bootstrap Go installation, Go can select newer
toolchains automatically. Check:

```bash
go env GOTOOLCHAIN
```

The normal setting is `auto` (or a version with `+auto`). If the user previously
disabled automatic toolchain selection, restore it:

```bash
go env -w GOTOOLCHAIN=auto
```

However, changing `GOTOOLCHAIN` is **not a substitute for fixing an obsolete or
broken Go installation**. If automatic download still reports `toolchain not
available`, install Go 1.24+ directly and confirm `go version` before retrying.

Finally:

```bash
cd ~/Projects/multi-agent-orchestrator
go test ./...
```

Do not continue with repository enrollment until these tests pass.

## 2. Recommended folder layout

Keep the orchestrator beside target projects, not inside them:

```text
~/Projects/
├── multi-agent-orchestrator/
├── project-a/
└── project-b/
```

Create the parent folder and clone:

```bash
mkdir -p ~/Projects
cd ~/Projects

git clone https://github.com/eeyung1/multi-agent-orchestrator.git
git clone https://github.com/OWNER/PROJECT.git my-project
```

Verify the orchestrator itself:

```bash
cd ~/Projects/multi-agent-orchestrator
go test ./...
```

All tests should pass before using it on another repository.

## 3. Preflight the target repository

Always inspect coordination authority before enrolling a project:

```bash
cd ~/Projects/multi-agent-orchestrator

go run ./cmd/orchestrator \
  --workspace ~/Projects/my-project \
  --preflight
```

### New repository with no competing coordinator

If preflight reports a native repository with no competing coordination mechanism, enroll it:

```bash
go run ./cmd/orchestrator \
  --workspace ~/Projects/my-project \
  --enroll \
  --enrollment-disposition NATIVE \
  --enrollment-provenance "Repository owner authorized ORCHESTRATOR coordination"
```

### Repository that already has agent coordination

Do **not** blindly enroll it. Reconcile first:

```bash
go run ./cmd/orchestrator \
  --workspace ~/Projects/my-project \
  --reconcile-legacy
```

If `ambiguous` contains entries, resolve them before migration. Active/WORKING legacy claims must not be silently stolen or released.

After the repository owner has fenced the old ownership writer, reconciled instructions, and confirmed historical ownership, migration can be authorized:

```bash
go run ./cmd/orchestrator \
  --workspace ~/Projects/my-project \
  --migrate-legacy \
  --acknowledge-legacy-history \
  --enrollment-provenance "Owner approved migration after legacy coordination reconciliation"
```

Do not use this acknowledgement to bypass unresolved ownership.

## 4. What appears in the target repository

The runtime creates local coordination state such as:

```text
my-project/
├── multi-agent-orchestrator/
│   ├── state.json
│   ├── agentmeeting.md
│   ├── coordination.md
│   └── resolution.md
└── .agent-worktrees/
```

`state.json` is the structured authority state. The Markdown files are readable coordination/evidence ledgers.

These runtime directories normally should **not** be committed to the target project's product branch. Add appropriate ignore rules for your repository if needed.

## 5. Two ways to coordinate work

### A. Direct/manual assignment

For a small test, start independent assignments directly:

```bash
go run ./cmd/orchestrator \
  --start \
  --workspace ~/Projects/my-project \
  --agent Codex \
  --role coding-agent \
  --feature backend-auth \
  --scope backend/auth \
  --token codex-backend-auth \
  --base main
```

The runtime returns the session, claim, branch and isolated worktree.

Start another agent with a non-overlapping scope:

```bash
go run ./cmd/orchestrator \
  --start \
  --workspace ~/Projects/my-project \
  --agent Claude-Code \
  --role coding-agent \
  --feature frontend-login \
  --scope frontend/login \
  --token claude-frontend-login \
  --base main
```

Invariant:

```text
one feature → one owner → one branch → one isolated worktree
```

Never point multiple agents at the same worktree.

### B. Objective-plan acquisition

For less manual assignment, define an objective plan. Example `plan.json`:

```json
{
  "ID": "auth-hardening",
  "Objective": "Harden authentication and document the behavior",
  "Tasks": [
    {
      "ID": "backend-auth",
      "Feature": "backend-auth",
      "Scope": "backend/auth",
      "Priority": 20
    },
    {
      "ID": "auth-tests",
      "Feature": "auth-tests",
      "Scope": "tests/auth",
      "DependsOn": ["backend-auth"],
      "Priority": 10
    },
    {
      "ID": "auth-docs",
      "Feature": "auth-docs",
      "Scope": "docs/auth",
      "Priority": 5
    }
  ]
}
```

Acquire the next eligible task:

```bash
go run ./cmd/orchestrator \
  --workspace ~/Projects/my-project \
  --acquire-next \
  --plan-file ./plan.json \
  --agent Codex \
  --role coding-agent \
  --base main
```

Run the same command for additional workers. The scheduler persists the objective/tasks, reserves eligible work under structured state, prevents legitimate duplicate acquisition, creates assignment generations, and delegates the normal isolated-worktree start.

Save the returned JSON. It contains the task, session, token, branch, worktree, attempt and assignment generation needed for later result collection.

## 6. Give each agent its exact worktree

The portable protocol is:

```text
~/Projects/multi-agent-orchestrator/agent_activation_prompt.md
```

Give each worker:

- the activation protocol;
- its returned session ID;
- assigned task/feature;
- exact scope;
- exact worktree;
- acceptance criteria.

Open/run that coding agent **inside the returned worktree**, not the primary checkout.

An agent must never infer ownership from silence or edit another assignment's scope.

## 7. Heartbeats and guarded execution

Refresh a long-running session:

```bash
go run ./cmd/orchestrator \
  --workspace ~/Projects/my-project \
  --session SESSION_ID \
  --heartbeat
```

The runtime can launch a CLI agent inside an already-owned worktree:

```bash
go run ./cmd/orchestrator \
  --execute \
  --workspace ~/Projects/my-project \
  --worktree WORKTREE_PATH \
  --session SESSION_ID \
  --agent-program AGENT_EXECUTABLE \
  --agent-arg ARGUMENT \
  --timeout 30m
```

Use repeatable `--agent-arg` flags rather than combining an untrusted command into a shell string.

Execution is ownership/lifecycle guarded, but it is not an OS sandbox.

## 8. Validate normal/manual assignments

For a directly started assignment, use the target project's real validation command.

Go example:

```bash
go run ./cmd/orchestrator \
  --complete \
  --workspace ~/Projects/my-project \
  --worktree WORKTREE_PATH \
  --session SESSION_ID \
  --feature FEATURE_NAME \
  --token CLAIM_TOKEN \
  --validate-program go \
  --validate-arg test \
  --validate-arg ./...
```

Node example:

```bash
go run ./cmd/orchestrator \
  --complete \
  --workspace ~/Projects/my-project \
  --worktree WORKTREE_PATH \
  --session SESSION_ID \
  --feature FEATURE_NAME \
  --token CLAIM_TOKEN \
  --validate-program npm \
  --validate-arg test
```

Failed validation moves the work to BLOCKED and retains ownership. Do not give that scope to another worker merely because validation failed.

## 9. Collect objective-task results

For objective-plan assignments, preserve the scheduler output. After the agent has implemented, tested, committed and pushed its branch, collect the exact result:

```bash
go run ./cmd/orchestrator \
  --workspace ~/Projects/my-project \
  --collect-result \
  --objective-id auth-hardening \
  --task-id backend-auth \
  --session SESSION_ID \
  --token CLAIM_TOKEN \
  --assignment-generation ASSIGNMENT_GENERATION \
  --worktree WORKTREE_PATH \
  --result-branch BRANCH_NAME \
  --validation-evidence "go test ./...: PASS" \
  --artifact "PR #123" \
  --limitation "No production deployment performed"
```

Repeat `--artifact`, `--blocker`, and `--limitation` when needed.

Result collection resolves the exact Git HEAD from the assigned worktree and rejects stale session/generation publication.

## 10. Human review gate

When every task in an objective has a collected result, the objective becomes:

```text
AWAITING_REVIEW
```

Build the consolidated review package:

```bash
go run ./cmd/orchestrator \
  --workspace ~/Projects/my-project \
  --review-package \
  --objective-id auth-hardening
```

The repository owner/reviewer can then record:

```bash
go run ./cmd/orchestrator \
  --workspace ~/Projects/my-project \
  --objective-id auth-hardening \
  --review-decision APPROVED \
  --review-actor "Repository Owner"
```

or:

```bash
go run ./cmd/orchestrator \
  --workspace ~/Projects/my-project \
  --objective-id auth-hardening \
  --review-decision CHANGES_REQUESTED \
  --review-actor "Repository Owner"
```

**APPROVED does not merge, push, or deploy anything.** Integration remains under the repository's authorized human workflow.

## 11. GitHub integration

Each agent should normally:

```text
inspect → implement → test → commit → push feature branch → open focused PR
```

Then:

```text
CI → verify exact PR head → human review → authorized merge
```

Do not treat mergeability or an old successful CI run as proof that the current PR head passed.

## 12. End-to-end execution option

After understanding the lifecycle, a direct assignment can be composed into one guarded command:

```bash
go run ./cmd/orchestrator \
  --run \
  --workspace ~/Projects/my-project \
  --agent Codex \
  --role coding-agent \
  --feature feature-name \
  --scope path/to/scope \
  --token unique-token \
  --base main \
  --agent-program AGENT_EXECUTABLE \
  --agent-arg ARGUMENT \
  --validate-program go \
  --validate-arg test \
  --validate-arg ./... \
  --timeout 30m
```

The exact agent executable/arguments depend on the coding tool installed by the user.

## 13. Recommended first test

Use a disposable/small repository with two independent tasks.

Confirm:

1. preflight/enrollment succeeds;
2. two agents receive different sessions/worktrees;
3. overlapping scopes are rejected;
4. both agents can work independently;
5. validation/evidence is recorded;
6. objective results reach `AWAITING_REVIEW`;
7. the review package contains exact heads and evidence;
8. nothing merges without the authorized human.

Only then increase the number of simultaneous workers.

## 14. Troubleshooting

**"repository is not enrolled"** — run preflight and enroll/migrate correctly.

**legacy authority is ambiguous** — do not bypass it. Reconcile project instructions, active claims and old ownership writers first.

**overlapping scope/claim rejected** — another assignment owns that path or a parent/child path. Choose independent work or perform an authorized handoff.

**session is BLOCKED** — inspect validation/execution evidence and repair the existing assignment. Do not steal its scope.

**wrong worktree** — use the exact worktree returned by the scheduler/start command.

**state/runtime folders show as untracked** — do not accidentally commit them to product code; add repository-appropriate ignore rules.

**Go tests fail in this repository** — stop and resolve that before using the runtime for production work.

## 15. Updating the orchestrator

Because one clone can serve multiple target projects:

```bash
cd ~/Projects/multi-agent-orchestrator
git switch main
git pull --ff-only origin main
go test ./...
```

Do not overwrite a target project's structured state simply because the orchestrator code was updated.

## 16. What this release does and does not guarantee

It provides:

- single acknowledged local coordination authority;
- exclusive hierarchical scopes;
- isolated Git branches/worktrees;
- durable objective/task reservations;
- assignment attempt/generation fencing;
- guarded execution and heartbeats;
- result/evidence collection;
- consolidated human review;
- human-controlled integration.

It does not provide:

- automatic discovery of already-open coding-agent windows;
- a container/VM security boundary;
- distributed locking across different machines;
- automatic GitHub merge/deployment;
- proof that an agent's code is correct merely because it completed.

## Core mental model

```text
You define the objective
        ↓
ORCHESTRATOR owns coordination
        ↓
agents receive isolated eligible work
        ↓
agents implement + validate + push
        ↓
results/evidence are collected
        ↓
AWAITING_REVIEW
        ↓
you approve or request changes
        ↓
authorized GitHub integration
```

The activation prompt teaches the protocol. The Go runtime enforces local coordination. Git/GitHub remain the integration boundary.
