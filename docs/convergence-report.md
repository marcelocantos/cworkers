# Convergence Report

Standing invariants: all green. Tests pass locally. No CI configured.

## Movement

- 🎯T6: converging → **close** (all acceptance criteria now met — docs updated, first-use hint implemented)
- 🎯T4: (unchanged — identified, may need reframing for MCP)
- 🎯T7: (new target — not started)

## Gap Report

### 🎯T6 cworkers operates as an MCP server  [weight 5]
Gap: **close**
All acceptance criteria are met in the codebase: MCP server on port 4242, `cwork` tool dispatching to workers, shadow auto-registration, transcript auto-discovery, progress heartbeats, pre-warming, `help-agent.md` embedded via `go:embed`/`WithInstructions`, first-use hint per cwd, `agents-guide.md` updated for MCP setup, `DESIGN.md` reflects MCP architecture, and tests pass. The only gap is delivery — this work is on master but no release has been cut since v0.10.0.

  Implied: code achieved but not yet released (v0.10.0 is current; post-MCP work not yet tagged)

### 🎯T4 cworkers binary detects stale ~/.claude instructions  [weight 3]
Gap: **not started**
No implementation work begun. The MCP rewrite (🎯T6) changes the delivery mechanism — the guide is now embedded via `WithInstructions` at MCP init, making file-based staleness detection less relevant. This target likely needs reframing: the "stale instructions" problem is now about stale CLAUDE.md directives rather than stale guide files.

### 🎯T7 Worker sessions are identified by nonce, not heuristics  [weight 4]
Gap: **not started**
No nonce implementation found in main.go. Workers are still identified via depth URL params and transcript heuristic. This is a greenfield implementation task.

## Recommendation

Work on: **🎯T6 cworkers operates as an MCP server**
Reason: Highest effective weight (4.0) and gap is "close" — all code criteria are met. The only remaining work is delivery (cutting a release). Closing this target is the highest-leverage action since it's nearly free.

## Suggested action

Run `/release` to cut a new version (v0.11.0) that includes the MCP architecture, SQLite persistence, Svelte dashboard, and session tracking. This would achieve 🎯T6's delivery criterion and free up focus for 🎯T7.

<!-- convergence-deps
evaluated: 2026-03-13T22:00:00Z
sha: fea43bb

🎯T6:
  gap: close
  assessment: "All acceptance criteria met in code. Not yet released — v0.10.0 is current, post-MCP work on master not tagged."
  read:
    - main.go
    - DESIGN.md
    - agents-guide.md
    - help-agent.md
    - docs/targets.md

🎯T4:
  gap: not started
  assessment: "No implementation. MCP rewrite changes delivery mechanism — target may need reframing."
  read:
    - main.go
    - docs/targets.md

🎯T7:
  gap: not started
  assessment: "No nonce implementation found. Workers still identified by depth params and transcript heuristic."
  read:
    - main.go
    - docs/targets.md
-->
