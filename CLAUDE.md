# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

cworkers is a task broker for Claude Code agent sessions. It pre-spawns idle worker agents that receive tasks instantly via a Unix domain socket, optionally injecting recent conversation context from the root session's JSONL transcript ("shadow mode"). Single Go binary, zero external dependencies.

Subcommands: `serve`, `worker`, `dispatch`, `status`. See `DESIGN.md` for the full architecture, protocol spec, and known limitations.

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

Everything lives in a single `main.go` (~660 lines) with `main_test.go` for tests. Key components:

- **shadow** — Tails a JSONL transcript file, maintains a rolling window of recent user/assistant messages (thread-safe). Used to inject conversation context into dispatched tasks.
- **pool** — Thread-safe worker pool with model-tagged connections and a dispatch queue (waiters). When no worker matches a dispatch, the request queues with a timeout; arriving workers check queued dispatches before entering the pool.
- **Protocol** — Line-based text over Unix socket: `WORKER <model>`, `DISPATCH <model>\n<task>`, `STATUS`. Workers hold the connection open until a task arrives or timeout.
- **Worker reconnect loop** — `worker` command loops internally with 60s reconnect intervals within a single bash tool call (capped at 590s default). No agent-level looping required.

Socket path defaults to `/tmp/cworkers-<uid>.sock`.

## Delivery

Delivery: merged to master.
