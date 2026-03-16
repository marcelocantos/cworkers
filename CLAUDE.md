# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

cworkers is a task broker for Claude Code agent sessions. It runs as an MCP server that pre-spawns idle `claude -p` worker processes and dispatches tasks to them instantly. Shadow mode auto-discovers the calling session's JSONL transcript and injects recent conversation context into workers. Single Go binary.

Subcommands: `serve` (MCP daemon on port 4242), `status` (pool/shadow query). See `DESIGN.md` for the full architecture and known limitations.

## Build & Test

```bash
make          # build the cworkers binary
make test     # run all tests (go test ./...)
make install  # copy binary to /usr/local/bin
make clean    # remove binary
```

Run a single test:
```bash
go test -run TestE2EWorkerReceivesTask ./...
```

## Architecture

Everything lives in a single `main.go` with `main_test.go` for tests. Key components:

- **MCP server** — Streamable HTTP on `/mcp` (via mcp-go library). Single tool: `cwork(task, cwd, model?)`.
- **shadowRegistry** — Maps cwds to shadow instances. Each shadow tails a JSONL transcript file and maintains a rolling window of recent user/assistant messages (thread-safe). Auto-registers on first `cwork` call per cwd.
- **shadow** — Tails a single transcript, maintains rolling message window. Has a `done` channel for clean shutdown.
- **pool** — Thread-safe worker pool keyed by `cwd + model`. Pre-warming: each dispatch spawns a replacement worker in the background.
- **workerProc** — A pre-spawned `claude -p` process. Receives prompt on stdin, produces NDJSON on stdout. Broker parses stream-json output for result text.
- **Progress heartbeats** — Sends `notifications/progress` and `notifications/message` every 20s during dispatch to prevent MCP client timeout.

## Delivery

Delivery: merged to master.
