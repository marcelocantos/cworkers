# Targets

<!-- last-evaluated: 6da0836 -->

## Active

### 🎯T8 cworkers is rewritten in C
- **Weight**: 5 (value 8 / cost 8)
- **Estimated-cost**: 8
- **Acceptance**:
  - Single C binary replaces Go binary for all subcommands (serve, work, status)
  - Binary size under 1MB (static, no runtime dependencies)
  - Instant startup for `work` command (stdio MCP frontend, spawned per session)
  - Vendored dependencies: cJSON (MIT), SQLite3 amalgamation (public domain), HTTP server library (MIT/permissive)
  - All current functionality preserved: dispatch API, dashboard, SSE, SQLite observability
  - Cross-compilation without CGO headaches
  - Environment variable passthrough from `work` to daemon to `claude -p` workers (corporate proxy/TLS support)
  - Unix domain socket for daemon communication (replaces TCP port 4242)
- **Context**: Go + CGO (for SQLite) produces ~15MB binaries and has cross-compilation friction. The `work` command is spawned per Claude Code session — it needs to be lightweight. C gives ~100KB static binary, instant startup, zero runtime. The current Go codebase is ~1200 lines after simplification; C equivalent estimated at 2000-3000 lines. Vendor cJSON, SQLite amalgamation (with aggressive `#define` stripping — disable FTS, JSON1, RTree, window functions, etc.), and a lightweight HTTP server (civetweb or similar).
- **Status**: identified
- **Discovered**: 2026-03-17

### 🎯T9 Worker env vars propagate through dispatch chain
- **Weight**: 4 (value 8 / cost 3)
- **Estimated-cost**: 3
- **Acceptance**:
  - `cworkers work` captures relevant env vars from its environment (pattern-matched: `ANTHROPIC_*`, `CLAUDE_*`, `AWS_*`, `*_PROXY`, `*_proxy`, `NODE_EXTRA_CA_CERTS`)
  - Captured vars are sent to daemon via the dispatch request
  - Daemon applies them to spawned `claude -p` worker processes
  - Config file `~/.config/cworkers/config.json` supports `env` map for daemon-side defaults
  - CLI-passed vars override config vars
- **Context**: Corporate environments require `NODE_EXTRA_CA_CERTS`, proxy vars, and custom API endpoints to be available to workers. The brew service daemon doesn't inherit the user's shell environment. The stdio `work` command has the right env (inherited from Claude Code) and can propagate it to the daemon, which applies it to workers.
- **Status**: identified
- **Discovered**: 2026-03-17

### 🎯T7 Worker sessions are identified by nonce, not heuristics
- **Weight**: 4 (value 7 / cost 3)
- **Estimated-cost**: 3
- **Acceptance**:
  - Broker assigns identity at activation time, not spawn time
  - Worker display names and parent-child relationships managed by broker state
  - No identity baked into `claude -p` process arguments
  - Concurrent sessions in the same CWD are correctly distinguished
- **Context**: Currently workers carry identity via URL params in `--mcp-config`. This prevents generic pre-warming and creates incorrect hierarchical naming. Identity should be a broker-side concept assigned at dispatch time. With the move to stdio MCP + dispatch API, workers no longer connect back via MCP — the broker holds stdin/stdout directly.
- **Status**: identified
- **Discovered**: 2026-03-13

### 🎯T4 cworkers binary detects stale ~/.claude instructions
- **Weight**: 3 (value 5 / cost 2)
- **Estimated-cost**: 2
- **Acceptance**:
  - `cworkers --help-agent` output includes a version or content fingerprint
  - When the installed guide is stale relative to the binary, a warning is emitted with remediation
  - Detection is passive (triggered during normal operation, not a special command)
- **Context**: After `brew upgrade`, the operational guide delivered via `WithInstructions` auto-updates, but agents-guide.md references may be stale. Less critical since the MCP rewrite delivers the operational guide automatically.
- **Status**: identified
- **Discovered**: 2026-03-11

## Achieved

### 🎯T6 cworkers operates as an MCP server
- **Status**: achieved — v0.15.0 released. Stateless MCP-to-CLI bridge with stdio and HTTP modes, SQLite observability, Svelte dashboard.
- **Discovered**: 2026-03-11

### 🎯T5 Session setup is a single command with auto-discovery
- **Status**: achieved — superseded by MCP rewrite (🎯T6).
- **Discovered**: 2026-03-11

### 🎯T3 v0.9.0 released with self-warming pool
- **Status**: achieved
- **Discovered**: 2026-03-11

### 🎯T1 cworkers is published as open-source on GitHub
- **Status**: achieved

### 🎯T2 Workers are session-scoped
- **Status**: achieved — released as v0.7.0
