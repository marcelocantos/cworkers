// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Append-only NDJSON event logging.
// Two files: activity log (lifecycle) and per-worker detail log.

#ifndef CWORKERS_LOG_H
#define CWORKERS_LOG_H

#include <stddef.h>

#define LOG_DIR_SUFFIX "/.local/share/cworkers"

// Open the activity log (activity.jsonl). Returns fd, or -1.
int log_activity_open(void);

// Open a per-worker detail log (workers/<id>.jsonl). Returns fd, or -1.
int log_worker_open(const char *worker_id);

// Append a pre-formatted NDJSON line + newline. Atomic via O_APPEND + writev.
void log_write(int fd, const char *line, size_t len);

#endif
