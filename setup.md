# Multi-Agent Orchestrator Setup Guide

This guide walks you through using the Multi-Agent Orchestrator with coding agents such as Codex, Claude Code, OpenCode, Antigravity, or similar tools.

The goal is simple: multiple agents can work on one project at the same time without editing the same scope or sharing one unsafe working directory.

> **Important:** Do not clone the orchestrator inside every project. Keep one copy of the orchestrator beside your project repositories and point it at whichever project you want to coordinate.

---

## 1. Prerequisites

Before starting, make sure you have:

- Git installed.
- Go installed.
- Access to the project repository you want to work on.
- The coding-agent tools you intend to use.
- A terminal for running the orchestrator.

Confirm Git and Go:

```bash
git --version
go version
```

---

## 2. Create a workspace directory

Choose one parent directory for your repositories.

Example:

```bash
mkdir -p ~/Projects
cd ~/Projects
```

Your final layout should look like:

```text
~/Projects/
├── multi-agent-orchestrator/
└── my-project/
```

---

## 3. Clone the orchestrator

Clone the orchestrator once:

```bash
cd ~/Projects
git clone https://github.com/eeyung1/multi-agent-orchestrator.git
```

Enter it and verify its tests:

```bash
cd ~/Projects/multi-agent-orchestrator
go test ./...
```

You can reuse this installation for multiple projects.

---

## 4. Clone the project you want to work on

Example:

```bash
cd ~/Projects
git clone https://github.com/OWNER/PROJECT.git my-project
```

Replace `OWNER/PROJECT` with the real GitHub repository.

Do not place the orchestrator repository inside `my-project`.

---

## 5. Decide which agents will work on the project

For example:

```text
Codex
Claude Code
OpenCode
Antigravity
```

Each agent will eventually receive:

1. a unique session;
2. one feature;
3. an exclusive scope;
4. its own Git branch;
5. its own isolated worktree.

A useful rule is:

```text
One feature → one owner → one branch → one isolated worktree
```

---

## 6. Read the activation protocol

The portable agent instructions are in:

```text
~/Projects/multi-agent-orchestrator/agent_activation_prompt.md
```

Give this protocol to every coding agent participating in the project.

The prompt teaches the agent how to behave. The Go runtime provides the enforceable local coordination operations.

---

## 7. Plan the work before starting agents

Break the project work into independent features and scopes.

Example:

| Agent | Feature | Scope |
| --- | --- | --- |
| Codex | backend-auth | backend/auth |
| Claude Code | frontend-login | frontend/login |
| OpenCode | auth-tests | tests/auth |
| Antigravity | auth-docs | docs/auth |

Avoid assigning overlapping scopes.

For example, if Codex owns:

```text
backend/auth
```

another agent should not claim:

```text
backend/auth/login.go
```

The runtime's hierarchical scope check will reject that overlap.

---

## 8. Start the first agent

Run the orchestrator from its own repository:

```bash
cd ~/Projects/multi-agent-orchestrator
```

For Codex:

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

The start operation performs the guarded setup:

```text
discover project
      ↓
bootstrap/join coordination
      ↓
register session
      ↓
claim scope
      ↓
create isolated branch/worktree
      ↓
record assignment
      ↓
WORKING
```

The command prints values similar to:

```text
agent=Codex-...
state=WORKING
claim=codex-backend-auth
branch=agent/backend-auth
worktree=/home/user/Projects/my-project/.agent-worktrees/backend-auth
```

Save the returned **session ID** and **worktree path**.

Do not invent a session ID.

---

## 9. Start the other agents

Repeat the same process with a different feature, scope, and token.

Claude Code example:

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

OpenCode example:

```bash
go run ./cmd/orchestrator \
  --start \
  --workspace ~/Projects/my-project \
  --agent OpenCode \
  --role coding-agent \
  --feature auth-tests \
  --scope tests/auth \
  --token opencode-auth-tests \
  --base main
```

Antigravity example:

```bash
go run ./cmd/orchestrator \
  --start \
  --workspace ~/Projects/my-project \
  --agent Antigravity \
  --role coding-agent \
  --feature auth-docs \
  --scope docs/auth \
  --token antigravity-auth-docs \
  --base main
```

Each successful start should return a different session and worktree.

---

## 10. Confirm the coordination state

After the first activation, the target project contains:

```text
my-project/
├── multi-agent-orchestrator/
│   ├── agentmeeting.md
│   ├── coordination.md
│   └── resolution.md
└── .agent-worktrees/
    ├── backend-auth/
    ├── frontend-login/
    ├── auth-tests/
    └── auth-docs/
```

The three coordination files have different jobs:

### `agentmeeting.md`

Tracks live operational state such as:

- active sessions;
- lifecycle state;
- heartbeat;
- feature;
- branch/worktree;
- claim.

### `coordination.md`

Tracks ORCHESTRATOR-level information such as:

- decisions;
- permissions;
- blockers;
- conflicts;
- handovers.

### `resolution.md`

Tracks completion evidence such as:

- execution results;
- validation results;
- completed work;
- resolution evidence.

---

## 11. Point each coding agent at its own worktree

Do not point all agents at the main checkout.

Use the exact worktree path returned by `--start`.

Example:

```text
Codex
→ ~/Projects/my-project/.agent-worktrees/backend-auth

Claude Code
→ ~/Projects/my-project/.agent-worktrees/frontend-login

OpenCode
→ ~/Projects/my-project/.agent-worktrees/auth-tests

Antigravity
→ ~/Projects/my-project/.agent-worktrees/auth-docs
```

Open each coding tool in its assigned worktree.

Then provide that agent with:

1. `agent_activation_prompt.md`;
2. its feature name;
3. its claimed scope;
4. its session ID;
5. its claim token;
6. the concrete acceptance criteria.

The agent should not edit another agent's worktree or claimed scope.

---

## 12. Keep agents alive with heartbeats

For long-running work, refresh the session heartbeat.

Example:

```bash
cd ~/Projects/multi-agent-orchestrator

go run ./cmd/orchestrator \
  --workspace ~/Projects/my-project \
  --session CODEX_SESSION_ID \
  --heartbeat
```

Replace `CODEX_SESSION_ID` with the real ID returned by the orchestrator.

The agent protocol calls for regular heartbeat updates during long-running work.

---

## 13. Check status during development

Open:

```text
~/Projects/my-project/multi-agent-orchestrator/agentmeeting.md
```

Use it to see who owns what and the current lifecycle states.

Before expanding an agent's scope, coordinate and acquire the new scope first. Never assume that silence means another agent's files are free.

---

## 14. Let the coding agent implement its feature

The agent works only inside its assigned worktree.

For example, Codex works inside:

```text
~/Projects/my-project/.agent-worktrees/backend-auth
```

Normal development still applies:

```text
inspect
→ implement
→ test
→ inspect diff
→ commit
→ push branch
→ open PR
```

The orchestrator coordinates ownership and lifecycle. It does not replace Git or GitHub.

---

## 15. Validate completed work

When an agent says its feature is finished, run the appropriate project validation through the orchestrator.

For a Go project:

```bash
go run ./cmd/orchestrator \
  --complete \
  --workspace ~/Projects/my-project \
  --worktree ~/Projects/my-project/.agent-worktrees/backend-auth \
  --session CODEX_SESSION_ID \
  --feature backend-auth \
  --token codex-backend-auth \
  --validate-program go \
  --validate-arg test \
  --validate-arg ./...
```

For a Node project, an example is:

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

Use the real test command required by the target repository.

Do not substitute a generic validation command when the project has its own required checks.

---

## 16. Understand successful completion

A successful completion follows:

```text
WORKING
   ↓
validation passes
   ↓
resolution evidence recorded
   ↓
COMPLETE + all session claims released
(one locked, atomic agentmeeting.md replacement)
```

`--complete` completes the entire session, including additional claims acquired
under that session for other features/scopes. `--token` still identifies an owned
claim and the evidence reference; it does not limit cleanup to that token. Other
sessions' claims remain owned. To release just one claim without completing the
session, use `--release --session SESSION_ID --token CLAIM_TOKEN`.

The coordination lock covers ownership/lifecycle checks, successful evidence
recording and the combined state/claim ledger update. A direct COMPLETE transition
with outstanding claims is rejected, and a completed session cannot acquire or
receive new claims.

Resolution evidence and the live ledger are separate files. If evidence is written
but ledger publication fails or the process crashes, evidence may remain while
the prior live state and claims are retained. The command returns an error on
publication failure; verify `agentmeeting.md` rather than treating a success
heading in `resolution.md` as proof of completion. After resolving the storage or
lock failure, retry from the valid live state; retries may append repeated evidence.
Do not delete historical evidence or automatically remove a stale lock. The
single-ledger rename is not a distributed or crash-durable cross-file transaction.

The agent's code still remains on its feature branch until the normal GitHub integration process is completed.

---

## 17. Understand failed validation

If validation fails, BLOCKED evidence is recorded and all claims are retained:

```text
WORKING
   ↓
validation fails
   ↓
BLOCKED
   ↓
all session claims remain owned
```

Do not assign the same scope to another agent simply because the first attempt failed.

Fix the failure on the existing owned work or perform an explicit authorized handoff.

---

## 18. Push and open a pull request

Each agent pushes its own feature branch.

Then open one focused pull request for that feature.

The recommended integration workflow is:

```text
agent implements
      ↓
tests
      ↓
commit
      ↓
push feature branch
      ↓
open PR
      ↓
CI runs
      ↓
review exact PR head
      ↓
ORCHESTRATOR/authorized human merges
```

Do not treat an old green CI run as proof that the current PR head passes.

---

## 19. Repeat for the next feature

After a feature is completed and integrated, select the next eligible task.

Start another isolated assignment:

```bash
go run ./cmd/orchestrator \
  --start \
  --workspace ~/Projects/my-project \
  --agent Codex \
  --role coding-agent \
  --feature NEXT_FEATURE \
  --scope NEXT_SCOPE \
  --token NEXT_UNIQUE_TOKEN \
  --base main
```

Prefer starting new work from an up-to-date base branch.

---

## 20. Optional: use the end-to-end runtime

The orchestrator can also compose start, execution, validation, and completion.

Example shape:

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

Only use this after you understand the manual lifecycle. The exact executable and arguments depend on the coding-agent CLI you are using.

The runtime uses structured executable/argument vectors. Do not convert the flags back into one shell command string.

---

## 21. What the orchestrator does not do

The current milestone does **not** automatically discover open Codex, Claude Code, OpenCode, or Antigravity applications.

It does not magically attach itself to existing agent windows.

The current connection model is:

```text
one target repository
       +
shared coordination state
       +
registered sessions
       +
exclusive claims
       +
isolated Git worktrees
       +
activation protocol
       +
coding agents opened in assigned worktrees
```

It is also not an OS/container sandbox or a distributed multi-machine lock service.

---

## 22. Recommended first real-world test

Do not begin with four agents editing a large production feature.

Start with a small repository and two independent tasks.

Example:

```text
Codex
Feature: add-health-endpoint
Scope: backend/health

Claude Code
Feature: update-health-docs
Scope: docs/health
```

Confirm that:

- both sessions register;
- both claims appear;
- separate worktrees are created;
- both agents can work independently;
- overlapping claims are rejected;
- heartbeats update;
- validation evidence is recorded;
- successful completion releases the correct claim;
- each branch can be pushed and reviewed normally.

After that works, increase to three or four simultaneous agents.

---

## Quick daily checklist

1. Pull/clone the target project.
2. Update its default branch.
3. Break the work into non-overlapping features.
4. Run `--start` once for each agent assignment.
5. Save each returned session ID and worktree.
6. Open each coding agent in its own worktree.
7. Give every agent the activation protocol and its exact assignment.
8. Monitor `agentmeeting.md` and refresh heartbeats.
9. Keep every agent inside its claimed scope.
10. Run the project's real validation before completion.
11. Push one feature branch and PR per feature.
12. Verify CI on the exact PR head.
13. Merge only through the authorized integration workflow.
14. Start the next task from the current base.

---

## Core mental model

Remember these three rules:

```text
The activation prompt teaches the protocol.

The Go runtime enforces local coordination.

GitHub remains the integration and review gate.
```

And the ownership invariant remains:

```text
One feature → one owner → one branch → one isolated worktree.
```
