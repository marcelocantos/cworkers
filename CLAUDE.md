# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

cworkers is a task broker for Claude Code agent sessions. It runs as an MCP server that spawns `claude -p` worker processes on demand and returns their output. Stateless MCP-to-CLI bridge with SQLite observability and a Svelte dashboard. Single Go binary.

Subcommands: `serve` (MCP daemon on port 4242), `status` (active worker query). See `DESIGN.md` for the full architecture and known limitations.

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
- **workerProc** — A `claude -p` process spawned on demand per `cwork` call. Receives prompt on stdin, produces NDJSON on stdout. Broker parses stream-json output for result text.
- **SQLite DB** — Logs sessions, workers, and worker output events to `~/.local/share/cworkers/cworkers.db`.
- **Dashboard** — Svelte 5 single-file HTML served at `/dashboard`; reads live data via HTTP API and SSE.
- **Progress heartbeats** — Sends `notifications/progress` and `notifications/message` every 20s during dispatch to prevent MCP client timeout.

## Delivery

Delivery: merged to master.
