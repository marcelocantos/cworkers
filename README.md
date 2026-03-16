# cworkers

A task broker for [Claude Code](https://claude.ai/code) agent sessions.
Runs as an MCP server (streamable HTTP, port 4242) that pre-spawns idle
`claude -p` worker processes and dispatches tasks to them instantly. Shadow
mode auto-discovers the calling session's transcript and injects recent
conversation context into workers automatically.

Single Go binary. Two subcommands: `serve` (MCP daemon) and `status` (pool
query). Svelte dashboard for monitoring sessions and workers. SQLite persistence
for session/worker/event tracking. Installed via Homebrew, runs as a brew
service.

## Why

Claude Code agents run as single-threaded conversations. Delegating work inline
blocks the conversation; spawning sub-agents carries ~15-18k tokens of startup
overhead. cworkers eliminates both problems: workers are pre-spawned and idle,
and shadow mode gives them awareness of the root conversation automatically.
Model routing lets you direct tasks to sonnet (default) or opus based on
complexity. Depth-controlled hierarchies let workers spawn their own workers
without runaway recursion.

## Setup

Paste this into Claude Code:

> Follow https://github.com/marcelocantos/cworkers/blob/master/agents-guide.md

## How It Works

See [DESIGN.md](DESIGN.md) for the full architecture, protocol specification,
and known limitations.

## License

[Apache 2.0](LICENSE)
