#!/usr/bin/env python3
"""Google Antigravity SDK adapter for the Multi-Agent Orchestrator local protocol."""
from __future__ import annotations
import argparse, asyncio, json, os, secrets, stat, sys
from pathlib import Path
from typing import Any

PROTOCOL_VERSION = "orchestrator-agent/v4"
MAX_FRAME_BYTES = 1_048_576
MAX_DETAIL_CHARS = 8_192


class BridgeError(RuntimeError):
    pass


def read_credential(path: Path) -> dict[str, str]:
    try:
        info, parent = path.lstat(), path.parent.stat()
    except OSError as exc:
        raise BridgeError("credential file or parent directory is unavailable") from exc
    if path.is_symlink() or not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600:
        raise BridgeError("credential must be a regular non-symlink file with mode 0600")
    if not stat.S_ISDIR(parent.st_mode) or stat.S_IMODE(parent.st_mode) != 0o700:
        raise BridgeError("credential parent directory must have mode 0700")
    if hasattr(os, "getuid") and (info.st_uid != os.getuid() or parent.st_uid != os.getuid()):
        raise BridgeError("credential file and parent must be owned by the current user")
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise BridgeError("credential file is not valid JSON") from exc
    if not isinstance(value, dict) or set(value) != {"id", "secret"}:
        raise BridgeError("credential file must contain only id and secret")
    invitation_id, secret = value.get("id"), value.get("secret")
    if not isinstance(invitation_id, str) or len(invitation_id) != 32 or not isinstance(secret, str) or len(secret) != 64:
        raise BridgeError("credential has an invalid shape")
    if any(ch not in "0123456789abcdef" for item in (invitation_id, secret) for ch in item):
        raise BridgeError("credential contains invalid characters")
    return {"id": invitation_id, "secret": secret}


def validate_socket_path(path: Path) -> None:
    if not path.is_absolute() or path != Path(os.path.normpath(str(path))):
        raise BridgeError("socket path must be absolute and canonical")
    try:
        parent, sock = path.parent.stat(), path.lstat()
    except OSError as exc:
        raise BridgeError("socket unavailable; start the trusted Go listener first") from exc
    if path.is_symlink() or not stat.S_ISSOCK(sock.st_mode):
        raise BridgeError("socket path must be an existing Unix socket")
    if stat.S_IMODE(parent.st_mode) != 0o700:
        raise BridgeError("socket parent directory must have mode 0700")
    if hasattr(os, "getuid") and (parent.st_uid != os.getuid() or sock.st_uid != os.getuid()):
        raise BridgeError("socket parent and socket must be owned by the current user")


def parse_json_object(text: str) -> dict[str, Any]:
    raw = text.strip()
    fence = chr(96) * 3
    if raw.startswith(fence):
        lines = raw.splitlines()
        if len(lines) >= 3 and lines[-1].strip() == fence:
            raw = "\n".join(lines[1:-1]).strip()
    try:
        value = json.loads(raw)
    except json.JSONDecodeError:
        start, end = raw.find("{"), raw.rfind("}")
        if start < 0 or end <= start:
            raise BridgeError("SDK response did not contain a JSON object")
        try:
            value = json.loads(raw[start:end + 1])
        except json.JSONDecodeError as exc:
            raise BridgeError("SDK response contained malformed JSON") from exc
    if not isinstance(value, dict):
        raise BridgeError("SDK response must be a JSON object")
    return value


def validate_proposal(value: dict[str, Any], request: dict[str, Any]) -> dict[str, Any]:
    questions = value.get("questions", [])
    if not isinstance(questions, list) or len(questions) > 32:
        raise BridgeError("proposal questions must be a list of at most 32 items")
    if questions:
        if not all(isinstance(q, str) and q.strip() and len(q) <= 4096 for q in questions):
            raise BridgeError("proposal contains an invalid clarification question")
        return {"questions": questions}
    plan = value.get("plan")
    if not isinstance(plan, dict):
        raise BridgeError("proposal must contain plan or questions")
    objective, tasks = plan.get("objective"), plan.get("tasks")
    if not isinstance(objective, str) or not objective.strip():
        raise BridgeError("proposal plan requires an objective")
    if not isinstance(tasks, list) or not tasks or len(tasks) > 512:
        raise BridgeError("proposal plan requires between 1 and 512 tasks")
    scopes, capabilities = request.get("allowed_scopes", []), request.get("capabilities", [])
    validators, required = request.get("validators", []), request.get("required_validators", [])
    if not all(isinstance(v, list) and v for v in (scopes, capabilities, validators)):
        raise BridgeError("Planner request omitted allowed scopes, capabilities, or validators")
    seen, normalized = set(), []
    for task in tasks:
        if not isinstance(task, dict):
            raise BridgeError("each proposed task must be an object")
        task_id, feature, scope, description = (task.get(k) for k in ("id", "feature", "scope", "description"))
        if not all(isinstance(v, str) and v.strip() for v in (task_id, feature, scope, description)):
            raise BridgeError("each task needs id, feature, scope and description")
        if task_id in seen:
            raise BridgeError("task ids must be unique")
        seen.add(task_id)
        if scope.startswith("/") or ".." in Path(scope).parts:
            raise BridgeError("task scope must be a safe repository-relative path")
        if not any(scope == allowed or scope.startswith(allowed.rstrip("/") + "/") for allowed in scopes if isinstance(allowed, str)):
            raise BridgeError("task scope is outside the requested allowed scopes")
        capability = task.get("capability", capabilities[0])
        if capability not in capabilities:
            raise BridgeError("task capability is not allowed by the request")
        validation = task.get("validation", required)
        if not isinstance(validation, list) or not validation:
            raise BridgeError("each task needs at least one configured validation")
        if any(item not in validators for item in validation) or any(item not in validation for item in required):
            raise BridgeError("task validators must be allowed and include all required validators")
        dependencies = task.get("depends_on", [])
        if not isinstance(dependencies, list) or any(not isinstance(item, str) for item in dependencies):
            raise BridgeError("task dependencies must be a list of task ids")
        normalized.append({"id": task_id, "feature": feature, "scope": scope, "depends_on": dependencies,
            "priority": task.get("priority", 0), "description": description,
            "acceptance_criteria": task.get("acceptance_criteria", []), "validation": validation,
            "capability": capability})
    for task in normalized:
        if any(dep not in seen or dep == task["id"] for dep in task["depends_on"]):
            raise BridgeError("task has an unknown or self dependency")
    return {"plan": {"id": plan.get("id") or "antigravity-" + secrets.token_hex(8),
                     "objective": objective, "tasks": normalized}}


def load_sdk():
    try:
        from google.antigravity import Agent, LocalAgentConfig
    except ImportError as exc:
        raise BridgeError("Antigravity SDK missing; run python -m pip install google-antigravity") from exc
    return Agent, LocalAgentConfig


def workspace_mcp_guard(workspace: Path, allow_workspace_mcp: bool) -> None:
    config = workspace / ".agents" / "mcp_config.json"
    if config.exists() and not allow_workspace_mcp:
        raise BridgeError(f"{config} exists; review it before passing --allow-workspace-mcp")


async def agent_text(prompt: str, workspace: Path, allow_workspace_mcp: bool) -> str:
    workspace_mcp_guard(workspace, allow_workspace_mcp)
    Agent, LocalAgentConfig = load_sdk()
    previous = Path.cwd()
    os.chdir(workspace)
    try:
        config = LocalAgentConfig(system_instructions=(
            "You are enrolled in Multi-Agent Orchestrator. Go is the sole authority "
            "for roles, task ownership, assignment generations, validation, recovery "
            "and review. Never merge, deploy, or commit changes."
        ))
        async with Agent(config) as agent:
            response = await agent.chat(prompt)
            result = await response.text()
            if not isinstance(result, str) or not result.strip():
                raise BridgeError("Antigravity SDK returned an empty response")
            return result
    finally:
        os.chdir(previous)


def planner_prompt(request: dict[str, Any]) -> str:
    schema = {"plan": {"id": "optional-id", "objective": "objective summary", "tasks": [{
        "id": "task-1", "feature": "short feature name", "scope": "allowed/path",
        "description": "specific bounded work", "depends_on": [], "priority": 1,
        "acceptance_criteria": ["observable result"], "validation": ["validator-id"],
        "capability": "allowed-capability"}]}}
    return ("Return only a JSON object matching this shape: " + json.dumps(schema) +
        ' OR {"questions":["clarification question"]}. No Markdown or prose. Use only scopes '
        "within allowed_scopes, capabilities in capabilities, and validators in validators. "
        "Include every required_validators item in every task. Dependencies must refer to "
        "other task IDs. Do not invent commands or validators. Go validates the proposal and "
        "requires human approval before execution.\nREQUEST:\n" +
        json.dumps(request, ensure_ascii=False, separators=(",", ":")))


def worker_prompt(work: dict[str, Any]) -> str:
    assignment, task = work["assignment"], work["assignment"]["task"]
    return ("Implement this approved assignment in the current working directory, which is "
        "the assigned worktree. Keep all changes uncommitted. Do not commit, push, merge, "
        "deploy, edit orchestrator state/claims, or write outside the exact task scope. "
        "Do not modify the main checkout. The Go controller will inspect scope, commit, "
        "validate and collect evidence. Report blockers honestly; do not claim tests passed "
        "unless you ran them and saw them pass.\n"
        f"Objective: {assignment.get('objective', '')}\nTask: {json.dumps(task, ensure_ascii=False)}\n"
        f"Scope: {task.get('scope', '')}\nController rules: {assignment.get('rules', '')}\n"
        f"Additional instructions: {json.dumps(assignment.get('instructions', []), ensure_ascii=False)}")


class ProtocolBridge:
    def __init__(self, args: argparse.Namespace, proof: dict[str, str]) -> None:
        self.args, self.proof = args, proof
        self.writer: asyncio.StreamWriter | None = None
        self.write_lock = asyncio.Lock()
        self.pending: set[asyncio.Task[Any]] = set()
        self.stopping = asyncio.Event()
        self.workspace = Path.cwd().resolve()
        self.session_id = "antigravity-" + secrets.token_hex(16)

    def base(self, kind: str, **fields: Any) -> dict[str, Any]:
        return {"protocol_version": PROTOCOL_VERSION, "type": kind, "agent_id": self.args.agent_id,
                "session_id": self.session_id, **fields}

    async def send(self, envelope: dict[str, Any]) -> None:
        if self.writer is None:
            raise BridgeError("protocol connection is not ready")
        payload = json.dumps(envelope, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        if len(payload) > MAX_FRAME_BYTES:
            raise BridgeError("protocol frame exceeds 1 MiB")
        async with self.write_lock:
            self.writer.write(payload + b"\n")
            await self.writer.drain()

    async def run(self) -> None:
        validate_socket_path(Path(self.args.socket))
        if self.args.role == "planner":
            if not self.args.workspace:
                raise BridgeError("--workspace is required for a Planner")
            self.workspace = Path(self.args.workspace).resolve(strict=True)
            if not self.workspace.is_dir():
                raise BridgeError("Planner workspace must be a directory")
            capabilities, roles = ["planning"], ["PLANNER"]
        else:
            capabilities, roles = [self.args.capability], ["WORKER"]
        reader, self.writer = await asyncio.open_unix_connection(self.args.socket, limit=MAX_FRAME_BYTES + 1)
        identity = {"protocol_version": PROTOCOL_VERSION, "agent_id": self.args.agent_id,
            "session_id": self.session_id, "metadata": {"provider": "antigravity", "runtime": "google-antigravity-sdk"},
            "capabilities": capabilities, "supported_roles": roles}
        await self.send(self.base("enrollment_request", identity=identity, invitation=self.proof))
        line = await asyncio.wait_for(reader.readline(), timeout=10)
        if not line or len(line) > MAX_FRAME_BYTES:
            raise BridgeError("Go listener closed or sent an oversized enrollment response")
        admitted = json.loads(line)
        if (admitted.get("type") != "enrollment_response" or admitted.get("protocol_version") != PROTOCOL_VERSION
            or admitted.get("agent_id") != self.args.agent_id or admitted.get("session_id") != self.session_id
            or (admitted.get("enrollment") or {}).get("state") != "ADMITTED"):
            raise BridgeError("Go orchestrator rejected the Antigravity enrollment")
        generation = (admitted.get("enrollment") or {}).get("session_generation")
        if not isinstance(generation, int) or generation < 1:
            raise BridgeError("Go admission response omitted the session generation")
        print(json.dumps({"status": "ADMITTED", "agent_id": self.args.agent_id,
            "session_id": self.session_id, "generation": generation}), flush=True)
        heartbeat = asyncio.create_task(self.heartbeat())
        try:
            while not self.stopping.is_set():
                line = await reader.readline()
                if not line:
                    raise BridgeError("Go attachment closed the connection")
                if len(line) > MAX_FRAME_BYTES:
                    raise BridgeError("received protocol frame exceeds 1 MiB")
                envelope = json.loads(line)
                if (envelope.get("protocol_version") != PROTOCOL_VERSION
                    or envelope.get("agent_id") != self.args.agent_id
                    or envelope.get("session_id") != self.session_id):
                    raise BridgeError("received message identity does not match this session")
                kind = envelope.get("type")
                if kind == "session_acknowledgment":
                    ack = envelope.get("acknowledgment") or {}
                    if ack.get("for") not in ("heartbeat", "disconnect", "handoff") or ack.get("session_generation") != generation:
                        raise BridgeError("invalid lifecycle acknowledgment")
                elif kind == "planner_request" and self.args.role == "planner":
                    self.spawn(self.handle_planner(envelope))
                elif kind == "worker_assignment" and self.args.role == "worker":
                    self.spawn(self.handle_worker(envelope))
                else:
                    raise BridgeError(f"message {kind!r} is not valid for role {self.args.role}")
        finally:
            self.stopping.set()
            heartbeat.cancel()
            await asyncio.gather(heartbeat, return_exceptions=True)
            for task in self.pending:
                task.cancel()
            if self.pending:
                await asyncio.gather(*self.pending, return_exceptions=True)
            self.writer.close()
            try:
                await self.writer.wait_closed()
            except OSError:
                pass

    def spawn(self, coroutine: Any) -> None:
        task = asyncio.create_task(coroutine)
        self.pending.add(task)
        task.add_done_callback(self.pending.discard)

    async def heartbeat(self) -> None:
        while not self.stopping.is_set():
            await asyncio.sleep(self.args.heartbeat_interval)
            await self.send(self.base("heartbeat"))

    async def handle_planner(self, envelope: dict[str, Any]) -> None:
        payload = envelope.get("planning_request") or {}
        correlation, request = payload.get("correlation"), payload.get("request")
        if not isinstance(correlation, dict) or not isinstance(request, dict):
            raise BridgeError("Planner request lacks typed correlation or request")
        try:
            raw = await agent_text(planner_prompt(request), self.workspace, self.args.allow_workspace_mcp)
            proposal = validate_proposal(parse_json_object(raw), request)
        except Exception as exc:
            proposal = {"questions": [f"Antigravity could not produce a valid proposal: {str(exc)[:300]}"]}
        await self.send(self.base("planner_response", planning_response={"correlation": correlation, "proposal": proposal}))
        print(json.dumps({"event": "planner_response_sent", "request_id": correlation.get("request_id")}), flush=True)

    async def handle_worker(self, envelope: dict[str, Any]) -> None:
        work = envelope.get("work")
        if not isinstance(work, dict) or not isinstance(work.get("correlation"), dict) or not isinstance(work.get("assignment"), dict):
            raise BridgeError("Worker assignment lacks its typed payload")
        correlation, report = work["correlation"], None
        report = {"correlation": correlation}
        await self.send(self.base("worker_acceptance", work={**report, "accepted": True}))
        await self.send(self.base("worker_progress", work={**report, "detail": "Antigravity SDK started the approved assignment"}))
        assignment = work["assignment"]
        try:
            worktree = Path(assignment.get("worktree", "")).resolve(strict=True)
            if not worktree.is_dir() or not worktree.is_absolute():
                raise BridgeError("assigned worktree is not an existing directory")
            await agent_text(worker_prompt(work), worktree, self.args.allow_workspace_mcp)
            detail = "Antigravity SDK turn completed; Go must still inspect and validate the exact worktree changes."
            await self.send(self.base("worker_result", work={**report, "detail": detail[:MAX_DETAIL_CHARS]}))
            print(json.dumps({"event": "worker_result_sent", "task_id": correlation.get("task_id")}), flush=True)
        except Exception as exc:
            await self.send(self.base("worker_blocker", work={**report, "detail": f"Antigravity worker blocked: {str(exc)[:1000]}"}))
            print(json.dumps({"event": "worker_blocker_sent", "task_id": correlation.get("task_id")}), flush=True)


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="Google Antigravity SDK bridge for Multi-Agent Orchestrator")
    p.add_argument("--socket", required=True, help="absolute private Unix socket opened by the Go run")
    p.add_argument("--credential-file", required=True, help="one-use private invitation JSON; never share or commit it")
    p.add_argument("--agent-id", required=True, help="logical ID authorized in the invitation")
    p.add_argument("--role", required=True, choices=("planner", "worker"))
    p.add_argument("--capability", default="coding", help="worker capability matching invitation and trusted policy")
    p.add_argument("--workspace", help="repository directory used by a Planner")
    p.add_argument("--heartbeat-interval", type=float, default=5.0)
    p.add_argument("--allow-workspace-mcp", action="store_true", help="allow MCP config only after reviewing it")
    return p


async def async_main(args: argparse.Namespace) -> None:
    if args.heartbeat_interval <= 0 or args.heartbeat_interval >= 30:
        raise BridgeError("heartbeat interval must be positive and below the listener's 30-second idle timeout")
    proof = read_credential(Path(args.credential_file))
    await ProtocolBridge(args, proof).run()


def main() -> int:
    args = parser().parse_args()
    try:
        asyncio.run(async_main(args))
    except KeyboardInterrupt:
        print("Antigravity bridge stopped by user.", file=sys.stderr)
        return 130
    except Exception as exc:
        print(f"antigravity bridge: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
