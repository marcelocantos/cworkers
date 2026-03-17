// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Append-only NDJSON event log. No SQLite, no indexes.
// Concurrent writers safe via O_APPEND.

#ifndef CWORKERS_LOG_H
#define CWORKERS_LOG_H

#include <stddef.h>

#define LOG_PATH_SUFFIX "/.local/share/cworkers/events.jsonl"

// Open (or create) the log file. Returns fd, or -1 on failure.
int log_open(void);

// Append a pre-formatted NDJSON line (must not contain newlines).
// Adds trailing newline. Thread-safe via O_APPEND.
void log_write(int fd, const char *line, size_t len);

#endif
