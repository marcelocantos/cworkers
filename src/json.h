// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Minimal zero-alloc JSON scanner and emitter.
// Scanner: single-pass key extraction, pointers into source buffer.
// Emitter: direct write to fd, no intermediate objects.

#ifndef CWORKERS_JSON_H
#define CWORKERS_JSON_H

#include <stddef.h>
#include <string.h>

// --- Scanner ---

// Maximum fields extractable in one scan.
#define JSON_MAX_FIELDS 8

typedef struct {
    const char *keys[JSON_MAX_FIELDS];  // wanted keys (caller sets, NULL-terminated)
    const char *vals[JSON_MAX_FIELDS];  // pointers into source (set by scan)
    size_t      lens[JSON_MAX_FIELDS];  // value lengths (set by scan)
    int         count;                  // number of wanted keys
} json_scan_t;

// Initialise a scan with wanted keys. keys is a NULL-terminated array.
void json_scan_init(json_scan_t *s, const char **keys);

// Scan a JSON object in src[0..len). For each key matching a wanted
// key, populate the corresponding vals/lens entry. Values point into
// src. String values include the quotes. Returns 0 on success, -1 on
// malformed input.
int json_scan(json_scan_t *s, const char *src, size_t len);

// Convenience: get a string value (strips quotes, no unescape).
// Returns pointer into src (past the opening quote), sets *vlen
// to the length excluding quotes. Returns NULL if field not found
// or not a string.
const char *json_str(const json_scan_t *s, int idx, size_t *vlen);

// Convenience: check if scanned value equals a literal string.
int json_str_eq(const json_scan_t *s, int idx, const char *lit);

// --- Emitter ---

// Fixed-size output buffer. Caller provides storage.
typedef struct {
    char  *data;
    size_t len;
    size_t cap;
    int    fd;   // bound output fd, or -1
} jbuf_t;

void jb_init(jbuf_t *b, char *storage, size_t cap);
void jb_reset(jbuf_t *b);
void jb_ch(jbuf_t *b, char c);
void jb_raw(jbuf_t *b, const char *s, size_t n);
void jb_lit(jbuf_t *b, const char *s);      // raw literal (no quotes)
void jb_str(jbuf_t *b, const char *s);      // quoted + escaped
void jb_strn(jbuf_t *b, const char *s, size_t n);
void jb_int(jbuf_t *b, int v);
void jb_bool(jbuf_t *b, int v);
void jb_key(jbuf_t *b, const char *k);      // "k":

// Bind buffer to an output fd. Enables auto-flush when buffer fills.
void jb_bind(jbuf_t *b, int fd);

// Flush buffer contents to bound fd. Resets buffer position.
// Returns 0 on success, -1 on error.
int jb_flush(jbuf_t *b);

// Flush buffer + newline to bound fd. For JSON-RPC line termination.
int jb_flush_line(jbuf_t *b);

// --- Utility ---

// Copy up to cap-1 bytes from src (length n) into dst, null-terminate.
// Returns the number of bytes copied (excluding null).
static inline size_t zcopyn(char *dst, size_t cap, const char *src, size_t n) {
    size_t c = n < cap - 1 ? n : cap - 1;
    memcpy(dst, src, c);
    dst[c] = '\0';
    return c;
}

#endif
