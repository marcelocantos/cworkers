# Stability

## Stability Commitment

Version 1.0 represents a backwards-compatibility contract. After 1.0,
breaking changes to the CLI interface, MCP tool parameters, HTTP API,
configuration, or output formats require a major version bump (which in
practice means forking to a new product). The pre-1.0 period exists to
get these right.

## Interaction Surface Catalogue

Snapshot as of v0.13.0.

### CLI Subcommands

| Subcommand | Stability | Notes |
|---|---|---|
| `serve` | Stable | Core broker lifecycle. |
| `status` | Stable | Simple query; hits `/status` HTTP endpoint. |

### CLI Flags

| Flag | Scope | Type | Default | Stability |
|---|---|---|---|---|
| `--version` | global | bool | — | Stable |
| `--help` / `-h` | global | bool | — | Stable |
| `--help-agent` | global | bool | — | Stable |
| `--port <N>` | global | int | `4242` | Stable |
| `--debug` | global | bool | false | Needs review — internal/diagnostic; may be removed or gated. |

### Configuration File

`~/.config/cworkers/config.json` — optional, read at startup.

| Field | Type | Default | Stability |
|---|---|---|---|
| `claude_path` | string | `"claude"` (PATH lookup) | Needs review — new in v0.13.0. |

### MCP Tool: `cwork`

Delivered via Streamable HTTP at `http://localhost:<port>/mcp`.

| Parameter | Type | Required | Default | Stability |
|---|---|---|---|---|
| `task` | string | yes | — | Stable |
| `cwd` | string | yes | — | Stable |
| `model` | string | no | `"sonnet"` | Stable — values: `sonnet`, `opus`, `haiku`. |

Return value: tool result string containing the worker's output text, or
a tool error if the worker failed or max depth was exceeded.

Stability: **Stable** for the parameter surface. Return format (plain
text) is stable; error message wording is not.

### Depth-Controlled Worker Access

Workers receive `cwork` access at `depth+1` via a synthesised `--mcp-config`
argument. The URL carries `depth=N` and `wid=<parent-display-name>` query
parameters. Workers at `maxDepth` (currently 3) are denied `cwork` access
entirely and receive an error.

| URL query param | Meaning | Stability |
|---|---|---|
| `depth` | Delegation depth (0 = root) | Needs review — hardcoded constant, value may change. |
| `wid` | Parent worker display name (for hierarchy labelling) | Needs review — internal; may be renamed. |

### HTTP API Endpoints

All endpoints are served on `http://localhost:<port>/`.

| Endpoint | Method | Description | Stability |
|---|---|---|---|
| `/mcp` | GET/POST | MCP Streamable HTTP transport | Stable |
| `/status` | GET | JSON pool/shadow summary | Stable — see response shape below. |
| `/dashboard` | GET | Svelte dashboard (single-file HTML) | Needs review — UI evolving. |
| `/api/sessions` | GET | JSON array of session rows | Needs review — fields may grow. |
| `/api/workers` | GET | JSON array of worker rows | Needs review — fields may grow. |
| `/api/workers/{id}/events` | GET | JSON array of events for one worker | Needs review — new. |
| `/api/events` | GET | SSE stream of live lifecycle and worker events | Needs review — event set evolving. |
| `POST /api/open` | POST | Opens a file in the local editor (dashboard action) | Needs review — internal/dashboard. |
| `GET /api/home` | GET | Returns the user's home directory path | Needs review — internal/dashboard. |

#### `/status` Response Shape

```json
{ "workers": N, "models": { "<model>": N, ... }, "shadows": N }
```

Stability: **Stable** for existing fields; new fields may be added.

#### `/api/sessions` Row Shape

```json
{
  "id": "<uuid>",
  "client_name": "...",
  "client_version": "...",
  "cwd": "...",
  "transcript": "<jsonl-filename>",
  "depth": N,
  "connected_at": "<RFC3339Nano>",
  "disconnected_at": "<RFC3339Nano>"   // omitted if still connected
}
```

#### `/api/workers` Row Shape

```json
{
  "id": "<uuidv7>",
  "session_id": "<uuid>",             // omitted if no session
  "parent_id": "<display-name>",      // omitted if root worker
  "display_name": "w1.2.3",
  "cwd": "...",
  "model": "sonnet|opus|haiku",
  "task": "...",
  "status": "running|done|error",
  "started_at": "<RFC3339Nano>",
  "ended_at": "<RFC3339Nano>"         // omitted if running
}
```

#### `/api/workers/{id}/events` Entry Shape

```json
{
  "id": N,
  "type": "...",
  "data": "...",
  "created_at": "<RFC3339Nano>"
}
```

#### `/api/events` SSE Stream

Server-Sent Events; each frame is a JSON object on a `data:` line.

| Event name | Payload fields | Trigger |
|---|---|---|
| `hello` | — | Client connects |
| `session_start` | `session` (row) | MCP session connects |
| `session_update` | `session` (partial row: id, cwd, transcript) | CWD/transcript discovered |
| `session_end` | `id` | MCP session disconnects |
| `worker_start` | `worker` (row) | `cwork` call begins dispatch |
| `worker_done` | `id`, `status` | Worker finishes or errors |
| `worker_event` | `id`, `entry` (event row) | Worker emits output line (heading-level) |

Stability: **Needs review** — event names and payload shapes are new and
may evolve as the dashboard matures.

### SQLite Schema

Database at `~/.local/share/cworkers/cworkers.db`. WAL mode, 5 s busy
timeout.

```sql
CREATE TABLE sessions (
    id               TEXT PRIMARY KEY,
    client_name      TEXT NOT NULL DEFAULT '',
    client_version   TEXT NOT NULL DEFAULT '',
    cwd              TEXT NOT NULL DEFAULT '',
    transcript       TEXT NOT NULL DEFAULT '',
    depth            INTEGER NOT NULL DEFAULT 0,
    connected_at     TEXT NOT NULL,
    disconnected_at  TEXT
);

CREATE TABLE workers (
    id           TEXT PRIMARY KEY,       -- UUIDv7
    session_id   TEXT REFERENCES sessions(id),
    parent_id    TEXT,                   -- display name of parent worker
    display_name TEXT NOT NULL,          -- e.g. "w1.2"
    cwd          TEXT NOT NULL,
    model        TEXT NOT NULL,
    task         TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'running',  -- running|done|error
    started_at   TEXT NOT NULL,
    ended_at     TEXT
);
CREATE INDEX idx_workers_session ON workers(session_id);

CREATE TABLE events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    worker_id   TEXT NOT NULL REFERENCES workers(id),
    type        TEXT NOT NULL,
    data        TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
```

All timestamps are RFC3339Nano UTC strings.

Stability: **Needs review** — schema is young; columns will be added via
`ALTER TABLE` migrations. Readers should tolerate extra columns. The schema
is observable (tools can query the DB directly) and should be considered
part of the external surface from v1.0.

### Context Injection Format

When a worker receives a task, its stdin prompt is assembled from up to
three parts (in order, each separated by a blank line):

1. **Delegation policy block** (depth ≥ 1 only):
   ```
   === DELEGATION POLICY ===
   <depth-appropriate guidance>
   === END POLICY ===
   ```

2. **Shadow context block** (when context is available):
   ```
   === CONVERSATION CONTEXT (recent messages from root session) ===
   [User]: ...
   [Assistant]: ...
   === END CONTEXT ===
   ```

3. **Task**:
   ```
   TASK: <task body>
   ```

Stability: **Needs review** — block headers and format may change as
shadow mode and delegation policy mature.

### Shadow Context Window

50 lines (user + assistant message text), rolling. Each message is
formatted as `[User]: ...` or `[Assistant]: ...`. Transcript tailed with
a 500 ms poll interval; context is a best-effort snapshot.

Stability: **Needs review** — window size and poll interval are
compile-time constants, not configurable. May become configurable before
1.0.

### MCP Session Hooks

On MCP session connect: broker registers session in SQLite and, for
root sessions (depth 0), calls `RequestRoots` after a 500 ms delay to
discover the client's CWD. On disconnect: session is marked
`disconnected_at`; sessions from previous server runs are purged on
startup.

Stability: **Needs review** — the roots-based CWD discovery is a
heuristic; root selection (first root URI → strip `file://`) may need
refinement.

### Transcript Discovery

Given a CWD, the broker encodes it as:
```
"-" + strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(cwd[1:])
```
and scans `~/.claude/projects/<encoded>/` for `.jsonl` files, selecting
the most recently modified one.

Stability: **Needs review** — depends on Claude Code's undocumented
project directory naming convention, which could change.

### Worker Process Invocation

Workers are spawned as:
```
claude -p --verbose --output-format stream-json --dangerously-skip-permissions
       [--model <model>]
       [--mcp-config <json>]   # omitted at maxDepth
```
with `stdin` open (prompt written then closed), `stdout` captured as
NDJSON, working directory set to the requested `cwd`.

Stability: **Needs review** — depends on the `claude` CLI's stable flags.
`--dangerously-skip-permissions` is required but is an unstable upstream
flag name.

### Pool Key

Workers are pooled by `cwd + "\x00" + model + "\x00" + depth`. Pre-warming
spawns one replacement after each dispatch. Maximum idle per key: 1.

Stability: **Internal** — not externally observable.

### Display Names

Workers at depth 0 are named `w1`, `w2`, … (atomic counter). Children
inherit the parent's display name and append `.N` (per-parent counter).

Stability: **Needs review** — format is visible in logs, dashboard, and
`parent_id` column. Changing it would affect log parsing.

## Gaps and Prerequisites for 1.0

1. **Shadow context window is not configurable** — The 50-line rolling
   window is a compile-time constant. Projects with verbose conversations
   may want a larger window; resource-constrained setups may want smaller.
   Consider a `--context-lines` flag on `serve`.

2. **Task acknowledgment / retry** — Once the broker writes the prompt to
   a worker's stdin, failure is silent. No retry or dead-letter mechanism.
   Acceptable for v0.x; needs a decision before 1.0.

3. **Single transcript per cwd** — If two Claude Code sessions are active
   in the same project directory simultaneously, only the most recently
   modified transcript is tailed. A race window exists; the correct shadow
   is not guaranteed.

4. **Transcript discovery depends on undocumented Claude Code convention**
   — The project directory encoding (`/` → `-`) is reverse-engineered from
   Claude Code's behaviour. Changes upstream would silently break shadow
   mode.

5. **Port-based, not socket-based** — Port 4242 is shared across all users
   on the host. Multiple users need different ports; no per-user isolation.
   Pre-1.0, document the multi-user configuration or switch to a per-user
   socket/port convention.

6. **Test coverage** — Core dispatch paths are covered. Edge cases
   (concurrent shadow registration, large transcripts, malformed NDJSON,
   SSE client reconnect, DB migration idempotency) need additional tests.

7. **Schema stability signal** — SQLite schema is observable but has no
   version number. Add a `schema_version` pragma or a `meta` table before
   1.0 so external tools can gate on it.

8. **`--dangerously-skip-permissions` dependency** — Workers require this
   flag. If the upstream `claude` CLI renames or removes it, spawning
   breaks silently. Should be validated at startup.

9. **`wid` URL parameter** — Used to propagate the parent display name for
   child worker naming. The parameter is internal but visible in the
   synthesised `--mcp-config` JSON that workers receive. Its encoding is
   not validated.

## Out of Scope for 1.0

- **Multi-host support** — cworkers is a local broker. Network transport
  (TCP, SSH tunnelling) is a separate product if needed.
- **Task queuing/persistence** — Fire-and-forget is intentional. Durable
  queuing with replay changes the architecture fundamentally.
- **Authentication/authorization** — Port binding on localhost is
  sufficient for a per-user local broker. Network-facing deployments are
  out of scope.
- **Dashboard persistence beyond current run** — The dashboard shows
  SQLite data from the current and prior runs (DB is not wiped on start),
  but historical replay, search, and export are not planned for 1.0.
