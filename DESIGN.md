# cworkers Design Document

## Problem Statement

Claude Code agents run as single-threaded conversations. When a root agent
needs to delegate work (research, analysis, code generation), it must either
do it inline — blocking the conversation — or spawn a sub-agent with
`--tool-use-id`, which carries ~15-18k tokens of overhead per invocation
and has no awareness of the parent conversation's context.

cworkers solves both problems: it pre-spawns idle worker agents that
receive tasks instantly (no startup cost) and injects recent conversation
context automatically (shadow mode), so workers understand what the root
session is doing without the root paying the token cost of inline work.

## Architecture

```
 Root Agent Session
   |
   |  (spawns at session start)
   |
   +---> cworkers serve                # broker process
   |       - Unix socket listener
   |       - transcript tailer (shadow)
   |       - worker pool + dispatch queue
   |
   +---> agent: cworkers worker --model opus    # pre-spawned workers
   +---> agent: cworkers worker --model opus    #   (idle, waiting)
   +---> agent: cworkers worker --model sonnet
   +---> agent: cworkers worker --model sonnet
   |
   |  (when root needs to delegate)
   |
   +---> cworkers dispatch --model opus "Analyze the code in src/"
            -> broker matches to pooled opus worker
            -> worker receives task + conversation context
            -> worker executes, returns result to root via tool output
```

The entire system is a single Go binary (zero dependencies) with four
subcommands: `serve`, `worker`, `dispatch`, `status`.

## Protocol

All communication uses a line-based text protocol over a Unix domain socket
(`/tmp/cworkers-<uid>.sock` by default).

### Worker Registration

```
Client: WORKER <model>\n
Server: (connection held open until task arrives or timeout)
Server: <context>\n\nTASK: <task body>   (on dispatch)
Server: (connection closed cleanly on timeout)
```

### Task Dispatch

```
Client: DISPATCH <model>\n<task body>
Client: (half-close write side)
Server: OK\n          (task delivered)
Server: NO_WORKERS\n  (no matching worker within wait period)
```

### Status Query

```
Client: STATUS\n
Server: WORKERS: 4 (opus: 2, sonnet: 2), shadow: 15234 bytes\n
```

## Shadow Mode

When started with `--transcript <path>`, the broker tails the root session's
JSONL transcript file and maintains a rolling window of recent user/assistant
messages (default 50, configurable via `--context`).

When dispatching a task, the broker prepends this context:

```
=== CONVERSATION CONTEXT (recent messages from root session) ===
[User]: ...
[Assistant]: ...
=== END CONTEXT ===

TASK: <the actual task>
```

This gives workers awareness of the root conversation — what was discussed,
what decisions were made, what the user cares about — without the root agent
needing to summarize or repeat anything.

The tailer uses manual byte-offset tracking (not `Seek`-based) to avoid a
partial-line duplication bug identified during architecture review. Incomplete
lines are buffered across reads.

## Model Routing

Workers register with a model tag (e.g., `opus`, `sonnet`). Dispatches can
request a specific model via `--model`. The broker matches by exact string
equality; an empty model on either side acts as a wildcard.

This enables the root to maintain a mixed pool and route tasks by complexity:
opus for deep reasoning, sonnet for structured/mechanical work.

## Dispatch Queue

If no matching worker is available at dispatch time, the broker does not fail
immediately. Instead, it queues the dispatch request and waits (default 30s,
configurable via `--wait`) for a worker to register. When a matching worker
connects, the broker delivers the queued task directly — the worker never
enters the pool.

This eliminates the race condition where a replacement worker is still
spawning when a new task arrives.

## Worker Reconnect Loop

The worker binary loops internally with a 60-second reconnect interval,
staying within a single bash tool call (max 600s). On each iteration it
connects to the broker, registers, and waits up to 60s for a task. If none
arrives, it disconnects and reconnects — keeping connections short-lived
(which also prevents ghost workers). The agent sees nothing during this
cycling; it either gets a task printed to stdout or the process exits cleanly.

## Known Limitations

- **600s hard ceiling**: The bash tool timeout caps worker lifetime at ~10
  minutes. Workers must be respawned by the root agent after expiry.

- **Socket path is per-user, not per-session**: `/tmp/cworkers-<uid>.sock`
  avoids collisions between users but not between concurrent sessions of the
  same user. Use `--sock` to disambiguate.

- **No task acknowledgment**: Once the broker writes a task to a worker
  connection, it considers the task delivered. If the worker crashes mid-
  execution, the task is lost — there is no retry or dead-letter mechanism.

- **Stale worker detection is reactive**: The broker discovers dead workers
  only when attempting to write a task to them. The 60s reconnect interval
  bounds staleness but does not eliminate it.

- **Shadow consistency**: Two dispatches close together may receive slightly
  different context snapshots, since the tailer runs asynchronously. This
  is acceptable for the use case (conversational awareness, not
  transactional consistency).

- **Transcript discovery is manual**: The root agent must know and pass the
  transcript file path. There is no automatic discovery of the active
  session's JSONL file.
