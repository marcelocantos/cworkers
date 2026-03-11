# cworkers Operational Guide

cworkers is a task broker that lets you delegate work to pre-spawned worker
agents over a Unix domain socket. Workers are idle and waiting — dispatched
tasks arrive instantly with no startup overhead. Shadow mode gives workers
automatic awareness of your conversation context.

The broker runs as a brew service — it is always available.

## When to Delegate

**Default to delegating.** Every tool call, file read, search, build, or
test you run in the root session grows your context window and brings you
closer to compression or session death. Workers absorb that cost instead.

Delegate aggressively:
- **Any file reads or searches** — send a worker to explore and summarise
- **Code changes** — describe what to change, let a worker implement it
- **Builds and tests** — workers run them and report results
- **Research** — codebase exploration, doc reading, dependency analysis
- **Bulk work** — applying patterns across files, migrations, refactors

The only things that **must** stay in the root session:
- Direct conversation with the user (clarifying questions, presenting options)
- Orchestration decisions (what to do next based on worker results)
- Trivial operations under ~100 tokens (not worth the dispatch overhead)

Everything else should be dispatched. If you're about to read a file or run
a command, ask yourself: can a worker do this instead? Usually yes.

## Model Selection

- **opus** — Complex reasoning, architectural decisions, novel problem-solving,
  deep code analysis, tasks where getting it right matters more than speed.
- **sonnet** — Well-scoped coding tasks, mechanical changes across files,
  writing tests, running builds, triaging errors, anything with clear
  structure and bounded scope.

When in doubt, use sonnet. Reserve opus for tasks that genuinely need
deeper reasoning.

Examples:
- **opus**: designing a new module's API, debugging a subtle race
  condition, reviewing architecture trade-offs, writing a complex
  algorithm from scratch.
- **sonnet**: implementing a function from a spec, running tests and
  reporting results, applying a pattern across files, reading code
  and summarising findings.

## Session Setup

At the start of each session, register your transcript for shadow mode:

```bash
SESSION_ID=$(cworkers shadow)
```

This auto-discovers your transcript (by finding the most recently modified
JSONL in your Claude Code project directory) and prints a session ID. Use
`$SESSION_ID` for all subsequent `dispatch`, `worker`, `status`, and
`unshadow` calls.

Shadow mode tails the transcript and maintains a rolling window of recent
messages. When you dispatch tasks, workers automatically receive this context.

If auto-discovery fails (e.g., non-standard setup), use explicit flags:
```bash
cworkers shadow --session <id> --transcript <path>
```

**Always decide to delegate as if a worker is available.** Don't check
pool status before deciding *whether* to delegate — only check it to
choose *how*: dispatch (if a worker is idle) or spawn a regular Agent
(if the pool is cold). Either way, the work gets delegated and the
self-warming chain starts or continues.

This matters because the pool can be empty at session start or after an
idle timeout. If you gate delegation decisions on pool status, work stays
in the root session unnecessarily and the pool never warms up.

## Dispatching Tasks

First decide *whether* to delegate (see "When to Delegate" above), then
decide *how*:

1. **Try dispatching**: `cworkers dispatch --session <session-id> --model sonnet "your task"`
2. **If OK**: done — the worker handles it.
3. **If NO_WORKERS** (exit code 2): spawn a regular Agent with the same
   task. The agent seeds the pool before doing its work (see below).

- `--session` injects your conversation context into the task
- `--model` routes to a worker with the matching tag
- Omit `--model` for any-available-worker dispatch

The response is `OK` (task delivered) or `NO_WORKERS` (exit code 2). If no
worker is immediately available, the broker waits up to 30 seconds for one
to register.

## Self-Warming Pool

The pool maintains itself through a single invariant: **every new instance
(agent or worker) spawns a replacement worker before doing its own work.**

The sequence:

1. **First dispatch** — no idle worker exists, so the root session spawns a
   regular Agent.
2. **Agent starts** — its first action is to spawn a background worker via
   the Agent tool:
   ```bash
   cworkers worker --session <session-id> --model sonnet --timeout 60s
   ```
   Then it proceeds with its actual task.
3. **Next dispatch** — the idle worker from step 2 receives it instantly
   (no startup cost). The worker's agent spawns another background worker
   before executing the task.
4. **Repeat** — each completed task seeds the next idle worker.
5. **Idle timeout** — if no dispatch arrives within 60s, the worker expires
   and the pool drains to zero. No tokens wasted.

This is self-perpetuating: busy sessions always have a warm worker ready;
idle sessions cost nothing.

### Rules

- Use `--timeout 60s` for replacement workers (one idle cycle, then die).
- Match the replacement worker's `--model` to the current agent's model —
  if you're an opus agent, spawn an opus worker.
- **One new per one used** — each consumed worker spawns exactly one
  replacement. Don't spawn extras speculatively.
- The root session only spawns a regular Agent when no idle worker exists.
  Once the chain starts, workers sustain themselves.
- **Burst parallelism** (skills like `/cv` that fan out many agents at
  once): use regular Agent spawning, not cworkers. The slight startup
  latency is outweighed by the volume of work being performed. The
  self-warming pool is designed for steady-state individual dispatches,
  not sudden bursts.
- **Team agents** do not participate in the self-warming pool. Teams
  manage their own lifecycle — don't spawn replacement workers from
  team agents.

## Checking Status

```bash
cworkers status
```

Output: `WORKERS: 3 (opus: 1, sonnet: 2), shadows: 1`

For session-scoped status (is my shadow registered? are workers available?):

```bash
cworkers status --session <session-id>
```

Output: `SESSION: my-sess, shadow: true, available_workers: 1 (sonnet: 1)`

## Cleanup

When your session ends, remove the shadow registration:

```bash
cworkers unshadow --session <session-id>
```

## Reference

| Command | Key Flags |
|---|---|
| `serve` | `--wait <dur>` (dispatch wait timeout, default 30s) |
| `worker` | `--session <id>` (required), `--timeout <dur>` (lifetime, default 590s), `--model <name>` |
| `dispatch` | `--model <name>`, `--session <id>` |
| `shadow` | `--session <id>` (required), `--transcript <path>` (required), `--context <N>` (default 50) |
| `unshadow` | `--session <id>` (required) |
| `status` | `--session <id>` (optional, session-scoped status) |

Global: `--sock <path>` overrides the default socket path.
