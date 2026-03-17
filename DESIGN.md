# cworkers Design Document

## Problem Statement

Claude Code agents run as single-threaded conversations. When a root agent
needs to delegate work (research, analysis, code generation), it must either
do it inline — blocking the conversation — or spawn a sub-agent with
`--tool-use-id`, which carries ~15-18k tokens of overhead per invocation.

cworkers solves the blocking problem: it is a thin MCP-to-CLI bridge that
spawns worker agents on demand. Each `cwork` call spawns a fresh `claude -p`
process, waits for its output, and returns the result. The broker adds SQLite
observability and a Svelte dashboard; it does not manage worker pools or inject
conversation context.

## Architecture

```
 Root Agent Session
   |
   |  MCP tool call: cwork(task, cwd, model?)
   |
   +---> cworkers serve --port 4242     # MCP broker daemon (global, one per user)
   |       - Streamable HTTP on /mcp
   |       - Spawns claude -p worker on demand
   |       - Logs to SQLite for observability
   |       - Svelte dashboard on /dashboard
   |       - HTTP status endpoint on /status
   |
   |  (on each dispatch)
   |     -> spawns fresh claude -p worker with task on stdin
   |     -> sends progress heartbeats every 20s
   |     -> returns worker's result as MCP tool response
```

The system is a single Go binary with two subcommands: `serve` (MCP daemon)
and `status` (active worker query). Workers are `claude -p` processes spawned
on demand — not pre-warmed or pooled.

## MCP Interface

### Tool: cwork

```
cwork(task: string, cwd: string, model?: string) → string
```

- **task**: The prompt to send to the worker. Callers must provide all
  necessary context — workers start fresh with no knowledge of the parent
  conversation.
- **cwd**: Working directory of the calling session. Sets the worker process
  working directory.
- **model**: Model for the worker (default: "sonnet"). Options: sonnet, opus,
  haiku.

The broker:
1. Spawns a fresh `claude -p` worker process.
2. Writes the task to the worker's stdin (prepended with delegation policy
   when depth ≥ 1).
3. Sends progress heartbeats every 20s to prevent MCP client timeout.
4. Returns the worker's result text.

### Status Endpoint

```
GET /status → { "active_workers": N }
```

Also available via CLI: `cworkers status [--port N]`.

## Worker Processes

Workers are `claude -p --verbose --output-format stream-json
--dangerously-skip-permissions` processes. They are spawned on demand by the
broker for each `cwork` call. There is no pool or pre-warming — each dispatch
starts a fresh worker from scratch.

Workers start with no knowledge of the parent conversation. Callers must
include all relevant context in the `task` parameter.

### NDJSON Output Parsing

Workers produce stream-json output. The broker parses NDJSON lines looking
for:
- `"type": "assistant"` messages — accumulated as text parts.
- `"type": "result"` with `"subtype": "success"` — the final result text.
- `"type": "result"` with errors — reported as tool errors.

If a `result` line is found, its text is returned. Otherwise, accumulated
assistant text parts are joined and returned.

## Model Routing

Workers are spawned with the requested model via `--model`. Default model is
"sonnet". This enables routing tasks by complexity: opus for deep reasoning,
sonnet for structured/mechanical work, haiku for monotonous tasks.

## Depth-Controlled Hierarchies

Workers receive `cwork` access at `depth+1` via a synthesised `--mcp-config`
argument. Workers at `maxDepth` (currently 3) are denied `cwork` access
entirely and receive an error. This prevents runaway recursive delegation.

## Progress Heartbeats

Long-running dispatches send `notifications/progress` and
`notifications/message` every 20 seconds via the MCP server context.
This prevents MCP client timeouts during extended worker operations.

## SQLite Observability

The broker logs sessions, workers, and worker output events to SQLite at
`~/.local/share/cworkers/cworkers.db`. The Svelte dashboard at `/dashboard`
reads this data via HTTP API endpoints. Sessions from previous server runs
remain in the DB (not wiped on start) but are marked disconnected on startup.

## Known Limitations

- **No task acknowledgment**: Once the broker writes a prompt to a worker's
  stdin, it considers the task delivered. If the worker crashes mid-
  execution, the task is lost — there is no retry or dead-letter mechanism.

- **No context injection**: Workers start fresh. Callers are responsible for
  providing all context in the task description. This keeps the broker
  stateless but places more burden on the caller to write complete task
  prompts.

- **Port-based, not socket-based**: The MCP server uses HTTP (default port
  4242). Unlike a Unix socket design, this is accessible from any process on
  localhost but does not provide per-user isolation. Multiple users on the
  same host need different ports.
