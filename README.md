# cworkers

A task broker for [Claude Code](https://claude.ai/code) agent sessions.
Spawns `claude -p` worker processes on demand and returns their output.

Two binaries: `cwork` (53KB C binary, stdio MCP server) and `cdash`
(Go TUI dashboard). No daemon — `cwork` runs as a Claude Code MCP server
subprocess and spawns workers directly.

## Why

Claude Code agents run as single-threaded conversations. Delegating work inline
blocks the conversation; spawning sub-agents carries startup overhead. cworkers
gives you a simple MCP tool to delegate tasks to worker agents without blocking
the root session. Workers run in parallel with concurrent dispatch.

Workers start fresh — provide all necessary context in the task description.

## Setup

Paste this into Claude Code:

> Follow https://github.com/marcelocantos/cworkers/blob/master/agents-guide.md

## How It Works

See [DESIGN.md](DESIGN.md) for the full architecture and known limitations.

## Dashboard

Run `cdash` for a live TUI showing worker status and markdown-rendered
transcripts:

```bash
cd cdash && go build -o cdash-bin . && ./cdash-bin
```

## License

[Apache 2.0](LICENSE)
