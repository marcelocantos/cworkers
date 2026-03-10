# cworkers

A task broker for [Claude Code](https://claude.ai/code) agent sessions.
Pre-spawns idle worker agents that receive tasks instantly over a Unix socket,
with multi-session shadow mode for automatic conversation context injection.

## Why

Claude Code agents run as single-threaded conversations. Delegating work inline
blocks the conversation; spawning sub-agents carries ~15-18k tokens of startup
overhead. cworkers eliminates both problems: workers are pre-spawned and idle,
and shadow mode gives them awareness of the root conversation automatically.

## Setup

Paste this into Claude Code:

> Follow https://github.com/marcelocantos/cworkers/blob/master/agents-guide.md

## How It Works

See [DESIGN.md](DESIGN.md) for the full architecture, protocol specification,
and known limitations.

## License

[Apache 2.0](LICENSE)
