// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// work.c — Stdio MCP frontend for cworkers.
// Spawns claude -p workers directly. Logs events to NDJSON file.
// 35KB binary, zero SQLite dependency.

#include <stdlib.h>
#include <string.h>
#include <sys/uio.h>
#include <time.h>
#include <unistd.h>

#include "json.h"
#include "log.h"
#include "worker.h"

#ifndef CWORKERS_VERSION
#define CWORKERS_VERSION "dev"
#endif

// Output buffer for JSON-RPC responses.
#define OUT_CAP (64 * 1024)
static char out_storage[OUT_CAP];
static jbuf_t out;

// Line read buffer for stdin.
#define LINE_CAP (256 * 1024)
static char line_buf[LINE_CAP];

// Scratch buffer for building log entries.
#define LOG_CAP (64 * 1024)
static char log_storage[LOG_CAP];
static jbuf_t logbuf;

// Worker ID counter.
static int worker_seq = 0;

// --- Read line from stdin. Returns length, -1 on EOF. ---

static ssize_t read_line(void) {
    size_t pos = 0;
    for (;;) {
        if (pos >= LINE_CAP - 1) return -1;
        ssize_t n = read(STDIN_FILENO, line_buf + pos, 1);
        if (n <= 0) return pos > 0 ? (ssize_t)pos : -1;
        if (line_buf[pos] == '\n') {
            line_buf[pos] = '\0';
            return (ssize_t)pos;
        }
        pos++;
    }
}

// --- Emit helpers (JSON-RPC to stdout) ---

static void emit_flush(void) {
    jb_flush(&out, STDOUT_FILENO);
    jb_reset(&out);
}

static void emit_response_head(const char *raw_id, size_t id_len) {
    jb_reset(&out);
    jb_lit(&out, "{\"jsonrpc\":\"2.0\",\"id\":");
    jb_raw(&out, raw_id, id_len);
    jb_ch(&out, ',');
}

static void emit_tool_result(const char *raw_id, size_t id_len,
                             const char *text, size_t text_len,
                             int is_error) {
    emit_response_head(raw_id, id_len);
    jb_lit(&out, "\"result\":{");
    if (is_error) jb_lit(&out, "\"isError\":true,");
    jb_lit(&out, "\"content\":[{\"type\":\"text\",\"text\":");
    jb_strn(&out, text, text_len);
    jb_lit(&out, "}]}}");
    emit_flush();
}

static void emit_tool_error(const char *raw_id, size_t id_len,
                            const char *msg) {
    emit_tool_result(raw_id, id_len, msg, strlen(msg), 1);
}

static void emit_progress(const char *msg, size_t msg_len) {
    jb_reset(&out);
    jb_lit(&out, "{\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\","
                  "\"params\":{\"level\":\"info\",\"data\":");
    jb_strn(&out, msg, msg_len);
    jb_lit(&out, "}}");
    emit_flush();
}

// --- Timestamp ---

static void emit_timestamp(jbuf_t *b) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    struct tm tm;
    gmtime_r(&ts.tv_sec, &tm);
    char tbuf[32];
    strftime(tbuf, sizeof(tbuf), "%Y-%m-%dT%H:%M:%S", &tm);
    jb_lit(b, tbuf);
    // Append .NNN (milliseconds) and Z.
    char ms[8];
    int millis = (int)(ts.tv_nsec / 1000000);
    ms[0] = '.';
    ms[1] = (char)('0' + millis / 100);
    ms[2] = (char)('0' + (millis / 10) % 10);
    ms[3] = (char)('0' + millis % 10);
    ms[4] = 'Z';
    jb_raw(b, ms, 5);
}

// --- Log helpers ---

static void log_event(int log_fd, const char *worker_id,
                      const char *event_type, const char *data, size_t data_len) {
    jb_reset(&logbuf);
    jb_ch(&logbuf, '{');
    jb_key(&logbuf, "ts"); jb_ch(&logbuf, '"'); emit_timestamp(&logbuf); jb_ch(&logbuf, '"');
    jb_ch(&logbuf, ',');
    jb_key(&logbuf, "worker"); jb_str(&logbuf, worker_id);
    jb_ch(&logbuf, ',');
    jb_key(&logbuf, "event"); jb_str(&logbuf, event_type);
    if (data && data_len > 0) {
        jb_ch(&logbuf, ',');
        jb_key(&logbuf, "data"); jb_strn(&logbuf, data, data_len);
    }
    jb_ch(&logbuf, '}');
    log_write(log_fd, logbuf.data, logbuf.len);
}

// --- Env var passthrough ---

extern char **environ;

static int should_passthrough(const char *name, size_t nlen) {
    if (nlen >= 10 && memcmp(name, "ANTHROPIC_", 10) == 0) return 1;
    if (nlen >= 7  && memcmp(name, "CLAUDE_", 7) == 0) return 1;
    if (nlen >= 4  && memcmp(name, "AWS_", 4) == 0) return 1;
    if (nlen == 19 && memcmp(name, "NODE_EXTRA_CA_CERTS", 19) == 0) return 1;
    if (nlen >= 6) {
        if (memcmp(name + nlen - 6, "_PROXY", 6) == 0) return 1;
        if (memcmp(name + nlen - 6, "_proxy", 6) == 0) return 1;
    }
    return 0;
}

#define MAX_ENV_EXTRA 128
static const char *env_extra_buf[MAX_ENV_EXTRA + 1];

static const char **collect_env(void) {
    int idx = 0;
    for (char **e = environ; *e && idx < MAX_ENV_EXTRA; e++) {
        char *eq = strchr(*e, '=');
        if (!eq) continue;
        if (should_passthrough(*e, (size_t)(eq - *e)))
            env_extra_buf[idx++] = *e;
    }
    env_extra_buf[idx] = NULL;
    return env_extra_buf;
}

// --- Worker event handler ---

typedef struct {
    const char *raw_id;
    size_t id_len;
    const char *worker_id;
    int log_fd;
    int got_result;
} dispatch_ctx_t;

static void on_worker_event(enum worker_event ev,
                            const char *data, size_t len,
                            void *vctx) {
    dispatch_ctx_t *ctx = vctx;
    switch (ev) {
    case WE_TEXT:
        if (len > 0 && (data[0] == '#' ||
                       (len > 1 && data[0] == '*' && data[1] == '*'))) {
            emit_progress(data, len);
            log_event(ctx->log_fd, ctx->worker_id, "progress", data, len);
        }
        break;
    case WE_TOOL_USE: {
        char msg[128] = "using ";
        size_t mlen = 6;
        size_t copy = len < sizeof(msg) - mlen - 1 ? len : sizeof(msg) - mlen - 1;
        memcpy(msg + mlen, data, copy);
        mlen += copy;
        emit_progress(msg, mlen);
        log_event(ctx->log_fd, ctx->worker_id, "tool_use", data, len);
        break;
    }
    case WE_RESULT:
        emit_tool_result(ctx->raw_id, ctx->id_len, data, len, 0);
        log_event(ctx->log_fd, ctx->worker_id, "done", data, len > 200 ? 200 : len);
        ctx->got_result = 1;
        break;
    case WE_ERROR:
        emit_tool_result(ctx->raw_id, ctx->id_len, data, len, 1);
        log_event(ctx->log_fd, ctx->worker_id, "error", data, len > 200 ? 200 : len);
        ctx->got_result = 1;
        break;
    case WE_LINE:
        // Raw NDJSON — don't log (too verbose).
        break;
    }
}

// --- Handle tools/call ---

static void handle_cwork(const char *raw_id, size_t id_len,
                         const char *params, size_t params_len,
                         int log_fd) {
    const char *pkeys[] = {"name", "arguments", NULL};
    json_scan_t ps;
    json_scan_init(&ps, pkeys);
    json_scan(&ps, params, params_len);

    if (!json_str_eq(&ps, 0, "cwork")) {
        emit_response_head(raw_id, id_len);
        jb_lit(&out, "\"error\":{\"code\":-32601,\"message\":\"unknown tool\"}}");
        emit_flush();
        return;
    }
    if (!ps.vals[1]) {
        emit_tool_error(raw_id, id_len, "missing arguments");
        return;
    }

    const char *akeys[] = {"task", "cwd", "model", NULL};
    json_scan_t args;
    json_scan_init(&args, akeys);
    json_scan(&args, ps.vals[1], ps.lens[1]);

    size_t task_len, cwd_len, model_len;
    const char *task = json_str(&args, 0, &task_len);
    const char *cwd = json_str(&args, 1, &cwd_len);
    const char *model = json_str(&args, 2, &model_len);

    if (!task || task_len == 0) {
        emit_tool_error(raw_id, id_len, "missing required parameter: task");
        return;
    }
    if (!cwd || cwd_len == 0) {
        emit_tool_error(raw_id, id_len, "missing required parameter: cwd");
        return;
    }
    if (!model || model_len == 0) {
        model = "sonnet";
        model_len = 6;
    }

    // Copy strings out of line_buf before worker reuses it.
    char cwd_z[1024], model_z[64], task_z[LINE_CAP];
    zcopyn(cwd_z, sizeof(cwd_z), cwd, cwd_len);
    zcopyn(model_z, sizeof(model_z), model, model_len);
    zcopyn(task_z, sizeof(task_z), task, task_len);

    // Generate worker display name.
    worker_seq++;
    char display_name[16];
    {
        char tmp[16];
        int i = (int)sizeof(tmp);
        int v = worker_seq;
        if (v == 0) tmp[--i] = '0';
        else while (v > 0) { tmp[--i] = (char)('0' + v % 10); v /= 10; }
        tmp[--i] = 'w';
        int dlen = (int)sizeof(tmp) - i;
        memcpy(display_name, tmp + i, (size_t)dlen);
        display_name[dlen] = '\0';
    }

    // Copy raw_id out of line_buf.
    char id_copy[64];
    size_t id_copy_len = id_len;
    zcopyn(id_copy, sizeof(id_copy), raw_id, id_len);
    if (id_copy_len >= sizeof(id_copy)) id_copy_len = sizeof(id_copy) - 1;

    // Log start.
    log_event(log_fd, display_name, "start", task_z,
              task_len > 200 ? 200 : task_len);

    // Build prompt as iovecs: "TASK: " + task.
    struct iovec prompt_iov[2] = {
        { .iov_base = (void *)"TASK: ", .iov_len = 6 },
        { .iov_base = task_z, .iov_len = task_len },
    };

    const char **env = collect_env();

    dispatch_ctx_t dctx = {
        .raw_id = id_copy,
        .id_len = id_copy_len,
        .worker_id = display_name,
        .log_fd = log_fd,
        .got_result = 0,
    };

    int rc = worker_run(NULL, cwd_z, model_z, prompt_iov, 2,
                        env, on_worker_event, &dctx);

    if (rc < 0) {
        emit_tool_error(id_copy, id_copy_len, "failed to spawn worker");
        log_event(log_fd, display_name, "error", "spawn failed", 12);
        return;
    }
    if (!dctx.got_result) {
        emit_tool_error(id_copy, id_copy_len, "worker exited without result");
        log_event(log_fd, display_name, "error", "no result", 9);
    }
}

// --- MCP protocol responses ---

static void emit_initialize(const char *raw_id, size_t id_len) {
    emit_response_head(raw_id, id_len);
    jb_lit(&out,
        "\"result\":{"
            "\"protocolVersion\":\"2025-03-26\","
            "\"serverInfo\":{\"name\":\"cworkers\",\"version\":\"" CWORKERS_VERSION "\"},"
            "\"capabilities\":{\"tools\":{}},"
            "\"instructions\":\"Workers start fresh with no conversation context. "
            "Include all necessary context in the task description.\"}}");
    emit_flush();
}

static void emit_tools_list(const char *raw_id, size_t id_len) {
    emit_response_head(raw_id, id_len);
    jb_lit(&out,
        "\"result\":{\"tools\":[{"
            "\"name\":\"cwork\","
            "\"description\":\"Dispatch a task to a worker agent. Returns the worker's result.\","
            "\"inputSchema\":{"
                "\"type\":\"object\","
                "\"properties\":{"
                    "\"task\":{\"type\":\"string\",\"description\":\"The task prompt for the worker\"},"
                    "\"cwd\":{\"type\":\"string\",\"description\":\"Working directory of the calling session\"},"
                    "\"model\":{\"type\":\"string\",\"description\":\"Model to use (default: sonnet). Options: sonnet, opus, haiku\"}"
                "},"
                "\"required\":[\"task\",\"cwd\"]"
            "}"
        "}]}}");
    emit_flush();
}

// --- Main loop ---

int work_main(void) {
    jb_init(&out, out_storage, OUT_CAP);
    jb_init(&logbuf, log_storage, LOG_CAP);

    int log_fd = log_open();

    ssize_t len;
    while ((len = read_line()) >= 0) {
        if (len == 0) continue;

        const char *keys[] = {"method", "id", "params", NULL};
        json_scan_t msg;
        json_scan_init(&msg, keys);
        if (json_scan(&msg, line_buf, (size_t)len) < 0) continue;

        size_t method_len;
        const char *method = json_str(&msg, 0, &method_len);
        if (!method) continue;

        const char *raw_id = msg.vals[1];
        size_t id_len = msg.lens[1];

        if (method_len == 10 && memcmp(method, "initialize", 10) == 0) {
            emit_initialize(raw_id, id_len);
        } else if (method_len == 10 && memcmp(method, "tools/list", 10) == 0) {
            emit_tools_list(raw_id, id_len);
        } else if (method_len == 10 && memcmp(method, "tools/call", 10) == 0) {
            if (msg.vals[2])
                handle_cwork(raw_id, id_len, msg.vals[2], msg.lens[2], log_fd);
        } else if (raw_id) {
            // Unknown method with id — return JSON-RPC error.
            emit_response_head(raw_id, id_len);
            jb_lit(&out, "\"error\":{\"code\":-32601,\"message\":\"method not found\"}}");
            emit_flush();
        }
    }

    if (log_fd >= 0) close(log_fd);
    return 0;
}
