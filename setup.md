# Multi-Agent Orchestrator — Quick Setup

This guide gets you from a fresh clone to your first reviewed multi-agent coding task. You do not need to edit a configuration file to start with a supported built-in provider.

**What it does:** a local Go controller coordinates coding agents, assigns eligible tasks to separate Git worktrees, runs configured checks, and collects evidence for your review.

**What it does not do:** it is not a security sandbox, does not automatically discover every open AI-agent window, and does not merge or deploy your code. You remain responsible for reviewing results.

## 1. Check the requirements

You need:

- Git.
- Go 1.24 or newer.
- A target project that is already a Git repository.
- At least one supported coding-agent CLI installed and authenticated: **Codex, Claude Code, or Gemini CLI**.
- Permission to run the provider and its model. Provider use may incur costs under your account.

The orchestrator does not install providers, request credentials on their behalf, or guarantee that a detected CLI is authenticated. Antigravity is a planned integration, not a built-in provider in the current quick-start workflow.

## 2. Clone the orchestrator

Run:

```bash
mkdir -p ~/Projects
cd ~/Projects
git clone https://github.com/eeyung1/multi-agent-orchestrator.git
cd multi-agent-orchestrator
```

Run the Go tests before using your checkout:

```bash
go test ./...
```

## 3. Prepare your project

Clone or create the Git repository you want the agents to work on. For example, if your project already exists remotely:

```bash
cd ~/Projects
git clone <YOUR_PROJECT_GIT_URL> my-project
```

Replace `<YOUR_PROJECT_GIT_URL>` with your repository's actual clone URL. Do not use the placeholder literally.

## 4. Start the Planner

From the `multi-agent-orchestrator` directory, run:

```bash
go run ./cmd/planner --workspace ~/Projects/my-project
```

Replace `~/Projects/my-project` with the actual path to your Git project.

To see which built-in providers the orchestrator detects:

```bash
go run ./cmd/planner --list-providers
```

Detection checks trusted absolute `PATH` entries. It does not verify authentication or prove that a provider can successfully call its model. If a provider is missing, install and authenticate it using that provider's official instructions, then try again.

You can select providers and a concurrency limit directly when you know the IDs:

```bash
go run ./cmd/planner --workspace ~/Projects/my-project --planner codex --workers codex,gemini --max-workers 3
```

Use only provider IDs shown as available by your installation. You do not need to use this optional command if you prefer the interactive selection.

## 5. Approve the setup prompts carefully

On first use, the application may ask you to make important decisions:

1. **Repository authority:** authorize the orchestrator to be the sole coordinator for this repository. If another tool already owns agent/task coordination, stop and reconcile that setup instead of approving blindly.
2. **Execution policy:** review the validators required for your project. Choose checks that actually protect the codebase; the suggested checks may not cover every project.
3. **Worktree preparation (optional):** if the project needs local dependencies or generated files in a fresh worktree, you may explicitly authorize a preparation command. Only approve commands you trust.
4. **Context sharing:** inspect the repository context and confirm before it is sent to the selected planning provider. Provider privacy terms and costs apply.
5. **Plan approval:** read the proposed tasks, dependencies, scopes, and checks. Approve only work you want the agents to perform.

A fresh Git worktree contains tracked files, not your local dependency folders. For example, a JavaScript project may need `node_modules` installed in each worktree before tests can run. Preparation is not silently inferred or run without authorization.

## 6. Let the agents work, then review

After you approve the plan, the Go controller manages task ownership, assignment generations, isolated branches/worktrees, execution supervision, configured validation, and evidence collection.

When the objective reaches `AWAITING_REVIEW`:

- Review the reported task results, exact branch heads, and validation evidence.
- Approve the results or request changes through the supported review flow.
- Inspect the generated branches and diffs before integrating them.
- Merge, push, or deploy only through your own deliberate, authorized workflow.

Restarting the same Planner command lets you select a saved objective. Do not delete coordination state, claims, branches, or worktrees to force a retry. If an assignment is blocked, inspect the preserved work and follow the [advanced recovery guide](advanced-setup.md#recover-a-blocked-planner-assignment).

## Common problems

| Problem | What to do |
| --- | --- |
| `go: go.mod requires go >= 1.24` | Install/use Go 1.24 or newer, then check `go version`. |
| No compatible Planner CLI detected | Install and authenticate Codex, Claude Code, or Gemini CLI; rerun `--list-providers`. |
| Provider detected but call fails | Check that provider's own login, model access, CLI version, and account limits. Detection does not test these. |
| Tests fail in a fresh worktree | Review the validator output and explicitly configure trusted worktree preparation if dependencies are missing. Do not weaken required checks just to get a green result. |
| Another agent/coordinator already owns tasks | Stop and reconcile existing authority before enabling the orchestrator. |
| An assignment is blocked or disconnected | Preserve its branch/worktree and evidence; use the advanced recovery instructions after reviewing the old writer's state. |

## Important boundaries

- This is **local orchestration**, not an operating-system or container sandbox.
- A local socket closing does not prove an independently running process has stopped writing.
- Local filesystem coordination is not distributed locking across multiple machines.
- Independent agents can participate only if they have a compatible protocol client/bridge and are explicitly enrolled and authorized. The orchestrator does not automatically attach arbitrary open chat windows or vendor applications.
- The Go controller remains the authority. A Planner or Worker cannot grant itself permissions, take another task's claim, or approve its own final review.
- Human review does not itself merge or deploy code.

## Need the detailed reference?

See [Advanced Setup and Manual Operations](advanced-setup.md) for custom providers and policy, enrolled Planner/Worker sessions, operational commands, manual workflows, GitHub integration, and recovery procedures.

For architecture and implementation boundaries, start at the [README](README.md) and follow the linked protocol, attachment, worker, and acceptance documents.
