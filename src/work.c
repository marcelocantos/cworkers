// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// work.c — Stdio MCP frontend for cworkers.
// Spawns claude -p workers directly. Logs lifecycle to activity.jsonl,
// detail to per-worker files. 35KB binary, zero SQLite.

#include <fcntl.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <sys/uio.h>
#include <time.h>
#include <unistd.h>

#include "json.h"

// Embedded help-agent.md (linked via .incbin in help_agent.s).
extern const char help_agent_data[];
extern const unsigned long long help_agent_size;
#include "log.h"
#include "worker.h"

#ifndef CWORKERS_VERSION
#define CWORKERS_VERSION "dev"
#endif

// Line read buffer for stdin (main thread only).
#define LINE_CAP (256 * 1024)
static char line_buf[LINE_CAP];

// Mutex protecting stdout writes (shared by all dispatch threads).
static pthread_mutex_t stdout_mu = PTHREAD_MUTEX_INITIALIZER;

// Active dispatch thread tracking.
static pthread_mutex_t threads_mu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t threads_cond = PTHREAD_COND_INITIALIZER;
static int active_threads = 0;

// Per-thread buffer sizes.
#define OUT_CAP (64 * 1024)
#define LOG_CAP (64 * 1024)

// Main thread output buffer (for initialize, tools/list, errors).
static char main_out_storage[OUT_CAP];
static jbuf_t main_out;


// --- Read line from stdin ---

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

// --- Emit helpers (JSON-RPC to stdout, mutex-protected) ---

// Flush buffer to stdout under mutex, then reset.
static void emit_flush(jbuf_t *b) {
    pthread_mutex_lock(&stdout_mu);
    jb_flush_line(b);
    pthread_mutex_unlock(&stdout_mu);
}

static void emit_response_head(jbuf_t *b, const char *raw_id, size_t id_len) {
    jb_reset(b);
    jb_lit(b, "{\"jsonrpc\":\"2.0\",\"id\":");
    jb_raw(b, raw_id, id_len);
    jb_ch(b, ',');
}

// raw=1: text is pre-escaped JSON string content. raw=0: plain C string.
static void emit_tool_result(jbuf_t *b, const char *raw_id, size_t id_len,
                             const char *text, size_t text_len,
                             int is_error, int raw) {
    emit_response_head(b, raw_id, id_len);
    jb_lit(b, "\"result\":{");
    if (is_error) jb_lit(b, "\"isError\":true,");
    jb_lit(b, "\"content\":[{\"type\":\"text\",\"text\":");
    if (raw) jb_raw_str(b, text, text_len);
    else     jb_strn(b, text, text_len);
    jb_lit(b, "}]}}");
    emit_flush(b);
}

static void emit_tool_error(jbuf_t *b, const char *raw_id, size_t id_len,
                            const char *msg) {
    emit_tool_result(b, raw_id, id_len, msg, strlen(msg), 1, 0);
}

static void emit_progress(jbuf_t *b, const char *msg, size_t msg_len) {
    jb_reset(b);
    jb_lit(b, "{\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\","
               "\"params\":{\"level\":\"info\",\"data\":");
    jb_strn(b, msg, msg_len);
    jb_lit(b, "}}");
    emit_flush(b);
}

// --- Timestamp ---

static void emit_timestamp(jbuf_t *b) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    struct tm tm;
    gmtime_r(&ts.tv_sec, &tm);
    char tbuf[32];
    strftime(tbuf, sizeof(tbuf), "%Y-%m-%dT%H:%M:%S", &tm);
    jb_ch(b, '"');
    jb_lit(b, tbuf);
    char ms[8];
    int millis = (int)(ts.tv_nsec / 1000000);
    ms[0] = '.';
    ms[1] = (char)('0' + millis / 100);
    ms[2] = (char)('0' + (millis / 10) % 10);
    ms[3] = (char)('0' + millis % 10);
    ms[4] = 'Z';
    jb_raw(b, ms, 5);
    jb_ch(b, '"');
}

// --- Log helpers ---

// Write to activity.jsonl (lifecycle events).
static void log_activity(jbuf_t *lb, int afd, const char *worker_id,
                         const char *event,
                         const char *extra_key, const char *extra_val) {
    jb_reset(lb);
    jb_ch(lb, '{');
    jb_key(lb, "ts"); emit_timestamp(lb);
    jb_ch(lb, ',');
    jb_key(lb, "id"); jb_str(lb, worker_id);
    jb_ch(lb, ',');
    jb_key(lb, "event"); jb_str(lb, event);
    if (extra_key && extra_val) {
        jb_ch(lb, ',');
        jb_key(lb, extra_key); jb_str(lb, extra_val);
    }
    jb_ch(lb, '}');
    log_write(afd, lb->data, lb->len);
}

// Write to per-worker file (detail events).
// raw=1: data is already JSON-escaped (from json_str). raw=0: plain string.
static void log_detail(jbuf_t *lb, int wfd, const char *event,
                       const char *data, size_t data_len, int raw) {
    jb_reset(lb);
    jb_ch(lb, '{');
    jb_key(lb, "ts"); emit_timestamp(lb);
    jb_ch(lb, ',');
    jb_key(lb, "event"); jb_str(lb, event);
    if (data && data_len > 0) {
        jb_ch(lb, ',');
        jb_key(lb, "data");
        if (raw) jb_raw_str(lb, data, data_len);
        else     jb_strn(lb, data, data_len);
    }
    jb_ch(lb, '}');
    log_write(wfd, lb->data, lb->len);
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
    int activity_fd;
    int worker_fd;
    int got_result;
    jbuf_t *out;    // per-thread output buffer
    jbuf_t *logbuf; // per-thread log buffer
} dispatch_ctx_t;

static void on_worker_event(enum worker_event ev,
                            const char *data, size_t len,
                            void *vctx) {
    dispatch_ctx_t *ctx = vctx;
    switch (ev) {
    case WE_TEXT:
        if (len > 0 && (data[0] == '#' ||
                       (len > 1 && data[0] == '*' && data[1] == '*'))) {
            emit_progress(ctx->out, data, len);
            log_detail(ctx->logbuf, ctx->worker_fd, "progress", data, len, 1);
        }
        break;
    case WE_TOOL_USE: {
        char msg[128] = "using ";
        size_t mlen = 6;
        size_t copy = len < sizeof(msg) - mlen - 1 ? len : sizeof(msg) - mlen - 1;
        memcpy(msg + mlen, data, copy);
        mlen += copy;
        emit_progress(ctx->out, msg, mlen);
        log_detail(ctx->logbuf, ctx->worker_fd, "tool_use", data, len, 1);
        break;
    }
    case WE_RESULT:
        emit_tool_result(ctx->out, ctx->raw_id, ctx->id_len, data, len, 0, 1);
        log_detail(ctx->logbuf, ctx->worker_fd, "result", data, len, 1);
        log_activity(ctx->logbuf, ctx->activity_fd, ctx->worker_id, "done", NULL, NULL);
        ctx->got_result = 1;
        break;
    case WE_ERROR:
        emit_tool_result(ctx->out, ctx->raw_id, ctx->id_len, data, len, 1, 1);
        log_detail(ctx->logbuf, ctx->worker_fd, "error", data, len, 1);
        log_activity(ctx->logbuf, ctx->activity_fd, ctx->worker_id, "error", NULL, NULL);
        ctx->got_result = 1;
        break;
    case WE_LINE:
        break;
    case WE_HEARTBEAT:
        log_activity(ctx->logbuf, ctx->activity_fd, ctx->worker_id, "heartbeat", NULL, NULL);
        break;
    }
}

// --- Handle tools/call (runs in its own thread) ---

// Thread argument: all data copied from line_buf before thread starts.
typedef struct {
    char id_copy[64];
    size_t id_len;
    char cwd[1024];
    char model[64];
    size_t task_len;
    int activity_fd;
    char task[];  // flexible array member — must be last
} cwork_args_t;

static void *cwork_thread(void *arg) {
    cwork_args_t *a = arg;

    // Per-thread buffers (on stack — 128KB total, well within default 8MB stack).
    char out_storage[OUT_CAP];
    char log_storage[LOG_CAP];
    jbuf_t out, lb;
    jb_init(&out, out_storage, OUT_CAP);
    jb_bind(&out, STDOUT_FILENO);
    jb_init(&lb, log_storage, LOG_CAP);

    // Generate worker ID.
    char display_name[10];
    {
        static const char b36[] = "0123456789abcdefghijklmnopqrstuvwxyz";
        unsigned char rnd[6];
        int ufd = open("/dev/urandom", O_RDONLY);
        if (ufd >= 0) { read(ufd, rnd, sizeof(rnd)); close(ufd); }
        else memset(rnd, 0, sizeof(rnd));
        char prefix = 'S';
        size_t mlen = strlen(a->model);
        if (mlen >= 4 && memcmp(a->model, "opus", 4) == 0) prefix = 'O';
        else if (mlen >= 5 && memcmp(a->model, "haiku", 5) == 0) prefix = 'H';
        display_name[0] = prefix;
        for (int i = 0; i < 6; i++)
            display_name[1 + i] = b36[rnd[i] % 36];
        display_name[7] = '\0';
    }

    int worker_fd = log_worker_open(display_name);

    log_detail(&lb, worker_fd, "task", a->task, a->task_len, 1);
    log_activity(&lb, a->activity_fd, display_name, "start", "model", a->model);

    struct iovec prompt_iov[2] = {
        { .iov_base = (void *)"TASK: ", .iov_len = 6 },
        { .iov_base = a->task, .iov_len = a->task_len },
    };

    const char **env = collect_env();

    dispatch_ctx_t dctx = {
        .raw_id = a->id_copy,
        .id_len = a->id_len,
        .worker_id = display_name,
        .activity_fd = a->activity_fd,
        .worker_fd = worker_fd,
        .got_result = 0,
        .out = &out,
        .logbuf = &lb,
    };

    int rc = worker_run(NULL, a->cwd, a->model, prompt_iov, 2,
                        env, on_worker_event, &dctx);

    if (rc < 0) {
        emit_tool_error(&out, a->id_copy, a->id_len, "failed to spawn worker");
        log_activity(&lb, a->activity_fd, display_name, "error", NULL, NULL);
    } else if (!dctx.got_result) {
        emit_tool_error(&out, a->id_copy, a->id_len, "worker exited without result");
        log_activity(&lb, a->activity_fd, display_name, "error", NULL, NULL);
    }

    if (worker_fd >= 0) close(worker_fd);
    free(arg);

    pthread_mutex_lock(&threads_mu);
    active_threads--;
    pthread_cond_signal(&threads_cond);
    pthread_mutex_unlock(&threads_mu);
    return NULL;
}

// Parse and validate cwork call, then spawn dispatch thread.
static void handle_cwork(const char *raw_id, size_t id_len,
                         const char *params, size_t params_len,
                         int activity_fd) {
    const char *pkeys[] = {"name", "arguments", NULL};
    json_scan_t ps;
    json_scan_init(&ps, pkeys);
    json_scan(&ps, params, params_len);

    if (!json_str_eq(&ps, 0, "cwork")) {
        emit_response_head(&main_out, raw_id, id_len);
        jb_lit(&main_out, "\"error\":{\"code\":-32601,\"message\":\"unknown tool\"}}");
        emit_flush(&main_out);
        return;
    }
    if (!ps.vals[1]) {
        emit_tool_error(&main_out, raw_id, id_len, "missing arguments");
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
        emit_tool_error(&main_out, raw_id, id_len, "missing required parameter: task");
        return;
    }
    if (!cwd || cwd_len == 0) {
        emit_tool_error(&main_out, raw_id, id_len, "missing required parameter: cwd");
        return;
    }
    if (!model || model_len == 0) {
        model = "sonnet";
        model_len = 6;
    }

    // Allocate thread args with task as flexible array member.
    cwork_args_t *a = malloc(sizeof(cwork_args_t) + task_len + 1);
    if (!a) {
        emit_tool_error(&main_out, raw_id, id_len, "out of memory");
        return;
    }
    zcopyn(a->id_copy, sizeof(a->id_copy), raw_id, id_len);
    a->id_len = id_len < sizeof(a->id_copy) - 1 ? id_len : sizeof(a->id_copy) - 1;
    zcopyn(a->cwd, sizeof(a->cwd), cwd, cwd_len);
    zcopyn(a->model, sizeof(a->model), model, model_len);
    memcpy(a->task, task, task_len);
    a->task[task_len] = '\0';
    a->task_len = task_len;
    a->activity_fd = activity_fd;

    pthread_mutex_lock(&threads_mu);
    active_threads++;
    pthread_mutex_unlock(&threads_mu);

    pthread_t tid;
    pthread_attr_t attr;
    pthread_attr_init(&attr);
    pthread_attr_setdetachstate(&attr, PTHREAD_CREATE_DETACHED);
    pthread_create(&tid, &attr, cwork_thread, a);
    pthread_attr_destroy(&attr);
}

// --- MCP protocol responses ---

static void emit_initialize(jbuf_t *b, const char *raw_id, size_t id_len) {
    emit_response_head(b, raw_id, id_len);
    jb_lit(b,
        "\"result\":{"
            "\"protocolVersion\":\"2025-03-26\","
            "\"serverInfo\":{\"name\":\"cworkers\",\"version\":\"" CWORKERS_VERSION "\"},"
            "\"capabilities\":{\"tools\":{}},"
            "\"instructions\":");
    jb_strn(b, help_agent_data, (size_t)help_agent_size);
    jb_lit(b, "}}");
    emit_flush(b);
}

static void emit_tools_list(jbuf_t *b, const char *raw_id, size_t id_len) {
    emit_response_head(b, raw_id, id_len);
    jb_lit(b,
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
    emit_flush(b);
}

// --- Main loop ---

int work_main(void) {
    jb_init(&main_out, main_out_storage, OUT_CAP);
    jb_bind(&main_out, STDOUT_FILENO);

    int activity_fd = log_activity_open();

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
            emit_initialize(&main_out, raw_id, id_len);
        } else if (method_len == 10 && memcmp(method, "tools/list", 10) == 0) {
            emit_tools_list(&main_out, raw_id, id_len);
        } else if (method_len == 10 && memcmp(method, "tools/call", 10) == 0) {
            if (msg.vals[2])
                handle_cwork(raw_id, id_len, msg.vals[2], msg.lens[2], activity_fd);
        } else if (raw_id) {
            emit_response_head(&main_out, raw_id, id_len);
            jb_lit(&main_out, "\"error\":{\"code\":-32601,\"message\":\"method not found\"}}");
            emit_flush(&main_out);
        }
    }

    // Wait for all dispatch threads to complete before exiting.
    pthread_mutex_lock(&threads_mu);
    while (active_threads > 0)
        pthread_cond_wait(&threads_cond, &threads_mu);
    pthread_mutex_unlock(&threads_mu);

    if (activity_fd >= 0) close(activity_fd);
    return 0;
}
