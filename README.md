# cworkers

A task broker for [Claude Code](https://claude.ai/code) agent sessions.
Runs as an MCP server (streamable HTTP, port 4242) that spawns `claude -p`
worker processes on demand and returns their output. Svelte dashboard for
monitoring sessions and workers. SQLite persistence for session/worker/event
tracking. Installed via Homebrew, runs as a brew service.

Single Go binary. Two subcommands: `serve` (MCP daemon) and `status` (active
worker query).

## Why

Claude Code agents run as single-threaded conversations. Delegating work inline
blocks the conversation; spawning sub-agents carries ~15-18k tokens of startup
overhead. cworkers gives you a simple MCP tool to delegate tasks to worker
agents without blocking the root session. Model routing lets you direct tasks
to sonnet (default), opus, or haiku based on complexity. Depth-controlled
hierarchies let workers spawn their own workers without runaway recursion.

Workers start fresh — provide all necessary context in the task description.

## Setup

Paste this into Claude Code:

> Follow https://github.com/marcelocantos/cworkers/blob/master/agents-guide.md

## How It Works

See [DESIGN.md](DESIGN.md) for the full architecture, protocol specification,
and known limitations.

## License

[Apache 2.0](LICENSE)
