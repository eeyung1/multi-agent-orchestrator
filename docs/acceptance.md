# Real-World Acceptance & Heterogeneous Multi-Agent Operations: Phase 8

Phase 8 demonstrates that the Go orchestrator coordinates independent, heterogeneous
agent runtimes executing concurrent tasks across isolated Git worktrees under authoritative
Go control.

This milestone establishes end-to-end interoperability across genuinely different agent
implementations (Python 3 and Node.js standard libraries) speaking the universal protocol
over Unix domain sockets, verifies concurrent execution in separate isolated worktrees,
demonstrates live operational inspection, exercises crash and disconnect recovery, and
proves strict generation fencing and human review gates.

---

## Architecture and Protocol Compliance

The orchestrator enforces a single, authoritative control plane implemented in Go.
Agents connect as external clients over private Unix domain sockets using typed NDJSON
message envelopes defined by the universal wire protocol:

- **Universal Protocol Envelope (`internal/agentprotocol`)**:
  - `orchestrator-agent/v3` for Planner communication.
  - `orchestrator-agent/v4` for Worker task dispatch and execution.
- **Invitation & Credential Model (`internal/agentattachment`)**:
  - The orchestrator issues single-use invitation tokens with private credential files
    enforced with `0600` file permissions.
  - External agents read the credential file, connect to the Unix socket, and send an
    `agent_handshake` envelope containing the token, agent identity, and advertised capabilities.
  - Handshake acknowledgment (`handshake_ack`) returns an authoritative Go session identity,
    session generation, and heartbeat cadence.
- **Liveness & Heartbeats**:
  - Connected agents transmit periodic `agent_heartbeat` envelopes.
  - The Go orchestrator responds with `heartbeat_ack` and tracks session liveness in
    persisted state.

### Provider-Neutral Authority Invariants

1. **Go ORCHESTRATOR is the sole coordination authority**: No external agent, model, or bridge
   can self-authorize tasks, reassign ownership, or modify scheduler state directly.
2. **Agent identity and advertised capabilities determine compatibility**: Provider or model
   metadata does not grant authority. Python, Node.js, Go fixtures, and commercial LLM bridges
   adhere to the exact same wire protocol.
3. **Planner is the reasoning interface, not the scheduler**: The Planner proposes tasks; the
   human operator explicitly reviews and authorizes the plan.
4. **Workers operate in isolated Git worktrees**: Each task is assigned to exactly one worker
   session in a dedicated branch and private worktree (`.agent-worktrees/<task-id>`). Concurrent
   tasks never share worktree directories or uncommitted state.
5. **Strict Generation Fencing**: Each assignment carries a monotonic `assignment_generation`.
   A disconnected or superseded worker is fenced out; its stale submissions are rejected.
6. **Human Review Gate**: Worker completion does not auto-merge code. The operator reviews diffs,
   runs validators, and authorizes merge decisions.

---

## Independent Agent Implementations

Phase 8 provides two independent worker implementations and an independent planner bridge,
all located in `test/acceptance/agents/`:

### 1. Python Worker (`test/acceptance/agents/python_worker/worker.py`)
- **Runtime**: Python 3 standard library only (`socket`, `json`, `sys`, `os`, `threading`, `time`, `argparse`). Zero third-party package dependencies.
- **Protocol**: `orchestrator-agent/v4`.
- **Capabilities**: Advertises `coding` and `python` capabilities.
- **Behavior**:
  - Connects to Unix domain socket, verifies `0600` credential permissions, completes handshake.
  - Maintains background heartbeat thread.
  - Receives `worker_assignment`, resolves target worktree path, and executes scoped file modifications.
  - Supports barrier synchronization (`--pause-ready-file` / `--pause-release-file`) to hold execution in a `BUSY` state for concurrent multi-agent acceptance testing.
  - Supports fault injection (`--disconnect-after-accept`) to test immediate worker disconnects and recovery fencing.
  - Emits structured `worker_result` with exit code and summary.

### 2. Node.js Worker (`test/acceptance/agents/node_worker/worker.js`)
- **Runtime**: Node.js standard library only (`net`, `fs`, `readline`). Zero npm package dependencies.
- **Protocol**: `orchestrator-agent/v4`.
- **Capabilities**: Advertises `coding` and `javascript` capabilities.
- **Behavior**:
  - Connects to Unix domain socket via Node `net.createConnection`.
  - Performs JSON-lines handshake, sets up periodic heartbeat interval.
  - Receives `worker_assignment`, creates scoped files within assigned worktree, and submits `worker_result`.
  - Supports barrier synchronization (`--pause-ready-file` / `--pause-release-file`) for coordinated multi-process testing.

### 3. Python Planner Bridge (`test/acceptance/agents/planner/planner.py`)
- **Runtime**: Python 3 standard library only (`socket`, `json`, `sys`, `os`, `threading`, `time`, `argparse`).
- **Protocol**: `orchestrator-agent/v3`.
- **Capabilities**: Advertises `planning` capability and `PLANNER` role support.
- **Behavior**:
  - Connects via invitation credential, completes handshake, and maintains heartbeats.
  - Listens for `planner_request` containing the high-level human objective and repository recon report.
  - Decomposes the objective into independent, parallel task definitions with non-overlapping canonical scopes (`demo/python` and `demo/node`), validator requirements, and acceptance criteria.
  - Sends `planner_response` containing the structured plan proposal and keeps the session alive for human adoption.

---

## Reproducible Acceptance Scenarios

The acceptance suite is implemented in `test/acceptance/phase8_test.go` and can be executed
against any Go 1.22+ environment.

### Scenario 1: Heterogeneous Multi-Agent Concurrent Execution
**Test**: `TestPhase8HeterogeneousMultiAgentAcceptance`

1. **Environment Setup**:
   - Creates a disposable Git repository with initialized commit history and branch `main`.
   - Generates configuration with allowed scopes (`demo/python`, `demo/node`), validators (`whitespace`), and `max_workers: 4`.
   - Starts orchestrator controller and Unix domain socket listener.
2. **Planner Enrollment & Proposal**:
   - Enrolls the independent Python Planner process.
   - Operator selects Planner binding; Planner decomposes objective `"Implement heterogeneous multi-agent demonstration"`.
   - Operator reviews the proposal and explicitly authorizes plan admission (`controller.AdmitEnrolled`).
3. **Concurrent Worker Dispatch**:
   - Enrolls the Python Worker and the Node.js Worker simultaneously.
   - Operator authorizes both worker sessions.
   - Controller dispatches `task-demo-python` to the Python Worker and `task-demo-node` to the Node.js Worker.
   - Both workers enter active execution and write barrier ready signals.
4. **Live Operational Inspection**:
   - The test queries the live operational status via `cmd/orchestrator status --json` (or `operations.ProjectView`).
   - Verifies that both workers are simultaneously in status `BUSY`.
   - Verifies that both tasks run in separate, isolated worktrees:
     - `.agent-worktrees/task-demo-python`
     - `.agent-worktrees/task-demo-node`
   - Verifies that both tasks have distinct assignment generations and exclusive coordination claims.
5. **Completion & Review**:
   - Barrier files are released; both workers finish their worktree edits and report results.
   - Controller commits changes under Go authority, runs project validators, and generates task review records.
   - Operator approves both tasks; code is ready for final merge.

### Scenario 2: Worker Disconnect & Reassignment Recovery
**Test**: `TestPhase8WorkerDisconnectAndRecoveryAcceptance`

1. **Abrupt Worker Disconnect**:
   - Dispatches a task to a Python Worker configured with `--disconnect-after-accept`.
   - The worker accepts the task and immediately severs its socket connection.
2. **Generation Fencing & Blocked State**:
   - Controller detects the broken connection; heartbeat liveness fails.
   - The task is marked blocked with attention code `DISCONNECTED`.
   - The stale worker session cannot submit results; any late attempt is fenced out.
3. **Operator Recovery Authorization**:
   - The operator inspects the blocked task via operational status.
   - Operator authorizes reassignment to a replacement worker (Node.js Worker).
   - Scheduler updates the assignment generation strictly monotonically (`recoveredGen > failingGen`).
4. **Clean Replacement Completion**:
   - The Node.js Worker receives the new assignment generation, executes in a fresh isolated worktree, and submits results.
   - Controller runs validation and produces review approval evidence.

### Scenario 3: CLI Operations Command Verification
**Test**: `TestPhase8OperationsCLIExecution`

- Executes `cmd/orchestrator` binaries directly:
  - `status --workspace <path>` (human-readable table).
  - `status --workspace <path> --json` (machine-readable schema).
  - `inspect-agent --workspace <path> --agent <id>`.
  - `inspect-task --workspace <path> --objective <obj> --task <task>`.
  - `operations-help`.
- Asserts exit code `0` and validates output field contents.

---

## Running the Acceptance Suite

Execute the acceptance suite using standard Go test commands:

```bash
# Run all Phase 8 acceptance tests
go test -v ./test/acceptance/...

# Run specific acceptance tests
go test -v -run TestPhase8HeterogeneousMultiAgentAcceptance ./test/acceptance
go test -v -run TestPhase8WorkerDisconnectAndRecoveryAcceptance ./test/acceptance
go test -v -run TestPhase8OperationsCLIExecution ./test/acceptance

# Run with Go race detector enabled
go test -race ./test/acceptance
```

---

## Live Operational Status Sample Output

During concurrent execution in `TestPhase8HeterogeneousMultiAgentAcceptance`, operational
status inspection via `cmd/orchestrator status --json` reflects the live state:

```json
{
  "active_objectives": ["phase8-heterogeneous-demo"],
  "objectives": [
    {
      "id": "phase8-heterogeneous-demo",
      "description": "Implement heterogeneous multi-agent demonstration",
      "status": "ACTIVE",
      "authorized_by": "acceptance operator authorized plan"
    }
  ],
  "tasks": [
    {
      "id": "task-demo-python",
      "objective_id": "phase8-heterogeneous-demo",
      "feature": "feature-demo-python",
      "scope": "demo/python",
      "status": "ASSIGNED",
      "assigned_agent": "python-worker-agent",
      "assignment_generation": 1,
      "isolated_worktree": ".agent-worktrees/task-demo-python"
    },
    {
      "id": "task-demo-node",
      "objective_id": "phase8-heterogeneous-demo",
      "feature": "feature-demo-node",
      "scope": "demo/node",
      "status": "ASSIGNED",
      "assigned_agent": "node-worker-agent",
      "assignment_generation": 2,
      "isolated_worktree": ".agent-worktrees/task-demo-node"
    }
  ],
  "agents": [
    {
      "identity": {
        "agent_id": "python-worker-agent",
        "session_id": "sess-py-worker-01"
      },
      "status": "BUSY",
      "assigned_role": "WORKER",
      "capabilities": ["coding", "python"]
    },
    {
      "identity": {
        "agent_id": "node-worker-agent",
        "session_id": "sess-node-worker-02"
      },
      "status": "BUSY",
      "assigned_role": "WORKER",
      "capabilities": ["coding", "javascript"]
    }
  ]
}
```

---

## Commercial LLMs and Provider Neutrality

The orchestrator core is strictly provider-neutral. It does not hardcode Anthropic, OpenAI,
Google Gemini, DeepSeek, or any commercial vendor.

- **Local & Automated CI Testing**: In environments without live AI provider credentials,
  acceptance tests execute deterministic, independent Python and Node.js agent runtimes.
  This reliably verifies wire protocol framing, credential security, heartbeat liveness,
  concurrency, worktree filesystem isolation, and crash recovery without external network
  dependencies or API flakiness.
- **Commercial Agent Bridges**: External or vendor-specific agents (such as Codex, Claude Code,
  Gemini, OpenCode, or Kimi) integrate by running a lightweight adapter/bridge process that
  speaks `orchestrator-agent/v3` or `v4` over the private Unix domain socket.
- **Truthful Testing Statement**: Commercial LLM inference is only claimed when run with valid
  API credentials and explicit network access. In test environments where credentials are not
  configured, the orchestrator's provider neutrality ensures that protocol-compliant agent
  bridges exhibit identical coordination and authority behavior.

