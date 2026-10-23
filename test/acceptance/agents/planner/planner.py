#!/usr/bin/env python3
"""
Independent Python 3 Planner bridge for multi-agent-orchestrator Phase 8.
Speaks the universal NDJSON protocol (orchestrator-agent/v3) over a local Unix domain socket.
Standard-library only: socket, json, threading, os, sys, time, secrets, argparse, stat.
"""

import argparse
import json
import os
import secrets
import socket
import stat
import sys
import threading
import time

PROTOCOL_VERSION = "orchestrator-agent/v3"


def parse_args():
    parser = argparse.ArgumentParser(description="Independent Python Planner Agent")
    parser.add_argument("--socket", required=True, help="Path to orchestrator Unix domain socket")
    parser.add_argument("--credential-file", required=True, help="Path to 0600 invitation credential JSON")
    parser.add_argument("--agent-id", required=True, help="Logical Agent ID")
    parser.add_argument("--provider", default="python-planner-native", help="Informational provider metadata")
    parser.add_argument("--heartbeat-interval", type=float, default=2.0, help="Heartbeat interval in seconds")
    parser.add_argument("--tasks-file", help="Optional JSON file containing custom tasks array")
    parser.add_argument("--proposal-file", help="Optional JSON file containing full static proposal")
    parser.add_argument("--max-requests", type=int, default=1, help="Max planning requests to handle before exit")
    return parser.parse_args()


def read_credential(path):
    st = os.stat(path)
    mode = stat.S_IMODE(st.st_mode)
    if mode != 0o600:
        raise PermissionError(f"Credential file {path} has mode {oct(mode)}, expected 0600")
    with open(path, "r", encoding="utf-8") as f:
        data = json.load(f)
    if not isinstance(data, dict) or "id" not in data or "secret" not in data:
        raise ValueError("Invalid credential format: missing id or secret")
    return data["id"], data["secret"]


class PythonPlanner:
    def __init__(self, args):
        self.args = args
        self.sock = None
        self.sock_lock = threading.Lock()
        self.session_id = f"py-planner-{secrets.token_hex(8)}"
        self.session_generation = None
        self.stop_event = threading.Event()
        self.handled_requests = 0

    def connect_and_enroll(self):
        cred_id, cred_secret = read_credential(self.args.credential_file)
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.connect(self.args.socket)

        enroll_req = {
            "protocol_version": PROTOCOL_VERSION,
            "type": "enrollment_request",
            "agent_id": self.args.agent_id,
            "session_id": self.session_id,
            "identity": {
                "protocol_version": PROTOCOL_VERSION,
                "agent_id": self.args.agent_id,
                "session_id": self.session_id,
                "metadata": {
                    "provider": self.args.provider,
                    "runtime": "python3"
                },
                "capabilities": ["planning"],
                "supported_roles": ["PLANNER"]
            },
            "invitation": {
                "id": cred_id,
                "secret": cred_secret
            }
        }
        self._send_envelope(enroll_req)

        resp = self._read_one_envelope()
        if not resp:
            raise ConnectionError("Connection closed during enrollment handshake")
        if resp.get("type") != "enrollment_response":
            raise ValueError(f"Expected enrollment_response, got {resp.get('type')}")
        enrollment = resp.get("enrollment", {})
        if enrollment.get("state") != "ADMITTED":
            raise PermissionError(f"Enrollment rejected: {enrollment.get('reason')}")
        self.session_generation = enrollment.get("session_generation")
        sys.stderr.write(f"[python-planner] Enrolled successfully as {self.args.agent_id} session {self.session_id} generation {self.session_generation}\n")
        sys.stderr.flush()

    def _send_envelope(self, env):
        line = json.dumps(env, separators=(',', ':')) + "\n"
        with self.sock_lock:
            self.sock.sendall(line.encode("utf-8"))

    def _read_one_envelope(self):
        buf = bytearray()
        while True:
            b = self.sock.recv(1)
            if not b:
                return None
            if b == b'\n':
                break
            buf.extend(b)
        if not buf:
            return None
        return json.loads(buf.decode("utf-8"))

    def start(self):
        self.heartbeat_thread = threading.Thread(target=self._heartbeat_loop, daemon=True)
        self.heartbeat_thread.start()
        self._run_planning_loop()

    def _heartbeat_loop(self):
        while not self.stop_event.is_set():
            time.sleep(self.args.heartbeat_interval)
            if self.stop_event.is_set():
                break
            env = {
                "protocol_version": PROTOCOL_VERSION,
                "type": "heartbeat",
                "agent_id": self.args.agent_id,
                "session_id": self.session_id
            }
            try:
                self._send_envelope(env)
            except Exception as e:
                sys.stderr.write(f"[python-planner] Heartbeat send failed: {e}\n")
                self.stop_event.set()
                break

    def _run_planning_loop(self):
        buf = bytearray()
        try:
            while not self.stop_event.is_set():
                chunk = self.sock.recv(4096)
                if not chunk:
                    break
                buf.extend(chunk)
                while b'\n' in buf:
                    idx = buf.index(b'\n')
                    line = buf[:idx]
                    buf = buf[idx + 1:]
                    if not line:
                        continue
                    env = json.loads(line.decode("utf-8"))
                    self._handle_envelope(env)
        except Exception as e:
            if not self.stop_event.is_set():
                sys.stderr.write(f"[python-planner] Planning loop error: {e}\n")
        finally:
            self.stop_event.set()
            try:
                self.sock.close()
            except Exception:
                pass

    def _handle_envelope(self, env):
        msg_type = env.get("type")
        if msg_type == "session_acknowledgment":
            # Heartbeat ack
            return
        if msg_type == "planner_request":
            self._handle_planner_request(env)
        elif msg_type == "disconnect":
            sys.stderr.write("[python-planner] Received disconnect from orchestrator\n")
            self.stop_event.set()

    def _handle_planner_request(self, env):
        req_payload = env.get("planning_request", {})
        correlation = req_payload.get("correlation", {})
        request = req_payload.get("request", {})
        objective = request.get("objective", "")
        capabilities = request.get("capabilities", ["coding"])
        validators = request.get("validators", ["whitespace"])
        allowed_scopes = request.get("allowed_scopes", ["demo/task_a", "demo/task_b"])

        sys.stderr.write(f"[python-planner] Received planning request for objective: {objective}\n")
        sys.stderr.flush()

        if self.args.proposal_file:
            with open(self.args.proposal_file, "r", encoding="utf-8") as f:
                proposal = json.load(f)
            proposal["plan"]["objective"] = objective
        elif self.args.tasks_file:
            with open(self.args.tasks_file, "r", encoding="utf-8") as f:
                tasks = json.load(f)
            proposal = {
                "plan": {
                    "id": "suggested-plan",
                    "objective": objective,
                    "tasks": tasks
                },
                "questions": []
            }
        else:
            # Build independent parallel tasks based on allowed scopes
            tasks = []
            for i, scope in enumerate(allowed_scopes):
                clean_name = scope.replace("/", "-").replace(".", "-").strip("-")
                tasks.append({
                    "id": f"task-{clean_name}",
                    "feature": f"feature-{clean_name}",
                    "scope": scope,
                    "description": f"Implement independent functionality for {scope}",
                    "acceptance_criteria": [f"Deliver working files in {scope}"],
                    "validation": [validators[0]],
                    "capability": capabilities[0]
                })

            proposal = {
                "plan": {
                    "id": "suggested-plan",
                    "objective": objective,
                    "tasks": tasks
                },
                "questions": []
            }

        resp_env = {
            "protocol_version": PROTOCOL_VERSION,
            "type": "planner_response",
            "agent_id": self.args.agent_id,
            "session_id": self.session_id,
            "planning_response": {
                "correlation": correlation,
                "proposal": proposal
            }
        }
        self._send_envelope(resp_env)
        sys.stderr.write(f"[python-planner] Sent planning proposal with {len(proposal['plan']['tasks'])} tasks\n")
        sys.stderr.flush()
        self.handled_requests += 1


def main():
    args = parse_args()
    planner = PythonPlanner(args)
    try:
        planner.connect_and_enroll()
        planner.start()
    except Exception as e:
        sys.stderr.write(f"[python-planner] Fatal error: {e}\n")
        sys.stderr.flush()
        sys.exit(1)


if __name__ == "__main__":
    main()
