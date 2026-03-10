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
- **haiku** — File searches, find-and-replace, running builds/tests and
  reporting results, simple data gathering. Hand off to sonnet for
  diagnosis and fix decisions.

When in doubt, use sonnet. Reserve opus for tasks that genuinely need
deeper reasoning. Use haiku for monotonous grunt work.

## Session Setup

Run these steps at the start of each session.

### 1. Register your session transcript for shadow mode

Find your session's JSONL transcript file and register it:

```bash
cworkers shadow --session <session-id> --transcript <path-to-transcript.jsonl>
```

Use a unique session identifier (e.g., your working directory basename or a
UUID). Shadow mode tails the transcript and maintains a rolling window of
recent messages. When you dispatch tasks, workers automatically receive this
context.

### 2. Spawn workers

Use the Agent tool to spawn workers. Each worker is a sub-agent whose bash
call blocks on `cworkers worker`:

```bash
cworkers worker --session <session-id> --model opus --timeout 590s
```

Workers are session-scoped: `--session` is required and must match the
session ID used for shadow and dispatch. The broker only routes dispatches
to workers from the same session. This prevents cross-session task leakage.

The worker blocks until it receives a task, then prints it to stdout and
exits. The sub-agent reads the task, executes it, and returns the result.

#### Pool sizing

**Baseline** (always spawn): 1 sonnet. This covers the most common
dispatches — file reads, searches, builds, tests.

Scale up based on the session's workload:

| Session type | Add to baseline | Why |
|---|---|---|
| **Exploration / research** (audit, codebase review) | +1 opus, +1 sonnet | Deep reasoning + parallel searches |
| **Implementation** (coding against a plan) | +1 opus, +1-2 sonnet | Complex changes + tests/builds/mechanical edits |
| **Bulk / parallel** (same pattern across many files) | +2-3 sonnet or haiku | Fan out repetitive work |
| **Light** (conversation-heavy, occasional lookups) | baseline only | Don't waste idle workers |

Rules of thumb:
- Never spawn more workers than you expect to use in the next ~5 minutes —
  they time out at 590s and the context cost is wasted.
- Respawn immediately after a worker completes — don't let the pool go empty.
- Don't idle more than 1 opus at a time — opus context is expensive.
- haiku is only worth it for truly mechanical work (grep-and-report,
  build-and-report). Hand off to sonnet for anything requiring judgement.

The 590s timeout stays within Claude Code's 600s bash tool limit. Workers
reconnect to the broker internally every 60 seconds, so a single call
covers the full window.

### 3. Respawn workers

After a worker completes a task or times out, spawn a replacement. Keep
the pool stocked so dispatches are served instantly.

## Dispatching Tasks

Send a task to a matching worker:

```bash
cworkers dispatch --session <session-id> --model opus "Analyze the error handling in src/api/"
```

- `--session` injects your conversation context into the task
- `--model` routes to a worker with the matching tag
- Omit `--model` for any-available-worker dispatch

The response is `OK` (task delivered) or `NO_WORKERS` (exit code 2). If no
worker is immediately available, the broker waits up to 30 seconds for one
to register.

## Checking Status

```bash
cworkers status
```

Output: `WORKERS: 3 (opus: 1, sonnet: 2), shadows: 1`

For session-scoped status (is my shadow registered? are workers available?):

```bash
cworkers status --session <session-id>
```

Output: `SESSION: my-sess, shadow: true, available_workers: 3 (opus: 1, sonnet: 2)`

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
