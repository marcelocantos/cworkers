// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

#include "json.h"
#include <string.h>
#include <unistd.h>

// --- Scanner internals ---

static const char *ws(const char *p, const char *e) {
    while (p < e && (*p == ' ' || *p == '\t' || *p == '\n' || *p == '\r')) p++;
    return p;
}

// Skip a quoted string. p points at opening '"'. Returns past closing '"'.
static const char *skip_str(const char *p, const char *e) {
    if (p >= e || *p != '"') return NULL;
    p++;
    while (p < e) {
        if (*p == '\\') { p += 2; continue; }
        if (*p == '"') return p + 1;
        p++;
    }
    return NULL;
}

// Skip any JSON value. Returns past the value.
static const char *skip_val(const char *p, const char *e) {
    p = ws(p, e);
    if (p >= e) return NULL;
    if (*p == '"') return skip_str(p, e);
    if (*p == '{' || *p == '[') {
        char open = *p, close = (open == '{') ? '}' : ']';
        int depth = 1;
        p++;
        while (p < e && depth > 0) {
            if (*p == '"') { p = skip_str(p, e); if (!p) return NULL; continue; }
            if (*p == open) depth++;
            else if (*p == close) depth--;
            p++;
        }
        return p;
    }
    // number, bool, null.
    while (p < e && *p != ',' && *p != '}' && *p != ']'
           && *p != ' ' && *p != '\t' && *p != '\n' && *p != '\r')
        p++;
    return p;
}

// Check if quoted string at p matches unquoted key.
static int key_eq(const char *p, const char *e, const char *key, size_t klen) {
    if (p >= e || *p != '"') return 0;
    if ((size_t)(e - p) < klen + 2) return 0;
    return memcmp(p + 1, key, klen) == 0 && p[klen + 1] == '"';
}

// --- Public scanner ---

void json_scan_init(json_scan_t *s, const char **keys) {
    s->count = 0;
    for (int i = 0; i < JSON_MAX_FIELDS && keys[i]; i++) {
        s->keys[i] = keys[i];
        s->vals[i] = NULL;
        s->lens[i] = 0;
        s->count++;
    }
}

int json_scan(json_scan_t *s, const char *src, size_t len) {
    const char *e = src + len;
    const char *p = ws(src, e);
    if (p >= e || *p != '{') return -1;
    p++;

    int found = 0;

    while (p < e) {
        p = ws(p, e);
        if (p >= e) return -1;
        if (*p == '}') return 0;

        // Key.
        if (*p != '"') return -1;
        const char *kstart = p;
        const char *kend = skip_str(p, e);
        if (!kend) return -1;
        size_t kraw = (size_t)(kend - kstart);

        // Colon.
        p = ws(kend, e);
        if (p >= e || *p != ':') return -1;
        p = ws(p + 1, e);
        if (p >= e) return -1;

        // Value start.
        const char *vstart = p;
        const char *vend = skip_val(p, e);
        if (!vend) return -1;

        // Check against wanted keys.
        for (int i = 0; i < s->count; i++) {
            if (s->vals[i]) continue; // already found
            size_t klen = strlen(s->keys[i]);
            if (kraw == klen + 2 && key_eq(kstart, e, s->keys[i], klen)) {
                s->vals[i] = vstart;
                s->lens[i] = (size_t)(vend - vstart);
                found++;
                break;
            }
        }

        // Early exit if all found.
        if (found == s->count) return 0;

        p = ws(vend, e);
        if (p < e && *p == ',') p++;
    }
    return 0;
}

const char *json_str(const json_scan_t *s, int idx, size_t *vlen) {
    if (idx < 0 || idx >= s->count || !s->vals[idx]) return NULL;
    if (s->lens[idx] < 2 || s->vals[idx][0] != '"') return NULL;
    *vlen = s->lens[idx] - 2;
    return s->vals[idx] + 1;
}

int json_str_eq(const json_scan_t *s, int idx, const char *lit) {
    size_t vlen;
    const char *v = json_str(s, idx, &vlen);
    if (!v) return 0;
    size_t llen = strlen(lit);
    return vlen == llen && memcmp(v, lit, llen) == 0;
}

// --- Emitter ---

void jb_init(jbuf_t *b, char *storage, size_t cap) {
    b->data = storage;
    b->len = 0;
    b->cap = cap;
}

void jb_reset(jbuf_t *b) { b->len = 0; }

void jb_ch(jbuf_t *b, char c) {
    if (b->len < b->cap) b->data[b->len++] = c;
}

void jb_raw(jbuf_t *b, const char *s, size_t n) {
    size_t avail = b->cap - b->len;
    size_t w = n < avail ? n : avail;
    memcpy(b->data + b->len, s, w);
    b->len += w;
}

void jb_lit(jbuf_t *b, const char *s) { jb_raw(b, s, strlen(s)); }

void jb_strn(jbuf_t *b, const char *s, size_t n) {
    jb_ch(b, '"');
    for (size_t i = 0; i < n; i++) {
        unsigned char c = (unsigned char)s[i];
        switch (c) {
        case '"':  jb_raw(b, "\\\"", 2); break;
        case '\\': jb_raw(b, "\\\\", 2); break;
        case '\n': jb_raw(b, "\\n", 2);  break;
        case '\r': jb_raw(b, "\\r", 2);  break;
        case '\t': jb_raw(b, "\\t", 2);  break;
        default:
            if (c < 0x20) {
                static const char hx[] = "0123456789abcdef";
                char esc[6] = {'\\', 'u', '0', '0', hx[c >> 4], hx[c & 0xf]};
                jb_raw(b, esc, 6);
            } else {
                jb_ch(b, (char)c);
            }
        }
    }
    jb_ch(b, '"');
}

void jb_str(jbuf_t *b, const char *s) { jb_strn(b, s, strlen(s)); }

void jb_int(jbuf_t *b, int v) {
    char tmp[16];
    int neg = 0;
    unsigned int uv;
    if (v < 0) { neg = 1; uv = (unsigned int)(-(v + 1)) + 1; }
    else uv = (unsigned int)v;
    int i = (int)sizeof(tmp);
    if (uv == 0) tmp[--i] = '0';
    else while (uv > 0) { tmp[--i] = (char)('0' + uv % 10); uv /= 10; }
    if (neg) tmp[--i] = '-';
    jb_raw(b, tmp + i, sizeof(tmp) - (size_t)i);
}

void jb_bool(jbuf_t *b, int v) {
    if (v) jb_raw(b, "true", 4);
    else   jb_raw(b, "false", 5);
}

void jb_key(jbuf_t *b, const char *k) {
    jb_str(b, k);
    jb_ch(b, ':');
}

int jb_flush(jbuf_t *b, int fd) {
    size_t off = 0;
    while (off < b->len) {
        ssize_t n = write(fd, b->data + off, b->len - off);
        if (n <= 0) return -1;
        off += (size_t)n;
    }
    char nl = '\n';
    if (write(fd, &nl, 1) != 1) return -1;
    return 0;
}
