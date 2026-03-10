# Convergence Targets

## 🎯T1 cworkers is published as open-source on GitHub

Status: achieved.

### 🎯T1.1 GitHub repo created and code pushed

Status: achieved — https://github.com/marcelocantos/cworkers

### 🎯T1.2 v0.1.0 released

Status: achieved — https://github.com/marcelocantos/cworkers/releases/tag/v0.1.0

### 🎯T1.3 Audit log entry written

Status: achieved.

## 🎯T2 Workers are session-scoped

Workers register with `--session <id>` and the broker only routes
dispatches to workers from the same session. This prevents cross-session
task leakage when multiple Claude Code sessions share a single broker.

Status: code complete — needs release as v0.7.0.

### 🎯T2.1 main.go compiles with session-scoped workers

`worker()` and `workerTryOnce()` accept and send session on the wire.
Protocol: `WORKER <model> <session>\n`.

Status: achieved.

### 🎯T2.2 All tests pass with session-scoped workers

Tests updated to use new signatures and protocol. Cross-session isolation
tests added (pool, waiter, E2E).

Status: achieved.

### 🎯T2.3 Docs updated for session-scoped workers

help-agent.md, DESIGN.md, CLAUDE.md protocol spec, STABILITY.md, global
CLAUDE.md reference table.

Status: achieved.
