// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// cwork — Stdio MCP frontend for cworkers.
// Entry point. Delegates to work_main().

#include <string.h>
#include <unistd.h>

#ifndef CWORKERS_VERSION
#define CWORKERS_VERSION "dev"
#endif

int work_main(void);

static const char usage[] =
    "usage: cwork [--version] [--help]\n"
    "\n"
    "Stdio MCP server. Spawns claude -p workers, returns results.\n"
    "Configure in .mcp.json:\n"
    "  {\"type\": \"stdio\", \"command\": \"cwork\"}\n";

int main(int argc, char **argv) {
    if (argc > 1) {
        if (strcmp(argv[1], "--version") == 0) {
            write(STDOUT_FILENO, CWORKERS_VERSION "\n",
                  sizeof(CWORKERS_VERSION));
            return 0;
        }
        if (strcmp(argv[1], "--help") == 0 || strcmp(argv[1], "-h") == 0) {
            write(STDOUT_FILENO, usage, sizeof(usage) - 1);
            return 0;
        }
    }
    return work_main();
}
