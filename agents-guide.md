# cworkers Agent Guide

cworkers is a task broker that lets you delegate work to pre-spawned worker
agents over a Unix domain socket. Workers are idle and waiting — dispatched
tasks arrive instantly with no startup overhead. Shadow mode gives workers
automatic awareness of your conversation context.

## Installation

```bash
brew install marcelocantos/tap/cworkers
```

## Setup

Run these steps at the start of your session.

### 1. Start the broker

```bash
pgrep -f "cworkers serve" || cworkers serve &
```

The broker is global (one per user). It listens on
`/tmp/cworkers-<uid>.sock`.

### 2. Register your session transcript for shadow mode

Find your session's JSONL transcript file and register it:

```bash
cworkers shadow --session <session-id> --transcript <path-to-transcript.jsonl>
```

Use a unique session identifier (e.g., your working directory basename or a
UUID). Shadow mode tails the transcript and maintains a rolling window of
recent messages. When you dispatch tasks, workers automatically receive this
context.

### 3. Spawn workers

Spawn workers as background processes. Use `--model` to tag them for routing:

```bash
cworkers worker --model opus --timeout 590s &
cworkers worker --model sonnet --timeout 590s &
```

The 590s timeout stays within Claude Code's 600s bash tool limit. Workers
reconnect internally every 60 seconds, so a single call covers the full
window. Respawn workers after they complete a task or their timeout expires.

## Dispatching Tasks

Send a task to a matching worker:

```bash
cworkers dispatch --session <session-id> --model opus "Analyze the error handling in src/api/"
```

- `--session` injects your conversation context into the task
- `--model` routes to a worker with the matching tag
- Omit `--model` for any-available-worker dispatch

The response is `OK` (task delivered) or `NO_WORKERS` (exit code 2, no
matching worker within the wait period).

If no worker is immediately available, the broker waits up to 30 seconds
for one to register. This handles the gap when a replacement worker is
still spawning.

## Model Routing

Use model tags to route tasks by complexity:

- **opus** — deep reasoning, architectural analysis, novel problem-solving
- **sonnet** — well-scoped coding, mechanical changes, test writing
- Omit `--model` on either side for wildcard matching

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

Run `cworkers --help` for the full flag reference:

| Command | Key Flags |
|---|---|
| `serve` | `--wait <dur>` (dispatch wait timeout, default 30s) |
| `worker` | `--timeout <dur>` (lifetime, default 590s), `--model <name>` |
| `dispatch` | `--model <name>`, `--session <id>` |
| `shadow` | `--session <id>` (required), `--transcript <path>` (required), `--context <N>` (default 50) |
| `unshadow` | `--session <id>` (required) |
| `status` | (no flags) |

Global: `--sock <path>` overrides the default socket path.
