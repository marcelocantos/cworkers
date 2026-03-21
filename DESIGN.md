# cworkers Design Document

## Problem Statement

Claude Code agents run as single-threaded conversations. When a root agent
needs to delegate work (research, analysis, code generation), it must either
do it inline — blocking the conversation — or spawn a sub-agent with
`--tool-use-id`, which carries ~15-18k tokens of overhead per invocation.

cworkers solves the blocking problem: `cwork` is a 53KB stdio MCP server
that spawns worker agents on demand. Each `cwork` call spawns a fresh
`claude -p` process, waits for its output, and returns the result. Workers
run concurrently via pthreads.

## Architecture

```
 Claude Code session
   |
   |  stdio MCP (JSON-RPC)
   |
   +---> cwork                        # 53KB C binary
   |       - Reads JSON-RPC from stdin
   |       - Spawns claude -p workers via posix_spawnp
   |       - Concurrent dispatch (pthread per cwork call)
   |       - Streams progress as MCP notifications
   |       - Logs to NDJSON files for observability
   |       - Returns result as MCP tool response
   |
   |  (optional)
   |
   +---> cdash                        # Go TUI dashboard
           - Watches activity.jsonl via fsnotify
           - Renders worker transcripts with glamour
```

There is no daemon. `cwork` is spawned by Claude Code as a stdio MCP server
subprocess. It inherits the parent's environment (proxy vars, CA certs, API
keys) and passes them to workers.

## MCP Interface

### Tool: cwork

```
cwork(task: string, cwd: string, model?: string) → string
```

- **task**: The prompt to send to the worker. Callers must provide all
  necessary context — workers start fresh with no knowledge of the parent
  conversation.
- **cwd**: Working directory for the worker process.
- **model**: Model for the worker (default: "sonnet"). Options: sonnet, opus,
  haiku.

## Worker Processes

Workers are `claude -p --verbose --output-format stream-json` processes
spawned via `posix_spawnp`. Each `cwork` call spawns a fresh worker — there
is no pool or pre-warming.

### NDJSON Output Parsing

Workers produce stream-json output. `cwork` parses NDJSON lines looking for:
- `"type": "assistant"` messages — text and tool_use blocks forwarded as
  progress notifications
- `"type": "result"` with `"subtype": "success"` — the final result text
- `"type": "result"` with errors — reported as tool errors

### Heartbeat Thread

A pthread sends periodic heartbeat events (every 10s) to the activity log
while a worker is running. The dashboard uses these to detect crashed workers
(no heartbeat for >30s = stale).

## Observability

### Activity Log

`~/.local/share/cworkers/activity.jsonl` — lifecycle events for all workers:
```json
{"ts":"...","id":"S6w52nk","event":"start","model":"sonnet"}
{"ts":"...","id":"S6w52nk","event":"heartbeat"}
{"ts":"...","id":"S6w52nk","event":"done"}
```

Safe for concurrent writers (`O_APPEND` + `writev`). Worker IDs are
`<model_prefix><6 random base36 chars>` (e.g., `S6w52nk` for sonnet,
`Okraccr` for opus, `Hc0j6r6` for haiku).

### Per-Worker Logs

`~/.local/share/cworkers/workers/<id>.jsonl` — full detail per worker:
```json
{"ts":"...","event":"task","data":"the full task prompt"}
{"ts":"...","event":"progress","data":"## Reading files"}
{"ts":"...","event":"tool_use","data":"Read"}
{"ts":"...","event":"result","data":"the worker's output"}
```

Only one process writes to each file. Arbitrarily large task descriptions
are supported.

### Dashboard (cdash)

Go TUI using bubbletea + glamour. Sidebar shows worker IDs (color-coded by
status), main panel renders the selected worker's transcript as markdown.
Watches activity.jsonl via fsnotify for live updates.

## Environment Passthrough

`cwork` captures env vars matching `ANTHROPIC_*`, `CLAUDE_*`, `AWS_*`,
`*_PROXY`, `*_proxy`, and `NODE_EXTRA_CA_CERTS` from its environment and
passes them to spawned workers. This supports corporate environments with
custom proxy/TLS/API configurations.

## Known Limitations

- **No task acknowledgment**: If a worker crashes mid-execution, the task is
  lost — there is no retry or dead-letter mechanism.

- **No context injection**: Workers start fresh. Callers are responsible for
  providing all context in the task description.

- **Worker ID collisions**: IDs use 6 random base36 chars from `/dev/urandom`.
  Collision probability is negligible for normal use (~2B possible IDs).
