# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

cworkers is a task broker for Claude Code agent sessions. Two binaries:

- **`cwork`** (C, 53KB) — Stdio MCP server. Spawns `claude -p` workers on demand, returns results. Zero malloc, concurrent dispatch via pthreads. Logs events to NDJSON files.
- **`cdash`** (Go) — TUI dashboard. Watches activity log, renders worker transcripts with glamour markdown.

No daemon. `cwork` is spawned by Claude Code as a stdio MCP server. Workers run in parallel. The dashboard is optional.

## Build & Test

```bash
# cwork (C binary)
cc -std=c11 -Wall -Wextra -Werror -Os -Isrc -o cwork \
  src/cwork.c src/work.c src/json.c src/log.c src/worker.c \
  src/help_agent.s -lpthread -dead_strip
sh test/test_cwork.sh

# cdash (Go binary)
cd cdash && go build -o ../cdash-bin .
```

Install cwork:
```bash
cp cwork ~/.local/bin/cwork
```

## Architecture

### cwork (src/)
- `cwork.c` — entry point, CLI flags
- `work.c` — MCP JSON-RPC protocol, dispatch logic, NDJSON logging
- `worker.c` — `posix_spawnp` worker management, NDJSON output parsing, heartbeat thread
- `json.c/h` — zero-alloc JSON scanner (single-pass key extraction) and emitter
- `log.c/h` — append-only NDJSON event logging (`O_APPEND` for concurrent writers)
- `help_agent.s` — linker-level embedding of `help-agent.md` via `.incbin`

### cdash (cdash/)
- `main.go` — bubbletea TUI, glamour markdown rendering, fsnotify file watcher

### Data files (~/.local/share/cworkers/)
- `activity.jsonl` — worker lifecycle events (start/done/error/heartbeat)
- `workers/<id>.jsonl` — per-worker detail logs (task, progress, result)

## Delivery

Delivery: merged to master.
