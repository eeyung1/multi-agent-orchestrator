#!/usr/bin/env python3
"""
Independent Python 3 Worker agent for multi-agent-orchestrator Phase 8.
Speaks the universal NDJSON protocol (orchestrator-agent/v4) over a local Unix domain socket.
Standard-library only: socket, json, threading, os, sys, time, secrets, argparse.
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

PROTOCOL_VERSION = "orchestrator-agent/v4"


def parse_args():
    parser = argparse.ArgumentParser(description="Independent Python Worker Agent")
    parser.add_argument("--socket", required=True, help="Path to orchestrator Unix domain socket")
    parser.add_argument("--credential-file", required=True, help="Path to 0600 invitation credential JSON")
    parser.add_argument("--agent-id", required=True, help="Logical Agent ID")
    parser.add_argument("--provider", default="python-worker-native", help="Informational provider metadata")
    parser.add_argument("--capability", default="coding", help="Advertised capability")
    parser.add_argument("--heartbeat-interval", type=float, default=2.0, help="Heartbeat interval in seconds")
    parser.add_argument("--pause-ready-file", help="File to create when assignment accepted")
    parser.add_argument("--pause-release-file", help="File to wait for before completing assignment")
    parser.add_argument("--disconnect-on-task", help="Task ID to disconnect on after acceptance (for recovery test)")
    parser.add_argument("--disconnect-after-accept", action="store_true", help="Disconnect immediately after accepting any assignment")
    parser.add_argument("--reject-task", help="Task ID to explicitly reject")
    parser.add_argument("--file-content", help="Custom content to write to result.txt")
    parser.add_argument("--max-tasks", type=int, default=1, help="Number of tasks to complete before exit")
    return parser.parse_args()


def read_credential(path):
    st = os.stat(path)
    # Check permissions (must be 0600 on Unix)
    mode = stat.S_IMODE(st.st_mode)
    if mode != 0o600:
        raise PermissionError(f"Credential file {path} has mode {oct(mode)}, expected 0600")
    with open(path, "r", encoding="utf-8") as f:
        data = json.load(f)
    if not isinstance(data, dict) or "id" not in data or "secret" not in data:
        raise ValueError("Invalid credential format: missing id or secret")
    return data["id"], data["secret"]


class PythonWorker:
    def __init__(self, args):
        self.args = args
        self.sock = None
        self.sock_lock = threading.Lock()
        self.session_id = f"py-session-{secrets.token_hex(8)}"
        self.session_generation = None
        self.stop_event = threading.Event()
        self.ack_event = threading.Event()
        self.work_queue = []
        self.work_cv = threading.Condition()
        self.completed_tasks = 0

    def connect_and_enroll(self):
        cred_id, cred_secret = read_credential(self.args.credential_file)
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.connect(self.args.socket)

        # Build enrollment request
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
                "capabilities": [self.args.capability],
                "supported_roles": ["WORKER"]
            },
            "invitation": {
                "id": cred_id,
                "secret": cred_secret
            }
        }
        self._send_envelope(enroll_req)

        # Read enrollment response directly
        resp = self._read_one_envelope()
        if not resp:
            raise ConnectionError("Connection closed during enrollment handshake")
        if resp.get("type") != "enrollment_response":
            raise ValueError(f"Expected enrollment_response, got {resp.get('type')}")
        enrollment = resp.get("enrollment", {})
        if enrollment.get("state") != "ADMITTED":
            raise PermissionError(f"Enrollment rejected: {enrollment.get('reason')}")
        self.session_generation = enrollment.get("session_generation")
        sys.stderr.write(f"[python-worker] Enrolled successfully as {self.args.agent_id} session {self.session_id} generation {self.session_generation}\n")
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
        # Start reader thread
        self.reader_thread = threading.Thread(target=self._reader_loop, daemon=True)
        self.reader_thread.start()

        # Start heartbeat thread
        self.heartbeat_thread = threading.Thread(target=self._heartbeat_loop, daemon=True)
        self.heartbeat_thread.start()

        # Main worker execution loop
        self._work_loop()

    def _reader_loop(self):
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
                    self._dispatch_envelope(env)
        except Exception as e:
            if not self.stop_event.is_set():
                sys.stderr.write(f"[python-worker] Reader error: {e}\n")
                sys.stderr.flush()
        finally:
            self.stop_event.set()
            with self.work_cv:
                self.work_cv.notify_all()

    def _dispatch_envelope(self, env):
        msg_type = env.get("type")
        if msg_type == "session_acknowledgment":
            ack = env.get("acknowledgment", {})
            if ack.get("for") == "heartbeat":
                self.ack_event.set()
        elif msg_type == "worker_assignment":
            with self.work_cv:
                self.work_queue.append(env)
                self.work_cv.notify()
        elif msg_type == "disconnect":
            sys.stderr.write("[python-worker] Received disconnect from orchestrator\n")
            self.stop_event.set()

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
                sys.stderr.write(f"[python-worker] Heartbeat send failed: {e}\n")
                self.stop_event.set()
                break

    def _work_loop(self):
        while not self.stop_event.is_set():
            env = None
            with self.work_cv:
                while not self.work_queue and not self.stop_event.is_set():
                    self.work_cv.wait(timeout=1.0)
                if self.work_queue:
                    env = self.work_queue.pop(0)

            if not env:
                continue

            self._process_assignment(env)

    def _process_assignment(self, env):
        work = env.get("work", {})
        correlation = work.get("correlation", {})
        assignment = work.get("assignment", {})
        task = assignment.get("task", {})
        task_id = task.get("id")
        worktree = assignment.get("worktree")
        scope = task.get("scope")

        sys.stderr.write(f"[python-worker] Received task {task_id} scope {scope} in worktree {worktree}\n")
        sys.stderr.flush()

        # Check explicit rejection test
        if self.args.reject_task and self.args.reject_task == task_id:
            reject_env = {
                "protocol_version": PROTOCOL_VERSION,
                "type": "worker_acceptance",
                "agent_id": self.args.agent_id,
                "session_id": self.session_id,
                "work": {
                    "correlation": correlation,
                    "accepted": False,
                    "detail": "Task explicitly rejected by Python test policy"
                }
            }
            self._send_envelope(reject_env)
            return

        # Report acceptance
        accept_env = {
            "protocol_version": PROTOCOL_VERSION,
            "type": "worker_acceptance",
            "agent_id": self.args.agent_id,
            "session_id": self.session_id,
            "work": {
                "correlation": correlation,
                "accepted": True
            }
        }
        self._send_envelope(accept_env)

        # Check disconnect simulation
        if self.args.disconnect_after_accept or (self.args.disconnect_on_task and self.args.disconnect_on_task == task_id):
            sys.stderr.write(f"[python-worker] Simulating abrupt disconnect on task {task_id}\n")
            sys.stderr.flush()
            self.stop_event.set()
            try:
                self.sock.close()
            except Exception:
                pass
            sys.exit(0)

        # Report progress
        prog_env = {
            "protocol_version": PROTOCOL_VERSION,
            "type": "worker_progress",
            "agent_id": self.args.agent_id,
            "session_id": self.session_id,
            "work": {
                "correlation": correlation,
                "detail": "Python worker accepted assignment and started execution"
            }
        }
        self._send_envelope(prog_env)

        # If barrier / pause requested, touch ready file and wait for release file
        if self.args.pause_ready_file:
            with open(self.args.pause_ready_file, "w", encoding="utf-8") as f:
                f.write(f"ready:{task_id}\n")
            sys.stderr.write(f"[python-worker] Created pause-ready file {self.args.pause_ready_file}\n")
            sys.stderr.flush()

        if self.args.pause_release_file:
            sys.stderr.write(f"[python-worker] Waiting for pause-release file {self.args.pause_release_file}...\n")
            sys.stderr.flush()
            while not os.path.exists(self.args.pause_release_file) and not self.stop_event.is_set():
                time.sleep(0.1)

        # Perform work: write files inside worktree within scope
        target_dir = os.path.join(worktree, scope)
        os.makedirs(target_dir, exist_ok=True)
        result_file = os.path.join(target_dir, "result.txt")
        content = self.args.file_content or f"Python worker output for task {task_id} in scope {scope}\n"
        with open(result_file, "w", encoding="utf-8") as f:
            f.write(content)

        sys.stderr.write(f"[python-worker] Wrote {result_file}\n")
        sys.stderr.flush()

        # Report progress
        prog_env2 = {
            "protocol_version": PROTOCOL_VERSION,
            "type": "worker_progress",
            "agent_id": self.args.agent_id,
            "session_id": self.session_id,
            "work": {
                "correlation": correlation,
                "detail": "Python worker generated implementation files"
            }
        }
        self._send_envelope(prog_env2)

        # Report terminal result
        res_env = {
            "protocol_version": PROTOCOL_VERSION,
            "type": "worker_result",
            "agent_id": self.args.agent_id,
            "session_id": self.session_id,
            "work": {
                "correlation": correlation,
                "detail": "Python worker completed task; writers stopped; changes left uncommitted"
            }
        }
        self._send_envelope(res_env)
        sys.stderr.write(f"[python-worker] Reported result for task {task_id}\n")
        sys.stderr.flush()
        self.completed_tasks += 1


def main():
    args = parse_args()
    worker = PythonWorker(args)
    try:
        worker.connect_and_enroll()
        worker.start()
    except Exception as e:
        sys.stderr.write(f"[python-worker] Fatal error: {e}\n")
        sys.stderr.flush()
        sys.exit(1)


if __name__ == "__main__":
    main()
