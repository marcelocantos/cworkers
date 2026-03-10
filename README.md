# cworkers

A task broker for [Claude Code](https://claude.ai/code) agent sessions.
Pre-spawns idle worker agents that receive tasks instantly over a Unix socket,
with multi-session shadow mode for automatic conversation context injection.

## Why

Claude Code agents run as single-threaded conversations. Delegating work inline
blocks the conversation; spawning sub-agents carries ~15-18k tokens of startup
overhead. cworkers eliminates both problems: workers are pre-spawned and idle,
and shadow mode gives them awareness of the root conversation automatically.

## Install

```bash
go install github.com/marcelocantos/cworkers@latest
```

Or build from source:

```bash
make
make install  # copies to /usr/local/bin (may need sudo)
```

## Quick Start

```bash
# 1. Start the broker (global, one per user)
cworkers serve &

# 2. Register your session's transcript for shadow mode
cworkers shadow --session my-session --transcript ~/.claude/projects/.../session.jsonl

# 3. Spawn workers
cworkers worker --model opus --timeout 590s &
cworkers worker --model sonnet --timeout 590s &

# 4. Dispatch tasks (with session context)
cworkers dispatch --session my-session --model opus "Analyze the error handling in src/api/"
# => OK

# 5. Check status
cworkers status
# => WORKERS: 1 (sonnet: 1), shadows: 1

# 6. Clean up when session ends
cworkers unshadow --session my-session
```

## Commands

| Command | Description |
|---------|-------------|
| `serve` | Start the broker on a Unix socket |
| `worker` | Register as an idle worker, block until a task arrives |
| `dispatch` | Send a task to a matching worker (with optional session context) |
| `shadow` | Register a session's transcript for context injection |
| `unshadow` | Remove a session's shadow registration |
| `status` | Show pool size by model and shadow count |

Run `cworkers --help` for full flag reference, or `cworkers --help-agent` for
the agent integration guide.

## Shadow Mode

Each session registers its transcript via `cworkers shadow`. The broker tails
the JSONL file and maintains a rolling window of recent messages. When
dispatching with `--session`, the broker prepends that session's context to the
task, giving workers awareness of the conversation without the root agent
summarising anything.

Multiple sessions can share a single broker, each with its own shadow.

## Model Routing

Workers register with a model tag (`--model opus`, `--model sonnet`). Dispatches
route to matching workers by exact tag. Omit `--model` on either side for
wildcard matching.

## Design

See [DESIGN.md](DESIGN.md) for the full architecture, protocol specification,
and known limitations.

## License

[Apache 2.0](LICENSE)
