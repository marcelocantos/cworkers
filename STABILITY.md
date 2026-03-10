# Stability

## Stability Commitment

Version 1.0 represents a backwards-compatibility contract. After 1.0,
breaking changes to the CLI interface, wire protocol, configuration, or
output formats require a major version bump (which in practice means
forking to a new product). The pre-1.0 period exists to get these right.

## Interaction Surface Catalogue

Snapshot as of v0.7.0.

### CLI Subcommands

| Subcommand | Stability | Notes |
|---|---|---|
| `serve` | Stable | Core broker lifecycle. |
| `worker` | Stable | Core worker registration. |
| `dispatch` | Stable | Core task routing. |
| `shadow` | Needs review | Multi-session shadow is new; command surface may evolve. |
| `unshadow` | Needs review | Paired with `shadow`. |
| `status` | Stable | Simple query, unlikely to break. |

### CLI Flags

| Flag | Scope | Type | Default | Stability |
|---|---|---|---|---|
| `--version` | global | bool | — | Stable |
| `--help` / `-h` | global | bool | — | Stable |
| `--help-agent` | global | bool | — | Stable |
| `--sock <path>` | global | string | `/tmp/cworkers-<uid>.sock` | Stable |
| `--wait <dur>` | serve | duration | 30s | Stable |
| `--timeout <dur>` | worker | duration | 590s | Stable |
| `--model <name>` | worker, dispatch | string | "" (wildcard) | Stable |
| `--session <id>` | worker, dispatch, shadow, unshadow, status | string | "" | Needs review |
| `--transcript <path>` | shadow | string | — (required) | Needs review |
| `--context <N>` | shadow | int | 50 | Needs review |

### Wire Protocol

Line-based text over Unix domain socket.

| Command | Format | Stability |
|---|---|---|
| `WORKER` | `WORKER <model> <session>\n` | Needs review — session scoping is new; positional encoding matches DISPATCH. |
| `DISPATCH` | `DISPATCH <model> <session>\n<task body>` | Needs review — positional empty-field encoding is fragile. |
| `SHADOW` | `SHADOW <session-id> <transcript-path> [context-lines]\n` | Needs review |
| `UNSHADOW` | `UNSHADOW <session-id>\n` | Needs review |
| `STATUS` | `STATUS [<session-id>]\n` | Needs review — session-scoped variant is new. |

### Wire Protocol Responses

| Response | Meaning | Stability |
|---|---|---|
| `OK\n` | Success (dispatch delivered, shadow registered, etc.) | Stable |
| `NO_WORKERS\n` | No matching worker within wait period | Stable |
| `ERROR: <msg>\n` | Protocol or validation error | Stable |
| `WORKERS: N (model: n, ...), shadows: M\n` | Global status output | Needs review — format may gain fields. |
| `SESSION: <id>, shadow: <bool>, available_workers: N (model: n, ...)\n` | Session-scoped status output | Needs review — new in v0.5.0. |

### Exit Codes

| Code | Meaning | Stability |
|---|---|---|
| 0 | Success | Stable |
| 1 | General error | Stable |
| 2 | No workers available (dispatch) | Stable |

### Context Injection Format

```
=== CONVERSATION CONTEXT (recent messages from root session) ===
[User]: ...
[Assistant]: ...
=== END CONTEXT ===

TASK: <task body>
```

Stability: **Needs review** — format markers and structure may change as
shadow mode matures.

### Socket Path Convention

Default: `/tmp/cworkers-<uid>.sock` where `<uid>` is the numeric user ID.

Stability: **Stable**.

## Gaps and Prerequisites for 1.0

1. **DISPATCH protocol encoding** — The positional empty-field encoding
   (`DISPATCH  session\n...` with double space for empty model) is fragile
   and error-prone. Consider switching to explicit delimiters or a
   `-` wildcard marker before 1.0.

2. **Shadow session lifecycle** — No automatic cleanup when sessions end.
   The root agent must explicitly `UNSHADOW`. Consider a TTL or
   heartbeat mechanism.

3. **Status output format** — Currently a single human-readable line.
   May need a structured (JSON) mode for programmatic consumers before
   locking in.

4. **Task acknowledgment** — No retry or dead-letter mechanism. Acceptable
   for v0.x but may need addressing before committing to 1.0 stability.

5. **Test coverage** — Core paths are covered but edge cases (concurrent
   shadow registration races, large transcript handling, malformed
   protocol input) need more tests.

6. **Transcript discovery** — Currently manual (`--transcript` path).
   Auto-discovery would improve UX but affects the CLI surface.

## Out of Scope for 1.0

- **Multi-host support** — cworkers is a local broker. Network transport
  (TCP, SSH tunnelling) is a separate product if needed.
- **Task queuing/persistence** — Fire-and-forget is intentional. Durable
  queuing changes the architecture fundamentally.
- **Authentication/authorization** — Socket permissions (0700) are
  sufficient for a per-user local broker.
- **Web UI/dashboard** — CLI-only. Status output covers monitoring needs.
