# cworkers Operational Guide

cworkers is a task broker that lets you delegate work to pre-spawned worker
agents over a Unix domain socket. Workers are idle and waiting — dispatched
tasks arrive instantly with no startup overhead. Shadow mode gives workers
automatic awareness of your conversation context.

The broker runs as a brew service — it is always available.

## When to Delegate

Delegate to a worker when the task is **independent** — it doesn't need
your conversation's back-and-forth, and you can continue other work while
it runs. Good candidates:

- **Research**: Exploring a codebase area, reading docs, gathering context
- **Bulk changes**: Applying the same pattern across multiple files
- **Testing**: Running test suites, diagnosing failures
- **Code review**: Auditing a module, checking for issues
- **Parallel implementation**: Building independent features or components

Do **not** delegate when:
- The task requires interactive decisions with the user
- The result is needed immediately before you can continue
- The task is trivial (faster to do inline than to dispatch)

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
cworkers worker --model opus --timeout 590s
```

The worker blocks until it receives a task, then prints it to stdout and
exits. The sub-agent reads the task, executes it, and returns the result.

Spawn a mix of workers based on expected workload — e.g., 1 opus + 2 sonnet
for a typical session. Adjust as needed.

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

## Cleanup

When your session ends, remove the shadow registration:

```bash
cworkers unshadow --session <session-id>
```

## Reference

| Command | Key Flags |
|---|---|
| `serve` | `--wait <dur>` (dispatch wait timeout, default 30s) |
| `worker` | `--timeout <dur>` (lifetime, default 590s), `--model <name>` |
| `dispatch` | `--model <name>`, `--session <id>` |
| `shadow` | `--session <id>` (required), `--transcript <path>` (required), `--context <N>` (default 50) |
| `unshadow` | `--session <id>` (required) |
| `status` | (no flags) |

Global: `--sock <path>` overrides the default socket path.
