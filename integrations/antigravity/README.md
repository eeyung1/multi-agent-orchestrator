# Google Antigravity integration

This directory contains a real SDK-backed bridge for Google Antigravity. It is separate from cmd/agent-bridge, whose fixture modes simulate protocol traffic and do not invoke an AI model.

## What it connects

- Antigravity Planner mode uses the official Python SDK to answer the Go orchestrator's typed planning requests with a structured proposal.
- Antigravity Worker mode uses the SDK to perform an approved assignment from the assigned worktree and reports completion or a blocker.
- The existing Go listener still controls enrollment, assigned roles, claims, generations, worktrees, validation, recovery and review. A model response does not grant itself authority.

## Requirements

- Go 1.24+ and the existing Multi-Agent Orchestrator checkout.
- Python 3.10+.
- The official Google Antigravity SDK and valid Gemini API or Google Cloud credentials. See https://www.antigravity.google/docs/sdk/overview.
- A target Git repository and the normal trusted Go policy/validators.
- A compatible Unix-domain socket path in a private directory.

Install the SDK in a virtual environment:

    python3 -m venv .venv-antigravity
    source .venv-antigravity/bin/activate
    python -m pip install --upgrade pip
    python -m pip install -r integrations/antigravity/requirements.txt

Configure authentication as described in the official docs. For a Gemini API key, set GEMINI_API_KEY in the shell where the bridge will run. Never put a key or invitation secret in a committed file, shell history, issue, or log.

## Important limits

This bridge is an integration, not a security sandbox. A local agent process can attempt filesystem or shell operations within the permissions available to its user. The Go controller checks assignment ownership and validates the worktree, but it cannot contain hostile same-UID code. Use a disposable Git repository for the first live test, review SDK tool approvals, and retain human review.

The bridge refuses to start an SDK turn if the workspace has .agents/mcp_config.json unless you pass --allow-workspace-mcp. Review that file and every configured server before opting in. SDK policy prompts may require your interaction when the agent requests a high-risk tool such as shell execution.

No real Antigravity call has been verified merely by running unit tests. A live acceptance requires your authenticated SDK environment and an actual Planner or Worker turn. The Go fixture acceptance suite remains separate.

## First test

From the repository root:

    source .venv-antigravity/bin/activate
    python integrations/antigravity/bridge.py --help
    python -m pytest -q tests/test_antigravity_bridge.py

These checks verify the command and pure proposal validation. They do not call the model. Next, use the local enrollment steps in antigravity-testing.md with a disposable Git repository. The exact Planner/Worker commands are there.

## Debugging

- SDK import failure: activate the environment and install the requirements.
- Authentication failure: verify credentials in the same terminal used to start the bridge.
- Enrollment rejected: check that the AgentID, advertised role/capability and one-use invitation match exactly.
- Planner asks clarification: inspect the question; the adapter deliberately refuses malformed proposals rather than inventing a valid plan.
- Worker blocked: inspect its error and the preserved worktree. Do not delete claims, branches, or state to force a retry.
