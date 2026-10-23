# Antigravity live testing checklist

Use this only after the branch containing the adapter is checked out locally. Start with a disposable Git repository, not your main project. Keep the orchestrator checkout, target repository, and credential directory separate.

## 1. Prepare a safe test workspace

    mkdir -p ~/Projects
    mkdir ~/Projects/mao-antigravity-smoke
    cd ~/Projects/mao-antigravity-smoke
    git init -b main
    printf '# Antigravity smoke test\n' > README.md
    git add README.md
    git commit -m "test: initialize disposable Antigravity workspace"

Return to the orchestrator checkout and run the existing suite first:

    go test ./...

## 2. Create a private socket directory

On Linux:

    mkdir -m 700 /tmp/mao-antigravity-run

On macOS, use a canonical private path such as /private/tmp/mao-antigravity-run. Do not reuse an existing directory or socket. The socket parent must be private (mode 0700); the listener creates the socket with mode 0600.

## 3. Start an enrolled Planner session

From the orchestrator repository root:

    go run ./cmd/planner --workspace ~/Projects/mao-antigravity-smoke --enrolled-planner-socket /tmp/mao-antigravity-run/agents.sock

Use the path appropriate for your OS. In the trusted console, create the invitation for the Antigravity Planner:

    invite {"agent_id":"antigravity-planner","credential_file":"/tmp/mao-antigravity-run/planner.json","roles":["PLANNER"],"capabilities":["planning"],"ttl_seconds":120}

The console writes a one-use credential file with mode 0600. Never print or share its secret. In another terminal, activate the Antigravity virtual environment, export your credentials, then run:

    python integrations/antigravity/bridge.py --socket /tmp/mao-antigravity-run/agents.sock --credential-file /tmp/mao-antigravity-run/planner.json --agent-id antigravity-planner --role planner --workspace ~/Projects/mao-antigravity-smoke

Back in the trusted Planner console, use candidates, inspect the exact session, then select it with select <number> and confirm when prompted. Enter an objective small enough to review, such as adding one short section to README.md with a corresponding validation. Inspect the proposed plan and approve only if its scopes, dependencies and validators are correct. The Go controller still validates the proposal and applies its human approval gate.

## 4. Add one Worker

Use the Worker console's invitation command with a distinct ID and credential:

    invite {"agent_id":"antigravity-worker-a","credential_file":"/tmp/mao-antigravity-run/worker-a.json","roles":["WORKER"],"capabilities":["coding"],"ttl_seconds":120}

Ensure the trusted policy has enrolled_capabilities including coding and that the approved task uses capability coding. Start a second bridge process in a new terminal:

    python integrations/antigravity/bridge.py --socket /tmp/mao-antigravity-run/agents.sock --credential-file /tmp/mao-antigravity-run/worker-a.json --agent-id antigravity-worker-a --role worker --capability coding

In the Worker console, run workers, inspect the exact live session and authorize the intended session using authorize <number>. Confirm explicitly. Then run the approved task. The SDK may ask you to approve sensitive tool calls. Read each request before approving it.

## 5. What success looks like

- Both bridge processes print an ADMITTED status with their AgentID/session ID.
- The Planner returns a structured plan or a clarification question; Go validates the proposal and asks for human approval.
- The Worker reports acceptance, progress and a terminal result/blocker for the exact assignment correlation.
- Go, not Antigravity, owns the task claim, generation, branch/worktree, commit, validators, evidence and final review state.
- The target repository's main branch is not merged or deployed automatically.

A live SDK-backed run is required to verify real integration. The Python unit tests only check JSON parsing and proposal validation. Record the actual provider, SDK version, command, result, and relevant redacted logs separately from fixture acceptance results.

## 6. Stop and recover safely

Use Ctrl+C to stop a bridge. A connection close does not prove an independently running tool process has stopped writing. Before recovery, inspect the exact assignment, branch and worktree and ensure the old writer is quiescent. Follow the existing advanced recovery guide; never edit state.json, claims, ledgers or tokens by hand. Do not reuse an invitation credential after admission.
