# Targets

<!-- last-evaluated: 505506d -->

## Active

### 🎯T3 v0.9.0 released with self-warming pool
- **Weight**: 3 (value 5 / cost 2)
- **Estimated-cost**: 2
- **Acceptance**:
  - v0.9.0 tag exists on master
  - GitHub release published with release notes
  - Homebrew formula updated via homebrew-releaser
  - STABILITY.md version snapshot updated
  - Audit log entry written
- **Context**: The self-warming pool redesign (commit `505506d`) is on master but not yet tagged or released. Every worker instance spawns a replacement before doing work, replacing the upfront pool spawning ceremony. Haiku removed from model selection.
- **Status**: achieved
- **Discovered**: 2026-03-11

### 🎯T4 cworkers binary detects stale ~/.claude instructions
- **Weight**: 3 (value 5 / cost 2)
- **Estimated-cost**: 2
- **Acceptance**:
  - `cworkers --help-agent` output includes a version or content fingerprint
  - When the installed guide file (`~/.claude/cworkers-guide.md` or similar) exists but doesn't match the current binary's output, the binary emits a warning with remediation instructions
  - Detection works without requiring the user to run a special command (e.g., triggered during `cworkers status` or `cworkers worker`)
- **Context**: After `brew upgrade cworkers`, the operational guide baked into `~/.claude/cworkers-guide.md` may be stale. Users (and their agents) continue following outdated instructions until they manually regenerate. This is a friction point in the upgrade flow.
- **Status**: identified
- **Discovered**: 2026-03-11

## Achieved

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
