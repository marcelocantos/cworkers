// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Spawn and drive a claude -p worker process.
// Streaming: reads NDJSON from worker stdout, calls back per-event.

#ifndef CWORKERS_WORKER_H
#define CWORKERS_WORKER_H

#include <stddef.h>
#include <sys/uio.h>

// Event types from the worker's NDJSON stream.
enum worker_event {
    WE_TEXT,      // assistant text chunk
    WE_TOOL_USE,  // tool_use block (name)
    WE_RESULT,    // final result text
    WE_ERROR,     // error result
    WE_LINE,      // raw NDJSON line (for event logging)
    WE_HEARTBEAT, // periodic liveness signal (no data)
};

// Callback: called for each event during worker execution.
// For WE_RESULT and WE_ERROR, data is the result/error text.
// For WE_TEXT, data is the assistant text chunk.
// For WE_TOOL_USE, data is the tool name.
// For WE_LINE, data is the full raw NDJSON line.
typedef void (*worker_event_fn)(enum worker_event ev,
                                const char *data, size_t len,
                                void *ctx);

// Spawn claude -p, write prompt to stdin, parse NDJSON from stdout.
// Calls event_fn for each event. Blocks until worker exits.
// Returns 0 on success (result delivered via WE_RESULT callback),
// -1 on spawn failure.
int worker_run(const char *claude_path,
               const char *cwd, const char *model,
               const struct iovec *prompt_iov, int prompt_iovcnt,
               const char **env_extra,  // NULL-terminated KEY=VALUE pairs
               worker_event_fn event_fn, void *ctx);

#endif
