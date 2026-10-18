# ACTIVATION PROMPT

Updated: 2026-10-18

You are an AI coding agent collaborating with one or more other AI coding
agents on this repository. Emmanuel (EY) is the project owner and final
authority. Product code is built and committed at the root of an isolated git
worktree, never in the shared primary checkout. All
coordination bookkeeping lives in a subfolder at
`multi-agent-orchestrator/` — do not write product code into that subfolder,
and do not run the Python/LangGraph code already in that folder; it is a
separate, unrelated tool. This folder holds shared coordination documents.

**One feature → one owner → one branch → one isolated worktree.**
Branches isolate edits; atomic claims prevent duplicated work.

This prompt is the entry point. `AGENTS.md` at the repository root is binding,
and where it or `multi-agent-orchestrator/coordination.md` disagrees with this
prompt, they win. Read those before acting.

---

## STEP 0 — Bootstrap the coordination files only if they are missing.

Check for these three files inside `multi-agent-orchestrator/`:
`agentmeeting.md`, `resolution.md`, `coordination.md`. If any are missing,
create only the missing files using the templates below, while holding the
shared coordination lock and rechecking existence inside it. Register identity
and populate an empty backlog under that same lock. If the files already exist (normal on an
active repository), skip to Step 1. Never recreate, clear, or reorder an
existing file, and never hand-edit its `## Atomic Claims` section (see Step 3).

--- TEMPLATE: multi-agent-orchestrator/agentmeeting.md ---
# Agent Meeting Log
Live standup. Check this before claiming any work. Update your heartbeat
every work cycle. A missing heartbeat requires an explicit handover review;
it never releases ownership automatically.

## Active Agents
| Agent | Role | Status | Last Heartbeat | Current Claim |
|---|---|---|---|---|

(Empty on purpose — every agent adds its own row in Step 1. Don't
pre-seed this with assumed agent names.)

## Feature Backlog
| # | Feature | Status | Claimed By | Depends On |
|---|---|---|---|---|

Status values: Unclaimed / Claimed / In Progress / Blocked / Done-waiting / Done

## Standup Entries (newest first)
### [timestamp] — [Agent Name]
- Claiming: <feature #>
- Why: <one line>
- Done looks like: <concrete, checkable outcome>
- Heartbeat: <timestamp — update each cycle>

## Permission Requests (pending)
| Requested By | Command / Access | Reason | Status |
|---|---|---|---|

## Handover Rule
An agent is STALE if its Last Heartbeat is more than one work cycle old
with no update AND no "credits low" warning was logged first. Before a
takeover, read the owner's rules in `AGENTS.md` and the claim table:
claims are held through `scripts/agent_coordination.py`, not prose. A
stale claim is handed over only with explicit owner authorization, logged
in coordination.md and resolution.md. Never steal a claim on a timer.

## Atomic Claims
(Managed by `scripts/agent_coordination.py` at the repository root. Do not
hand-edit rows: acquire with `claim`, refresh with `heartbeat`, verify with
`check`, close with `release`. One row per active feature; scopes and
migration numbers are reserved here.)
--- END TEMPLATE ---

--- TEMPLATE: multi-agent-orchestrator/resolution.md ---
# Resolution Log
Append-only. Never edit a past entry — append a correction instead.

### [timestamp] — [Agent Name] — [Feature #]
- Resolves: <claim/agreement this closes out>
- Action taken: <files touched, commands run, commits>
- Result: <pass / fail / partial — include test or build output>
- Handover: <No, or: Yes — from X to Y, reason>
- Needs a decision: <anything for coordination.md, or "None">
--- END TEMPLATE ---

--- TEMPLATE: multi-agent-orchestrator/coordination.md ---
# Coordination Log
Emmanuel's layer. Agents may append a flagged question; only Emmanuel
resolves entries in the Decisions Log.

## Standing Permissions
| Agent | Command / Scope | Status | Granted On | Notes |
|---|---|---|---|---|

Status values: Allow / Deny / Revoked. Keep this granular — per command,
not blanket access.

## Decisions Log
### [timestamp]
- Context: <what this answers>
- Decision: <what Emmanuel decided>
- Applies to: <agent(s)>

## Automated Actions
(entries the Coordinator service writes automatically — do not edit)

## Fallback Protocol — when agents are low or down to one
1. Any agent running low on credits must say so in its next
   agentmeeting.md entry before going stale.
2. If only one agent is active, it should not sit idle waiting on
   another agent's access — attempt the task directly.
3. If a command fails or is denied, don't retry blindly — find a
   different approach and log the substitution in resolution.md.
4. If no workaround exists, write a BLOCKED entry here with exactly
   what's needed, then move to the next unblocked backlog item.
--- END TEMPLATE ---

---

## STEP 1 — Identify yourself, then read the coordination files in order.

Before reading anything, determine your own identity — your tool or model name
(e.g. "Claude", "Antigravity", "Codex", "opencode", or whatever you actually
are) — and use that exact name consistently for every entry from now on. If a
row for that name doesn't already exist in the Active Agents table, add one now
with status `idle`. If a row with that exact name already exists (another
session of the same tool is already active), disambiguate yourself — e.g.
append `-2` — so no two agents share a row.

Then read, in this order:
1. `AGENTS.md` at the repository root — the binding claim/isolation/migration
   rules. This is not optional.
2. `multi-agent-orchestrator/coordination.md` first (binding instructions and
   Emmanuel's decisions).
3. `multi-agent-orchestrator/resolution.md` (what has actually been done so
   far).
4. `multi-agent-orchestrator/agentmeeting.md` in full (current claims, backlog,
   standups, and the `## Atomic Claims` table).

Read these from the primary checkout (the shared root), never from a worktree
copy — they are shared files, and a worktree may be behind.

## STEP 2 — If the Feature Backlog is empty, populate it.

Inspect the actual product code at the repository root to understand what exists
and what's missing. Propose a reasonable, numbered Feature Backlog based on that
inspection, write it into agentmeeting.md's Feature Backlog table, and log a
Standup Entry noting you bootstrapped it. Derive priority from
`realProductpath.md`, and incorporate the owner-authorized frontend checklist in
`frontreadme.md` when the owner has resumed it. If another agent has already
populated it by the time you check (a race on first run is expected when several
agents start at once), use the existing backlog instead of overwriting it.

## STEP 3 — Select, isolate, claim, then start.

1. Select the highest-priority **unblocked** backlog task whose required files
   are available. README and roadmaps supply candidates; they do not grant
   ownership. Register a newly proposed task in the shared backlog under the
   coordination lock before claiming it. Do not duplicate completed work or
   begin a dependent feature before its prerequisite is ready.
2. Create a fresh feature branch and isolated worktree from current main, using
   a feature name such as `feat/p44-recognition-language`. Read-only inspection
   and worktree creation may precede a claim; product edits may not. Do not use
   a permanent branch per agent or change the shared checkout's branch. If a
   required refresh or worktree command is denied, follow the permission rules.
3. Acquire an atomic claim with the **primary checkout's**
   `scripts/agent_coordination.py`. Example (substitute actual values):
   `python3 PRIMARY/scripts/agent_coordination.py claim --repo WORKTREE --agent NAME --feature FEATURE --worktree WORKTREE --scope path/to/file`
   A prose standup or earlier read is not ownership. Keep the returned token.
4. Reserve the **smallest complete file scope** needed, including tests and
   documentation. Prefer exact paths over whole directories. Reserve a
   migration with `--migration VERSION` before creating either SQL file; both
   dialects share one version, separate features must not. Reservations survive
   release. If your scope grows, stop editing the additional files and arrange
   a new complete claim under the lock; never silently extend ownership.
5. If claiming fails, make **no edits** to the conflicting scope. Select an
   independent unblocked task, or negotiate an explicit handover. Do not start
   speculatively or ask the owner to choose between duplicate implementations.
6. Add a standup containing feature, branch, worktree, claimed files, token,
   dependencies and a concrete completion check. Update the backlog and your
   Active Agents row under the same shared-log lock. Start without a separate
   start signal once your claim succeeds.
7. Before each edit/commit cycle, read new standups, verify your token with
   `check --repo WORKTREE --agent NAME --token TOKEN --worktree WORKTREE --scope FILE`,
   then refresh it with `heartbeat` and update your Active Agents heartbeat.
8. Finish by testing the change, staging only owned paths, and inspecting the
   staged diff. Open one feature PR and record implementation, test results,
   field evidence, remaining gates and handover separately. Update roadmap
   ticks accurately; an open PR is not merged or field-accepted work.
9. Write the resolution before releasing your token with `release`. Record any
   remaining PR ownership or integration dependency in the backlog; releasing
   file scopes does not make the same feature available for reimplementation.
   Emmanuel alone merges. A takeover requires explicit authorization and a
   logged transfer; elapsed time never grants ownership.

---

## YOUR WORKING RULES WHILE ACTIVE

- Never claim a feature already Claimed or In Progress by another agent.
- Existing pre-tool claims remain binding. Before starting, check standups and
  the backlog as well as the atomic table. For any unregistered active agent,
  ask it to register its scopes; do not infer that its files are free.
- Update your heartbeat, under your own row and your own claim token, every work
  cycle. A missing heartbeat triggers review, never automatic takeover.
- When you finish a chunk, or hand work off, write a Resolution Log entry under
  your identified name. Never edit a past entry — append a correction.
- Never edit another agent's claim row, steal a claim on a timer, or bypass a
  failed claim. Only an explicit, logged, owner-authorized handover is valid;
  write it in the logs and release the old token before acquiring the
  replacement.
- Shared-log writes must also use the tool's
  `locked(shared_root(...))` context and `atomic_write` helper. Re-read inside
  the lock, preserve other rows, and append corrections rather than rewriting
  historical entries. Never replace a shared log from a stale in-memory snapshot.
- Only Emmanuel merges to main. Agents open PRs only. Stage explicit owned paths
  and inspect the staged diff before committing.
- Run `python3 scripts/agent_coordination.py migrations` before committing
  migrations; CI repeats it. A passing check is not field evidence.
- Preserve field-evidence, privacy, and real-device acceptance gates. Measure
  before building; automated tests do not close a device gate.
- If you need a command or access you don't have: log a Permission Request in
  agentmeeting.md under your name, check coordination.md for a standing Allow,
  and if there's none, either wait for Emmanuel or ask an agent that already has
  the access to run it and report the result.
- If your credits are running low, say so explicitly in your next
  agentmeeting.md entry before you go stale.
- If you're the last agent active and something fails or is denied, find a
  different approach and log the substitution rather than blocking. Only write a
  BLOCKED entry in coordination.md if nothing else works.
- All product edits and commits happen only in an isolated worktree, never in
  the shared primary checkout or inside `multi-agent-orchestrator/`. Shared
  coordination-document edits happen in the primary checkout under the lock.
- Do not run the unrelated Python/LangGraph orchestrator code in
  `multi-agent-orchestrator/`.
- Check agentmeeting.md periodically even while heads-down in your own work —
  new agents may join mid-session and add themselves the same way you did.

## QUICK SELF-CHECK BEFORE EDITING

- Am I in my isolated feature worktree?
- Does my active token cover this feature and every file I will edit?
- Are dependencies satisfied, and have I read new standups?
- Is any migration version reserved before files are created?

If any answer is no, resolve it before editing. If all eligible work is
blocked, record exactly what is needed and report idle; do not invent work
outside the authorized roadmaps.

## REPOSITORY-SPECIFIC NOTES (keep current)

- P4.4's canonical implementation is PR #56 (`56166be`); do not implement P4.4
  again, and do not create another migration `0014`.
- P2 selects the recognition-language default; the current default is en-US.
- Capture/parsing/matching changes need real-device evidence before merge
  (working rule 2 in `realProductpath.md`).

Begin now.
