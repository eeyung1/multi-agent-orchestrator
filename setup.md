# Multi-Agent Orchestrator — Quick Setup

Coordinate multiple AI coding agents on a local Git repository. The Go controller manages task ownership, isolated Git worktrees, supervised execution, configured validation, and collected results. You approve the plan and review the evidence; it does not automatically merge or deploy.

---

## 1. Check the requirements

You need:
- **Git** (2.20 or newer).
- **Go 1.24 or newer** (check with `go version`).
- A target project that is already a Git repository.
- Coding agents to run the tasks. These can be:
  - **Interactive coding agents in chat/IDE** (e.g. Google Antigravity, Claude, Cursor, Windsurf, ChatGPT) using the **Prompt-Driven Workflow** (recommended below).
  - **Supported CLI tools installed on PATH** (Codex, Claude Code, or Gemini CLI) using the **Managed CLI Workflow**.
  - **Headless protocol bridges** connecting over Unix sockets using the **Automated IPC Protocol**.

---

## 2. Clone the orchestrator

```bash
mkdir -p ~/Projects
cd ~/Projects
git clone https://github.com/eeyung1/multi-agent-orchestrator.git
cd multi-agent-orchestrator
```

Verify the build and tests:

```bash
go test ./...
```

*(Note: On systems where `go` invokes an older bootstrap version, prefix commands with your installed Go 1.24 toolchain, e.g. `GOTOOLCHAIN=go1.24.10 go test ./...`).*

---

## 3. Prepare your project

Ensure your target project is a Git repository:

```bash
cd ~/Projects/my-project
git status
```

Initialize the authoritative coordination directory (`multi-agent-orchestrator/`):

```bash
cd ~/Projects/multi-agent-orchestrator
go run ./cmd/orchestrator --workspace ~/Projects/my-project --enroll \
  --enrollment-disposition NATIVE \
  --enrollment-provenance "Human operator initialized project coordination"
```

This bootstraps the shared coordination ledgers under `~/Projects/my-project/multi-agent-orchestrator/`:
- `agentmeeting.md`: Live presence, claims, heartbeats, and the **Feature Backlog**.
- `coordination.md`: Human operator decisions, permissions, and blockers.
- `resolution.md`: Append-only evidence of validated completed tasks.

---

## 4. Primary Workflow: Prompt-Driven Planner & Workers

This is the simplest and most flexible workflow for human operators using interactive AI coding assistants (such as Antigravity, Claude, Cursor, or ChatGPT). No Unix sockets or credential JSON files are needed.

```
                              HUMAN OPERATOR
                              /            \
        1. Discuss & Plan    /              \   3. Paste Prompt & Work
                            v                v
                     PLANNER AGENT     WORKER AGENTS
                      (Window 1)       (Windows 2, 3...)
                           |                 |
         2. Writes Backlog v                 v 4. Reads & Claims Task
            +------------------------------------+
            | multi-agent-orchestrator/          |
            |   ├── agentmeeting.md (Backlog)    |
            |   ├── coordination.md (Decisions)  |
            |   └── resolution.md (Evidence)     |
            +------------------------------------+
                           |
                 5. Isolated Git Worktrees
                    (.agent-worktrees/)
```

### Step 4A. Activate Agent 1 as the Planner

Open your first agent window (e.g. in Google Antigravity, Claude, or your IDE), navigate to your target project, and provide the complete [`planner_activation_prompt.md`](planner_activation_prompt.md) (or copy the prompt block below):

```markdown
You are the PLANNER AGENT for this project.
Do not write product code, create branches, or implement features yourself.

Your responsibility:
1. Inspect the repository structure, git log, tech stack, and conventions.
2. Ask me (the human operator) any clarification questions needed to fully understand our objective, constraints, and acceptance criteria.
3. Once we agree on the scope and design, decompose the objective into discrete, independent, bounded tasks.
4. Each task must specify:
   - Task ID & feature name
   - Exact file scope (repository-relative paths; concurrent tasks must have non-overlapping scopes)
   - Dependencies on other tasks
   - Required capability (e.g., "coding")
   - Acceptance criteria and concrete validation commands (e.g., "go test ./...")
5. Write the approved tasks into `multi-agent-orchestrator/agentmeeting.md` under the `## Feature Backlog` table.
```

- **Discussion & Clarifications**: The Planner inspects your repository and conducts an interactive interview with you in chat to clarify requirements.
- **Decomposition**: Once you confirm the tasks, the Planner records them in `multi-agent-orchestrator/agentmeeting.md`. Example:

```markdown
## Feature Backlog
| # | Feature | Scope | Status | Claimed By | Depends On | Acceptance Criteria | Validation |
|---|---|---|---|---|---|---|---|
| 1 | user-auth | internal/auth/ | Unclaimed | - | - | Auth middleware verifies JWT tokens | go test ./internal/auth/... |
| 2 | metrics-api | internal/metrics/ | Unclaimed | - | - | Expose prometheus metrics endpoint | go test ./internal/metrics/... |
```

---

### Step 4B. Activate Worker Agents with `agent_activation_prompt.md`

Whenever you start a worker agent in another window, tab, or tool:

1. Copy the full contents of [`agent_activation_prompt.md`](agent_activation_prompt.md) and paste it into the worker agent's chat or prompt.
2. Add a one-line directive at the top:
   ```markdown
   Follow the attached activation prompt. Read `multi-agent-orchestrator/agentmeeting.md` to find the next unclaimed task from the Planner's backlog, claim it, isolate your work in a git worktree, implement it, and validate it.
   ```

#### What the Worker Agent Does:
1. **Reads Backlog**: Inspects `multi-agent-orchestrator/agentmeeting.md` and selects an `Unclaimed` task.
2. **Claims Task & Isolates Work**:
   The worker uses the Go orchestrator command to atomically record its claim and create an isolated Git worktree:
   ```bash
   go run ./cmd/orchestrator --workspace ~/Projects/my-project --start \
     --agent Worker-A \
     --role backend \
     --feature user-auth \
     --scope internal/auth/ \
     --token token-auth-1 \
     --base main
   ```
   *This automatically registers the claim in `agentmeeting.md` and checks out a dedicated worktree under `.agent-worktrees/user-auth` so workers never collide.*
3. **Implements & Validates**: The worker navigates to its isolated worktree, edits only its assigned scope, and runs the required tests.
4. **Commits Without Co-Author Trailers**: Staged changes are committed locally on the feature branch without `Co-authored-by:` trailers.
5. **Records Handoff & Releases Claim**:
   When tests pass, the worker logs `HANDOFF: commit <sha> ready for publication` in `agentmeeting.md`, appends its result to `resolution.md`, and completes its session:
   ```bash
   go run ./cmd/orchestrator --workspace ~/Projects/my-project --complete \
     --worktree /path/to/worktree \
     --session SESSION_ID \
     --feature user-auth \
     --token token-auth-1 \
     --validate-program go --validate-arg test --validate-arg ./...
   ```
   *This releases the claim and updates the backlog status to `COMPLETE`.*
6. **Autonomously Moves to Next Task**: The worker immediately returns to step 1 to claim the next unclaimed, unblocked task from `agentmeeting.md` without waiting for manual intervention.

---

### Step 4C. Continuous Publication & Integration (Planner-Driven)

While workers code and commit locally, the Planner agent (or authorized integrator):
- **Monitors Handoffs**: Detects worker handoffs logged in `agentmeeting.md`.
- **Pushes & Opens PRs**: Pushes feature branches to remote `origin` and opens pull requests into `main`.
- **Monitors CI**: Watches GitHub Actions CI runs for the exact head commit SHA until checks pass.
- **Merges & Reconciles**: Merges the PR into `main`, pulls merged `origin/main` locally, updates the shared coordination ledgers (`agentmeeting.md`, `coordination.md`, `resolution.md`, `state.json`), and unblocks dependent tasks.
- **Eliminates Bottlenecks**: Workers never stall on DNS restrictions, credentials, PR creation, or CI waits.

---

## 5. Alternative Workflow A: Single-Command Managed CLI Providers

If you have installed and authenticated supported CLI tools (**Codex, Claude Code, or Gemini CLI**) on your system PATH, Go can manage the entire lifecycle automatically:

```bash
go run ./cmd/planner --workspace ~/Projects/my-project --planner codex --workers codex,gemini --max-workers 3
```

- Go invokes the selected CLI tool as Planner.
- Conducts the Q&A discussion and plan presentation in your terminal.
- After you confirm `[y/N]`, Go automatically spawns CLI worker subprocesses in isolated worktrees, runs validation, and produces the review report.

---

## 6. Alternative Workflow B: Headless IPC Protocol (For CI / Automated Bridges)

For headless background daemons or automated integration bridges communicating over private Unix domain sockets:

- Start the Planner with `--enrolled-planner-socket /private/path/agents.sock`.
- Issue one-time 0600 invitation credentials with `invite {"agent_id": ...}`.
- Connect external protocol bridges speaking `orchestrator-agent/v3` (Planner) and `orchestrator-agent/v4` (Workers).
- Authorize sessions in the console and dispatch work.
- See [docs/enrolled-planner.md](docs/enrolled-planner.md), [docs/enrolled-workers.md](docs/enrolled-workers.md), and [docs/agent-attachment.md](docs/agent-attachment.md) for protocol specifications.

---

## Common Problems

| Problem | What to do |
| --- | --- |
| `go: go.mod requires go >= 1.24` | Ensure Go 1.24+ is installed. Set `GOTOOLCHAIN=go1.24.10` or verify `go version`. |
| Task scope overlap rejected | Two workers cannot touch the same file scope simultaneously. Worker B must choose an independent task or wait for Worker A to finish. |
| Tests fail in fresh worktree | A fresh Git worktree excludes untracked files such as `node_modules`. Run dependency install (e.g. `npm install`) inside the worktree before running tests. |
| Another agent owns the task | An agent cannot take over another active claim without an explicit human decision recorded in `coordination.md`. |
| Unsaved work blocked | Workers must commit or clean up worktree changes before completing a task. |

---

## Important Boundaries

- **Local Orchestration**: This coordinates local processes and Git worktrees; it is not an OS/container security sandbox.
- **Sole Coordination Authority**: Go ORCHESTRATOR and the shared coordination ledgers manage ownership. Individual agents cannot self-authorize out-of-scope work or take over another agent's claim.
- **Human Review Authority**: Approval of a plan or review package records human decision evidence; it never automatically merges to `main` or deploys to production.
