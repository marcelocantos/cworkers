# cworkers Agent Guide

This guide explains how an AI coding agent should use cworkers to delegate
tasks to pre-spawned worker agents.

## Typical Workflow

1. **Start the broker** at the beginning of your session:

   ```bash
   cworkers serve --transcript /path/to/session.jsonl &
   ```

   The `--transcript` flag enables shadow mode — workers automatically
   receive recent conversation context from your session.

2. **Spawn workers** as background bash calls:

   ```bash
   cworkers worker --model opus --timeout 590s &
   cworkers worker --model sonnet --timeout 590s &
   ```

   Workers block until they receive a task or their timeout expires.
   The 590s timeout stays within the 600s bash tool limit.

3. **Dispatch tasks** when you need to delegate:

   ```bash
   cworkers dispatch --model opus "Analyze the error handling in src/api/"
   ```

   The response is either `OK` (task delivered) or `NO_WORKERS` (exit code 2).

4. **Check pool status** at any time:

   ```bash
   cworkers status
   ```

   Output: `WORKERS: 3 (opus: 1, sonnet: 2), shadow: 4096 bytes`

## Model Routing

Use `--model` to route tasks by complexity:
- **opus** — deep reasoning, architectural analysis, novel problem-solving
- **sonnet** — well-scoped coding tasks, mechanical changes, test writing
- Omit `--model` for any-model dispatch

## Dispatch Queue

If no matching worker is available, dispatch waits up to 30 seconds (configurable
via `--wait` on the broker) for one to register. This handles the race condition
where a replacement worker is still spawning.

## Worker Lifecycle

Workers reconnect to the broker every 60 seconds internally, so a single
`cworkers worker` call covers the full timeout window. When a worker receives
a task, it prints the task to stdout and exits. Respawn workers after they
complete a task or their timeout expires.

## Socket Path

Default: `/tmp/cworkers-<uid>.sock`. Use `--sock` to disambiguate when
running multiple concurrent sessions.
