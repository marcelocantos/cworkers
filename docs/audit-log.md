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
