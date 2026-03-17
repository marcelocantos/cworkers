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
   |  MCP tool call: cwork(task, cwd, model?)
   |
   +---> cworkers serve --port 4242     # MCP broker daemon (global, one per user)
   |       - Streamable HTTP on /mcp
   |       - Shadow registry (per-cwd, auto-discovered transcripts)
   |       - Worker pool (pre-spawned claude -p processes)
   |       - HTTP status endpoint on /status
   |
   |  (on first cwork call for a cwd)
   |     -> auto-discovers transcript: ~/.claude/projects/<encoded-cwd>/*.jsonl
   |     -> registers shadow, begins tailing
   |
   |  (on each dispatch)
   |     -> takes idle worker from pool (or spawns cold)
   |     -> injects shadow context + task into worker's stdin
   |     -> spawns replacement worker in background (pre-warming)
   |     -> sends progress heartbeats every 20s
   |     -> returns worker's result as MCP tool response
```

The system is a single Go binary with two subcommands: `serve` (MCP daemon)
and `status` (pool/shadow query). Workers are `claude -p` processes managed
by the broker — not a cworkers subcommand.

## MCP Interface

### Tool: cwork

```
cwork(task: string, cwd: string, model?: string) → string
```

- **task**: The prompt to send to the worker.
- **cwd**: Working directory of the calling session. Used for shadow lookup
  and worker process working directory.
- **model**: Model for the worker (default: "sonnet"). Options: sonnet, opus.

The broker:
1. Auto-registers a shadow for the cwd if one doesn't exist (discovers
   transcript from `~/.claude/projects/<encoded-cwd>/`).
2. Takes an idle worker from the pool, or spawns one cold.
3. Spawns a replacement worker in the background (pre-warming).
4. Injects shadow context + task into the worker's stdin.
5. Sends progress heartbeats every 20s to prevent MCP client timeout.
6. Returns the worker's result text.

On first use for a cwd, appends a setup hint suggesting the CLAUDE.md
directive to ensure agents always delegate.

### Status Endpoint

```
GET /status → { "workers": N, "models": {...}, "shadows": N }
```

Also available via CLI: `cworkers status [--port N]`.

## Shadow Mode

Shadows are registered implicitly on first `cwork` call per working
directory. The broker discovers the most recently modified `.jsonl`
transcript in `~/.claude/projects/<encoded-cwd>/` and begins tailing it.

The `shadowRegistry` maps cwds to shadow instances. Each shadow tails its
JSONL transcript with a rolling window of recent user/assistant messages
(default 50 lines). Thread-safe with double-checked locking on registration.

When dispatching, the broker prepends shadow context:

```
=== CONVERSATION CONTEXT (recent messages from root session) ===
[User]: ...
[Assistant]: ...
=== END CONTEXT ===

TASK: <the actual task>
```

This gives workers awareness of the root conversation without the root
agent needing to summarize or repeat anything.

### Transcript Discovery

The encoded cwd path replaces `/`, `.`, and `_` with `-` and prepends `-`,
matching Claude Code's project directory naming convention. The broker
scans for `.jsonl` files and selects the most recently modified one.

## Worker Processes

Workers are `claude -p --verbose --output-format stream-json
--dangerously-skip-permissions` processes. They are spawned by the broker
(not by the user or root agent) and managed as a pool keyed by
`cwd + model`.

### Pre-warming

After each dispatch, the broker spawns a replacement worker in the
background. This ensures the next dispatch finds an idle worker ready —
no startup delay. The pool is demand-driven: workers are only spawned
for cwd/model combinations that have been used.

### NDJSON Output Parsing

Workers produce stream-json output. The broker parses NDJSON lines looking
for:
- `"type": "assistant"` messages — accumulated as text parts.
- `"type": "result"` with `"subtype": "success"` — the final result text.
- `"type": "result"` with errors — reported as tool errors.

If a `result` line is found, its text is returned. Otherwise, accumulated
assistant text parts are joined and returned.

## Model Routing

Workers register with a model tag. The pool is keyed by `cwd + "\x00" +
model`. Dispatches request a specific model; the broker matches exactly.
Default model is "sonnet".

This enables routing tasks by complexity: opus for deep reasoning, sonnet
for structured/mechanical work.

## Progress Heartbeats

Long-running dispatches send `notifications/progress` and
`notifications/message` every 20 seconds via the MCP server context.
This prevents MCP client timeouts during extended worker operations.

## Known Limitations

- **No task acknowledgment**: Once the broker writes a prompt to a worker's
  stdin, it considers the task delivered. If the worker crashes mid-
  execution, the task is lost — there is no retry or dead-letter mechanism.

- **Shadow consistency**: Two dispatches close together may receive slightly
  different context snapshots, since the tailer runs asynchronously. This
  is acceptable for the use case (conversational awareness, not
  transactional consistency).

- **Single-transcript-per-cwd**: If two Claude Code sessions run in the
  same project directory simultaneously, only the most recently modified
  transcript is tailed. The race window is acceptably narrow for normal use.

- **Port-based, not socket-based**: The MCP server uses HTTP (default port
  4242). Unlike the previous Unix socket design, this is accessible from
  any process on localhost but does not provide per-user isolation.
  Multiple users on the same host need different ports.
