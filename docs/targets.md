# Targets

<!-- last-evaluated: 44be630 -->

## Active

### 🎯T6 cworkers operates as an MCP server
- **Weight**: 5 (value 8 / cost 2)
- **Estimated-cost**: 2
- **Acceptance**:
  - Broker runs as an MCP server (streamable HTTP on configurable port, default 4242)
  - Single MCP tool `cwork(task, cwd, model?)` dispatches tasks to pre-spawned `claude -p` workers
  - Shadow auto-registers on first `cwork` call per cwd (no explicit shadow/unshadow commands)
  - Transcript auto-discovery from cwd → `~/.claude/projects/<encoded>/` → most recent .jsonl
  - Progress heartbeats sent every 20s during dispatch to prevent MCP client timeout
  - Pre-warming: each dispatch spawns a replacement worker before returning
  - `help-agent.md` documents MCP usage (embedded via `go:embed`, delivered via `WithInstructions`)
  - First-use hint appended to first `cwork` result per cwd suggesting CLAUDE.md directive
  - `agents-guide.md` updated for MCP setup (`.mcp.json` configuration)
  - `DESIGN.md` updated to reflect MCP architecture
  - Tests pass
- **Context**: Major architecture rewrite from CLI-based Unix socket protocol (6 subcommands) to MCP server (2 subcommands: serve, status). The CLI dispatch/worker/shadow/unshadow commands are replaced by a single `cwork` MCP tool. Code compiles and tests pass. Remaining work: documentation updates (DESIGN.md, agents-guide.md) and release.
- **Status**: converging — code done, docs need update
- **Discovered**: 2026-03-11

### 🎯T4 cworkers binary detects stale ~/.claude instructions
- **Weight**: 3 (value 5 / cost 2)
- **Estimated-cost**: 2
- **Acceptance**:
  - `cworkers --help-agent` output includes a version or content fingerprint
  - When the installed guide file (`~/.claude/cworkers-guide.md` or similar) exists but doesn't match the current binary's output, the binary emits a warning with remediation instructions
  - Detection works without requiring the user to run a special command (e.g., triggered during `cworkers status` or `cworkers worker`)
- **Context**: After `brew upgrade cworkers`, the operational guide baked into `~/.claude/cworkers-guide.md` may be stale. Users (and their agents) continue following outdated instructions until they manually regenerate. This is a friction point in the upgrade flow. **Note**: The MCP rewrite (🎯T6) changes the delivery mechanism — the guide is now embedded via `WithInstructions` at MCP init, so the file-based staleness check may be less relevant. Consider reframing after 🎯T6 is complete.
- **Status**: identified
- **Discovered**: 2026-03-11

## Achieved

### 🎯T5 Session setup is a single command with auto-discovery
- **Status**: achieved — superseded by MCP rewrite (🎯T6). Shadow auto-registers implicitly on first `cwork` call per cwd, eliminating all explicit session setup. The original CLI auto-discovery (commit `44be630`) was intermediate; the MCP approach achieves the goal more completely with zero setup commands.
- **Discovered**: 2026-03-11

### 🎯T3 v0.9.0 released with self-warming pool
- **Status**: achieved — v0.9.0 released, Homebrew updated, audit logged
- **Discovered**: 2026-03-11

### 🎯T1 cworkers is published as open-source on GitHub
- **Status**: achieved

#### 🎯T1.1 GitHub repo created and code pushed
- **Status**: achieved — https://github.com/marcelocantos/cworkers

#### 🎯T1.2 v0.1.0 released
- **Status**: achieved — https://github.com/marcelocantos/cworkers/releases/tag/v0.1.0

#### 🎯T1.3 Audit log entry written
- **Status**: achieved

### 🎯T2 Workers are session-scoped
- **Status**: achieved — released as v0.7.0

#### 🎯T2.1 main.go compiles with session-scoped workers
- **Status**: achieved

#### 🎯T2.2 All tests pass with session-scoped workers
- **Status**: achieved

#### 🎯T2.3 Docs updated for session-scoped workers
- **Status**: achieved
