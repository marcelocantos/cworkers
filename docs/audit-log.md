# Audit Log

Chronological record of audits, releases, documentation passes, and other
maintenance activities. Append-only — newest entries at the bottom.

## 2026-03-10 — /open-source cworkers v0.1.0

- **Commit**: `2b2b0b4`
- **Outcome**: Open-sourced cworkers. Audit: 16 findings, all addressed. Fixed DISPATCH protocol parsing bug (empty model field). Docs: README, DESIGN.md, CLAUDE.md, CONTRIBUTING.md, agents-guide.md written. Released v0.1.0 (darwin-arm64, linux-amd64, linux-arm64) with Homebrew tap formula.
- **Deferred**:
  - go.sum missing (no external deps, but causes cache warnings in CI)
  - homebrew-releaser README table tags not configured in tap repo (non-critical warning)

## 2026-03-10 — /release v0.2.0

- **Commit**: `2a567c3`
- **Outcome**: Released v0.2.0 (darwin-arm64, linux-amd64, linux-arm64). Split agent guide into installation and operational guides. Homebrew formula updated.

## 2026-03-10 — /release v0.3.0

- **Commit**: `f5d4b04`
- **Outcome**: Released v0.3.0 (darwin-arm64, linux-amd64, linux-arm64). Broker runs as brew service. Homebrew formula updated with service block.

## 2026-03-11 — /release v0.4.0

- **Commit**: `802cc2f`
- **Outcome**: Released v0.4.0 (darwin-arm64, linux-amd64, linux-arm64). Added delegation guidance and model selection to operational guide.

## 2026-03-11 — /release v0.5.0

- **Commit**: `2c9d5da`
- **Outcome**: Released v0.5.0 (darwin-arm64, linux-amd64, linux-arm64). Added session-scoped status query (`cworkers status --session <id>`). Reframed delegation guide to default to delegating everything.

## 2026-03-11 — /release v0.6.0

- **Commit**: `8c86c2d`
- **Outcome**: Released v0.6.0 (darwin-arm64, linux-amd64, linux-arm64). Fixed Homebrew version detection (was showing "64" instead of semver) by adding explicit `version` input to homebrew-releaser workflow.

## 2026-03-11 — /release v0.7.0

- **Commit**: `c059ae9`
- **Outcome**: Released v0.7.0 (darwin-arm64, linux-amd64, linux-arm64). Session-scoped workers: `--session` required for worker command, broker routes dispatches only to same-session workers. Breaking WORKER protocol change (pre-1.0). Homebrew formula updated.

## 2026-03-11 — /release v0.8.0

- **Commit**: `93f22eb`
- **Outcome**: Released v0.8.0 (darwin-arm64, linux-amd64, linux-arm64). Added worker pool sizing guidance. Moved operational guide from inline CLAUDE.md to separate file reference pattern. Homebrew formula updated.

## 2026-03-11 — /release v0.9.0

- **Commit**: `505506d`
- **Outcome**: Released v0.9.0 (darwin-arm64, linux-amd64, linux-arm64). Self-warming worker pool: workers spawn replacements before doing work, replacing upfront pool spawning. Removed haiku from model selection. Added burst/team exclusion rules. Homebrew formula updated.

## 2026-03-11 — /release v0.10.0

- **Commit**: `f9a130a`
- **Outcome**: Released v0.10.0 (darwin-arm64, linux-amd64, linux-arm64). Fixed cold-pool bootstrapping: delegation decisions no longer gated on pool status. Dispatch-first flow with Agent fallback. Homebrew formula updated.

## 2026-03-16 — /release v0.11.0

- **Commit**: `1d543eb`
- **Outcome**: Released v0.11.0 (darwin-arm64, linux-amd64, linux-arm64). MCP server rewrite replacing Unix socket CLI protocol. Added Svelte dashboard, SQLite persistence, shadow auto-discovery, depth-controlled worker hierarchies, SSE live events. Updated README, STABILITY.md, and CI/release workflows for Node.js dashboard build. Homebrew formula updated.
