# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

cworkers is a task broker for Claude Code agent sessions. It pre-spawns idle worker agents that receive tasks instantly via a Unix domain socket, with multi-session shadow mode — each session registers its own JSONL transcript, and dispatches select which session's context to inject. Single Go binary, zero external dependencies.

Subcommands: `serve`, `worker`, `dispatch`, `shadow`, `unshadow`, `status`. See `DESIGN.md` for the full architecture, protocol spec, and known limitations.

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

- **shadowRegistry** — Maps session IDs to shadow instances. Each shadow tails a JSONL transcript file and maintains a rolling window of recent user/assistant messages (thread-safe). Sessions register/unregister via the SHADOW/UNSHADOW protocol commands.
- **shadow** — Tails a single transcript, maintains rolling message window. Has a `done` channel for clean shutdown on unregister.
- **pool** — Thread-safe worker pool with model-tagged connections and a dispatch queue (waiters). When no worker matches a dispatch, the request queues with a timeout; arriving workers check queued dispatches before entering the pool.
- **Protocol** — Line-based text over Unix socket: `WORKER <model>`, `DISPATCH <model> <session>\n<task>`, `SHADOW <session-id> <transcript-path> [context-lines]`, `UNSHADOW <session-id>`, `STATUS [session-id]`. Workers hold the connection open until a task arrives or timeout.
- **Worker reconnect loop** — `worker` command loops internally with 60s reconnect intervals within a single bash tool call (capped at 590s default). No agent-level looping required.

Socket path defaults to `/tmp/cworkers-<uid>.sock`.

## Delivery

Delivery: merged to master.
