// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

#include "log.h"

#include <fcntl.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/uio.h>
#include <unistd.h>

static void ensure_dir(const char *path) {
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

int log_open(void) {
    const char *home = getenv("HOME");
    if (!home) return -1;

    char path[512];
    size_t hlen = strlen(home);
    size_t slen = strlen(LOG_PATH_SUFFIX);
    if (hlen + slen + 1 > sizeof(path)) return -1;
    memcpy(path, home, hlen);
    memcpy(path + hlen, LOG_PATH_SUFFIX, slen + 1);

    ensure_dir(path);
    return open(path, O_WRONLY | O_CREAT | O_APPEND, 0644);
}

void log_write(int fd, const char *line, size_t len) {
    if (fd < 0 || len == 0) return;
    // Use writev to write line + newline atomically (single syscall).
    // O_APPEND ensures atomic positioning even with concurrent writers.
    struct iovec iov[2];
    iov[0].iov_base = (void *)line;
    iov[0].iov_len = len;
    iov[1].iov_base = (void *)"\n";
    iov[1].iov_len = 1;
    writev(fd, iov, 2);
}
