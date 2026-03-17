// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Spawn claude -p via posix_spawnp and stream-parse its NDJSON output.

#include "worker.h"
#include "json.h"

#include <spawn.h>
#include <string.h>
#include <sys/uio.h>
#include <sys/wait.h>
#include <unistd.h>

extern char **environ;

// --- Environment merging ---

// Build envp into static buffer: parent env filtered (no CLAUDECODE) + extras.
#define MAX_ENVP 4096
static char *envp_buf[MAX_ENVP + 1];

static char **build_env(const char **env_extra) {
    int idx = 0;
    for (char **e = environ; *e && idx < MAX_ENVP; e++) {
        if (strncmp(*e, "CLAUDECODE=", 11) != 0)
            envp_buf[idx++] = *e;
    }
    if (env_extra) {
        for (int i = 0; env_extra[i] && idx < MAX_ENVP; i++)
            envp_buf[idx++] = (char *)env_extra[i];
    }
    envp_buf[idx] = NULL;
    return envp_buf;
}

// --- NDJSON line parser ---

static void parse_ndjson_line(const char *line, size_t len,
                              worker_event_fn event_fn, void *ctx) {
    event_fn(WE_LINE, line, len, ctx);

    const char *keys[] = {"type", "message", "subtype", "result", NULL};
    json_scan_t top;
    json_scan_init(&top, keys);
    if (json_scan(&top, line, len) < 0) return;

    size_t type_len;
    const char *type = json_str(&top, 0, &type_len);
    if (!type) return;

    if (type_len == 9 && memcmp(type, "assistant", 9) == 0) {
        if (!top.vals[1]) return;

        const char *msg_keys[] = {"content", NULL};
        json_scan_t msg;
        json_scan_init(&msg, msg_keys);
        if (json_scan(&msg, top.vals[1], top.lens[1]) < 0) return;
        if (!msg.vals[0]) return;

        // Walk content array for text and tool_use blocks.
        const char *p = msg.vals[0];
        size_t plen = msg.lens[0];
        if (plen < 2 || p[0] != '[') return;
        const char *end = p + plen;
        p++;

        while (p < end && *p != ']') {
            while (p < end && (*p == ' ' || *p == ',' || *p == '\n')) p++;
            if (p >= end || *p != '{') break;

            const char *obj_start = p;
            int depth = 1;
            p++;
            while (p < end && depth > 0) {
                if (*p == '"') {
                    p++;
                    while (p < end && *p != '"') { if (*p == '\\') p++; p++; }
                    if (p < end) p++;
                    continue;
                }
                if (*p == '{') depth++;
                else if (*p == '}') depth--;
                p++;
            }
            size_t obj_len = (size_t)(p - obj_start);

            const char *blk_keys[] = {"type", "text", "name", NULL};
            json_scan_t blk;
            json_scan_init(&blk, blk_keys);
            json_scan(&blk, obj_start, obj_len);

            size_t btype_len;
            const char *btype = json_str(&blk, 0, &btype_len);
            if (btype) {
                if (btype_len == 4 && memcmp(btype, "text", 4) == 0) {
                    size_t txt_len;
                    const char *txt = json_str(&blk, 1, &txt_len);
                    if (txt) event_fn(WE_TEXT, txt, txt_len, ctx);
                } else if (btype_len == 8 && memcmp(btype, "tool_use", 8) == 0) {
                    size_t name_len;
                    const char *name = json_str(&blk, 2, &name_len);
                    if (name) event_fn(WE_TOOL_USE, name, name_len, ctx);
                }
            }
        }
    } else if (type_len == 6 && memcmp(type, "result", 6) == 0) {
        size_t sub_len;
        const char *sub = json_str(&top, 2, &sub_len);

        if (sub && sub_len == 7 && memcmp(sub, "success", 7) == 0) {
            size_t res_len;
            const char *res = json_str(&top, 3, &res_len);
            event_fn(WE_RESULT, res ? res : "", res ? res_len : 0, ctx);
        } else {
            size_t res_len;
            const char *res = json_str(&top, 3, &res_len);
            event_fn(WE_ERROR, res ? res : "worker error",
                     res ? res_len : 12, ctx);
        }
    }
}

// --- Main entry ---

int worker_run(const char *claude_path,
               const char *cwd, const char *model,
               const struct iovec *prompt_iov, int prompt_iovcnt,
               const char **env_extra,
               worker_event_fn event_fn, void *ctx) {
    if (!claude_path || !claude_path[0]) claude_path = "claude";

    const char *argv[16];
    int argc = 0;
    argv[argc++] = claude_path;
    argv[argc++] = "-p";
    argv[argc++] = "--verbose";
    argv[argc++] = "--output-format";
    argv[argc++] = "stream-json";
    argv[argc++] = "--dangerously-skip-permissions";
    if (model && model[0]) {
        argv[argc++] = "--model";
        argv[argc++] = model;
    }
    argv[argc] = NULL;

    char **envp = build_env(env_extra);
    if (!envp) return -1;

    // Pipes: child reads from pipe_in[0], parent writes to pipe_in[1].
    //        child writes to pipe_out[1], parent reads from pipe_out[0].
    int pipe_in[2], pipe_out[2];
    if (pipe(pipe_in) < 0 || pipe(pipe_out) < 0) {
        (void)envp;
        return -1;
    }

    // Set up file actions for the child.
    posix_spawn_file_actions_t actions;
    posix_spawn_file_actions_init(&actions);
    posix_spawn_file_actions_addclose(&actions, pipe_in[1]);
    posix_spawn_file_actions_addclose(&actions, pipe_out[0]);
    posix_spawn_file_actions_adddup2(&actions, pipe_in[0], STDIN_FILENO);
    posix_spawn_file_actions_adddup2(&actions, pipe_out[1], STDOUT_FILENO);
    posix_spawn_file_actions_addclose(&actions, pipe_in[0]);
    posix_spawn_file_actions_addclose(&actions, pipe_out[1]);

    // Set up attributes for chdir.
    posix_spawnattr_t attr;
    posix_spawnattr_init(&attr);
    if (cwd && cwd[0]) {
        // posix_spawnattr doesn't support chdir portably.
        // On macOS, posix_spawn_file_actions_addchdir_np is available.
#if defined(__APPLE__) && __MAC_OS_X_VERSION_MAX_ALLOWED >= 260000
        posix_spawn_file_actions_addchdir(&actions, cwd);
#elif defined(__APPLE__)
        posix_spawn_file_actions_addchdir_np(&actions, cwd);
#elif defined(__linux__) && defined(_GNU_SOURCE)
        posix_spawn_file_actions_addchdir_np(&actions, cwd);
#endif
    }

    pid_t pid;
    int rc = posix_spawnp(&pid, claude_path,
                          &actions, &attr,
                          (char *const *)argv, envp);

    posix_spawn_file_actions_destroy(&actions);
    posix_spawnattr_destroy(&attr);

    if (rc != 0) {
        close(pipe_in[0]); close(pipe_in[1]);
        close(pipe_out[0]); close(pipe_out[1]);
        return -1;
    }

    // Parent: close child's ends.
    close(pipe_in[0]);
    close(pipe_out[1]);

    // Write prompt via writev, then close stdin.
    {
        writev(pipe_in[1], prompt_iov, prompt_iovcnt);
        close(pipe_in[1]);
    }

    // Read NDJSON with 4KB buffer, process line by line.
    char rbuf[4096];
    char linebuf[65536];
    size_t line_pos = 0;

    for (;;) {
        ssize_t n = read(pipe_out[0], rbuf, sizeof(rbuf));
        if (n <= 0) break;

        for (ssize_t i = 0; i < n; i++) {
            if (rbuf[i] == '\n') {
                if (line_pos > 0) {
                    linebuf[line_pos] = '\0';
                    parse_ndjson_line(linebuf, line_pos, event_fn, ctx);
                    line_pos = 0;
                }
            } else if (line_pos < sizeof(linebuf) - 1) {
                linebuf[line_pos++] = rbuf[i];
            }
        }
    }
    if (line_pos > 0) {
        linebuf[line_pos] = '\0';
        parse_ndjson_line(linebuf, line_pos, event_fn, ctx);
    }

    close(pipe_out[0]);

    int status;
    waitpid(pid, &status, 0);
    return 0;
}
