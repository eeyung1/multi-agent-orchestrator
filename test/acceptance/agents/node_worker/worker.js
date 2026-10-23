#!/usr/bin/env node
/**
 * Independent Node.js Worker agent for multi-agent-orchestrator Phase 8.
 * Speaks the universal NDJSON protocol (orchestrator-agent/v4) over a local Unix domain socket.
 * Standard-library only: net, fs, path, crypto, readline.
 */

const net = require('net');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const readline = require('readline');

const PROTOCOL_VERSION = "orchestrator-agent/v4";

function parseArgs() {
    const args = process.argv.slice(2);
    const options = {
        socket: null,
        credentialFile: null,
        agentId: null,
        provider: "node-worker-native",
        capability: "coding",
        heartbeatInterval: 2000,
        pauseReadyFile: null,
        pauseReleaseFile: null,
        disconnectOnTask: null,
        rejectTask: null,
        fileContent: null,
        maxTasks: 1
    };

    for (let i = 0; i < args.length; i++) {
        switch (args[i]) {
            case '--socket': options.socket = args[++i]; break;
            case '--credential-file': options.credentialFile = args[++i]; break;
            case '--agent-id': options.agentId = args[++i]; break;
            case '--provider': options.provider = args[++i]; break;
            case '--capability': options.capability = args[++i]; break;
            case '--heartbeat-interval': options.heartbeatInterval = parseFloat(args[++i]) * 1000; break;
            case '--pause-ready-file': options.pauseReadyFile = args[++i]; break;
            case '--pause-release-file': options.pauseReleaseFile = args[++i]; break;
            case '--disconnect-on-task': options.disconnectOnTask = args[++i]; break;
            case '--reject-task': options.rejectTask = args[++i]; break;
            case '--file-content': options.fileContent = args[++i]; break;
            case '--max-tasks': options.maxTasks = parseInt(args[++i], 10); break;
            case '-h':
            case '--help':
                console.log("Usage: node worker.js --socket <path> --credential-file <path> --agent-id <id> [options]");
                process.exit(0);
        }
    }

    if (!options.socket || !options.credentialFile || !options.agentId) {
        console.error("[node-worker] Missing required arguments: --socket, --credential-file, --agent-id");
        process.exit(1);
    }
    return options;
}

function readCredential(filePath) {
    const st = fs.statSync(filePath);
    const mode = st.mode & 0o777;
    if (mode !== 0o600) {
        throw new Error(`Credential file ${filePath} has mode ${mode.toString(8)}, expected 0600`);
    }
    const raw = fs.readFileSync(filePath, 'utf-8');
    const parsed = JSON.parse(raw);
    if (!parsed.id || !parsed.secret) {
        throw new Error("Invalid credential format: missing id or secret");
    }
    return parsed;
}

class NodeWorker {
    constructor(opts) {
        this.opts = opts;
        this.client = null;
        this.rl = null;
        this.sessionId = `node-session-${crypto.randomBytes(8).toString('hex')}`;
        this.sessionGeneration = null;
        this.heartbeatTimer = null;
        this.isEnrolled = false;
        this.completedTasks = 0;
        this.stopped = false;
    }

    start() {
        const cred = readCredential(this.opts.credentialFile);
        this.client = net.createConnection(this.opts.socket);

        this.client.on('connect', () => {
            const enrollReq = {
                protocol_version: PROTOCOL_VERSION,
                type: "enrollment_request",
                agent_id: this.opts.agentId,
                session_id: this.sessionId,
                identity: {
                    protocol_version: PROTOCOL_VERSION,
                    agent_id: this.opts.agentId,
                    session_id: this.sessionId,
                    metadata: {
                        provider: this.opts.provider,
                        runtime: "node"
                    },
                    capabilities: [this.opts.capability],
                    supported_roles: ["WORKER"]
                },
                invitation: {
                    id: cred.id,
                    secret: cred.secret
                }
            };
            this.sendEnvelope(enrollReq);
        });

        this.rl = readline.createInterface({
            input: this.client,
            crlfDelay: Infinity
        });

        this.rl.on('line', (line) => {
            if (!line.trim()) return;
            try {
                const env = JSON.parse(line);
                this.handleEnvelope(env);
            } catch (err) {
                console.error("[node-worker] JSON parse error:", err.message);
            }
        });

        this.client.on('error', (err) => {
            if (!this.stopped) {
                console.error("[node-worker] Socket error:", err.message);
            }
        });

        this.client.on('close', () => {
            this.stop();
        });
    }

    sendEnvelope(env) {
        if (this.stopped || !this.client || this.client.destroyed) return;
        const data = JSON.stringify(env) + "\n";
        this.client.write(data, 'utf-8');
    }

    handleEnvelope(env) {
        const type = env.type;
        if (type === "enrollment_response") {
            const enrollment = env.enrollment || {};
            if (enrollment.state !== "ADMITTED") {
                console.error(`[node-worker] Enrollment rejected: ${enrollment.reason}`);
                process.exit(1);
            }
            this.sessionGeneration = enrollment.session_generation;
            this.isEnrolled = true;
            process.stderr.write(`[node-worker] Enrolled successfully as ${this.opts.agentId} session ${this.sessionId} generation ${this.sessionGeneration}\n`);
            this.startHeartbeat();
        } else if (type === "session_acknowledgment") {
            // Heartbeat acknowledged
        } else if (type === "worker_assignment") {
            this.processAssignment(env);
        } else if (type === "disconnect") {
            process.stderr.write("[node-worker] Received disconnect from orchestrator\n");
            this.stop();
        }
    }

    startHeartbeat() {
        this.heartbeatTimer = setInterval(() => {
            if (this.stopped) return;
            const hb = {
                protocol_version: PROTOCOL_VERSION,
                type: "heartbeat",
                agent_id: this.opts.agentId,
                session_id: this.sessionId
            };
            this.sendEnvelope(hb);
        }, this.opts.heartbeatInterval);
    }

    async processAssignment(env) {
        const work = env.work || {};
        const correlation = work.correlation || {};
        const assignment = work.assignment || {};
        const task = assignment.task || {};
        const taskId = task.id;
        const worktree = assignment.worktree;
        const scope = task.scope;

        process.stderr.write(`[node-worker] Received task ${taskId} scope ${scope} in worktree ${worktree}\n`);

        // Check explicit rejection test
        if (this.opts.rejectTask && this.opts.rejectTask === taskId) {
            const rejectEnv = {
                protocol_version: PROTOCOL_VERSION,
                type: "worker_acceptance",
                agent_id: this.opts.agentId,
                session_id: this.sessionId,
                work: {
                    correlation: correlation,
                    accepted: false,
                    detail: "Task explicitly rejected by Node.js test policy"
                }
            };
            this.sendEnvelope(rejectEnv);
            return;
        }

        // Report acceptance
        const acceptEnv = {
            protocol_version: PROTOCOL_VERSION,
            type: "worker_acceptance",
            agent_id: this.opts.agentId,
            session_id: this.sessionId,
            work: {
                correlation: correlation,
                accepted: true
            }
        };
        this.sendEnvelope(acceptEnv);

        // Check disconnect-on-task simulation
        if (this.opts.disconnectOnTask && this.opts.disconnectOnTask === taskId) {
            process.stderr.write(`[node-worker] Simulating abrupt disconnect on task ${taskId}\n`);
            this.stop();
            process.exit(0);
        }

        // Report progress
        const progEnv = {
            protocol_version: PROTOCOL_VERSION,
            type: "worker_progress",
            agent_id: this.opts.agentId,
            session_id: this.sessionId,
            work: {
                correlation: correlation,
                detail: "Node.js worker accepted assignment and started execution"
            }
        };
        this.sendEnvelope(progEnv);

        // If pause ready file configured, touch it
        if (this.opts.pauseReadyFile) {
            fs.writeFileSync(this.opts.pauseReadyFile, `ready:${taskId}\n`, 'utf-8');
            process.stderr.write(`[node-worker] Created pause-ready file ${this.opts.pauseReadyFile}\n`);
        }

        // If pause release file configured, wait until it exists
        if (this.opts.pauseReleaseFile) {
            process.stderr.write(`[node-worker] Waiting for pause-release file ${this.opts.pauseReleaseFile}...\n`);
            while (!fs.existsSync(this.opts.pauseReleaseFile) && !this.stopped) {
                await new Promise(r => setTimeout(r, 100));
            }
        }

        // Perform work: write files inside worktree within scope
        const targetDir = path.join(worktree, scope);
        fs.mkdirSync(targetDir, { recursive: true });
        const resultFile = path.join(targetDir, "result.txt");
        const content = this.opts.fileContent || `Node.js worker output for task ${taskId} in scope ${scope}\n`;
        fs.writeFileSync(resultFile, content, 'utf-8');

        process.stderr.write(`[node-worker] Wrote ${resultFile}\n`);

        // Report progress
        const progEnv2 = {
            protocol_version: PROTOCOL_VERSION,
            type: "worker_progress",
            agent_id: this.opts.agentId,
            session_id: this.sessionId,
            work: {
                correlation: correlation,
                detail: "Node.js worker generated implementation files"
            }
        };
        this.sendEnvelope(progEnv2);

        // Report terminal result
        const resEnv = {
            protocol_version: PROTOCOL_VERSION,
            type: "worker_result",
            agent_id: this.opts.agentId,
            session_id: this.sessionId,
            work: {
                correlation: correlation,
                detail: "Node.js worker completed task; writers stopped; changes left uncommitted"
            }
        };
        this.sendEnvelope(resEnv);
        process.stderr.write(`[node-worker] Reported result for task ${taskId}\n`);
        this.completedTasks++;
    }

    stop() {
        if (this.stopped) return;
        this.stopped = true;
        if (this.heartbeatTimer) clearInterval(this.heartbeatTimer);
        if (this.client && !this.client.destroyed) {
            this.client.end();
            this.client.destroy();
        }
    }
}

const opts = parseArgs();
const worker = new NodeWorker(opts);
worker.start();
