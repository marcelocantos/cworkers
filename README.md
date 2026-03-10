# cworkers

A task broker for [Claude Code](https://claude.ai/code) agent sessions.
Pre-spawns idle worker agents that receive tasks instantly over a Unix socket,
with optional conversation context injection from the root session's transcript.

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
# 1. Start the broker (with optional transcript shadowing)
cworkers serve --transcript ~/.claude/projects/.../session.jsonl &

# 2. Spawn workers
cworkers worker --model opus --timeout 590s &
cworkers worker --model sonnet --timeout 590s &

# 3. Dispatch tasks
cworkers dispatch --model opus "Analyze the error handling in src/api/"
# => OK

# 4. Check status
cworkers status
# => WORKERS: 1 (sonnet: 1), shadow: 4096 bytes
```

## Commands

| Command | Description |
|---------|-------------|
| `serve` | Start the broker on a Unix socket |
| `worker` | Register as an idle worker, block until a task arrives |
| `dispatch` | Send a task to a matching worker |
| `status` | Show pool size by model |

Run `cworkers --help` for full flag reference, or `cworkers --help-agent` for
the agent integration guide.

## Shadow Mode

When started with `--transcript <path>`, the broker tails the root session's
JSONL transcript and maintains a rolling window of recent messages. Dispatched
tasks are automatically prefixed with this context, giving workers awareness of
the conversation without the root agent summarising anything.

## Model Routing

Workers register with a model tag (`--model opus`, `--model sonnet`). Dispatches
route to matching workers by exact tag. Omit `--model` on either side for
wildcard matching.

## Design

See [DESIGN.md](DESIGN.md) for the full architecture, protocol specification,
and known limitations.

## License

[Apache 2.0](LICENSE)
