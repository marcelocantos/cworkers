// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

#include "log.h"

#include <fcntl.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/uio.h>
#include <unistd.h>

static int log_dir(char *dst, size_t cap) {
    const char *home = getenv("HOME");
    if (!home) return -1;
    size_t hlen = strlen(home);
    size_t slen = strlen(LOG_DIR_SUFFIX);
    if (hlen + slen + 1 > cap) return -1;
    memcpy(dst, home, hlen);
    memcpy(dst + hlen, LOG_DIR_SUFFIX, slen + 1);
    return 0;
}

static void mkdirs(const char *path) {
    char tmp[512];
    size_t len = strlen(path);
    if (len >= sizeof(tmp)) return;
    memcpy(tmp, path, len + 1);
    for (size_t i = 1; i < len; i++) {
        if (tmp[i] == '/') {
            tmp[i] = '\0';
            mkdir(tmp, 0755);
            tmp[i] = '/';
        }
    }
}

int log_activity_open(void) {
    char dir[512];
    if (log_dir(dir, sizeof(dir)) < 0) return -1;
    size_t dlen = strlen(dir);
    char path[560];
    memcpy(path, dir, dlen);
    memcpy(path + dlen, "/activity.jsonl", 16);
    mkdirs(path);
    return open(path, O_WRONLY | O_CREAT | O_APPEND, 0644);
}

int log_worker_open(const char *worker_id) {
    char dir[512];
    if (log_dir(dir, sizeof(dir)) < 0) return -1;
    size_t dlen = strlen(dir);
    size_t idlen = strlen(worker_id);
    char path[600];
    if (dlen + 9 + idlen + 7 >= sizeof(path)) return -1;
    memcpy(path, dir, dlen);
    memcpy(path + dlen, "/workers/", 9);
    memcpy(path + dlen + 9, worker_id, idlen);
    memcpy(path + dlen + 9 + idlen, ".jsonl", 7);
    mkdirs(path);
    return open(path, O_WRONLY | O_CREAT | O_APPEND, 0644);
}

void log_write(int fd, const char *line, size_t len) {
    if (fd < 0 || len == 0) return;
    struct iovec iov[2];
    iov[0].iov_base = (void *)line;
    iov[0].iov_len = len;
    iov[1].iov_base = (void *)"\n";
    iov[1].iov_len = 1;
    writev(fd, iov, 2);
}
