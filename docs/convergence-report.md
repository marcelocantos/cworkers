# Convergence Report

Standing invariants: all green. No CI configured; tests pass locally (cached).

## Movement

- 🎯T3: (unchanged — achieved, should move to Achieved section)
- 🎯T5: close → **superseded by MCP rewrite** (CLI `shadow` subcommand removed in uncommitted changes)
- 🎯T4: (unchanged — not started, but framing may need update for MCP)

## Major finding: uncommitted MCP architecture rewrite

There are **massive uncommitted changes** (-1844/+723 lines across main.go, main_test.go, help-agent.md, go.mod) that fundamentally restructure cworkers from a CLI-based protocol to an MCP server. Key changes:

- Subcommands reduced from 6 (`serve`, `worker`, `dispatch`, `shadow`, `unshadow`, `status`) to 2 (`serve`, `status`)
- Single MCP tool `cwork(task, cwd, model?)` replaces CLI dispatch/worker flow
- Shadow auto-registered implicitly on first `cwork` call per cwd (no explicit shadow/unshadow)
- help-agent.md completely rewritten for MCP interaction model

This rewrite has **no convergence target**. It supersedes aspects of both 🎯T5 and 🎯T4.

## Gap Report

### 🎯T5 Session setup is a single command with auto-discovery  [weight 4]
Gap: **needs reframing**
The committed code (44be630) implemented CLI auto-discovery per the acceptance criteria. However, the uncommitted MCP rewrite removes the `shadow` subcommand entirely — shadow registration is now implicit via `cwork(task, cwd)`. The acceptance criteria reference CLI flags (`--session`, `--transcript`) and subcommands that no longer exist in the working tree. The *spirit* of the target (frictionless session setup) is achieved more completely by the MCP approach, but the *letter* of the acceptance criteria is no longer applicable.

### 🎯T4 cworkers binary detects stale ~/.claude instructions  [weight 2.5]
Gap: not started
No implementation work begun. The MCP rewrite changes the delivery mechanism (MCP `WithInstructions` delivers the usage guide at init instead of a file at `~/.claude/cworkers-guide.md`), so the staleness detection framing may need updating — the guide is now embedded in the MCP server's init response rather than a separate file the agent reads.

### 🎯T3 v0.9.0 released with self-warming pool  [weight 2.5]
Gap: achieved
All acceptance criteria met. v0.9.0 released. Should be moved from Active to Achieved in targets.md.

## Recommendation

Work on: **creating a target for the MCP architecture rewrite and committing it**
Reason: There are ~2500 lines of uncommitted changes with no convergence target. This is the highest-leverage action — it establishes the source of truth for the rewrite, allows 🎯T5 and 🎯T4 to be reframed against the new architecture, and prevents context loss. The code compiles and tests pass, making it safe to commit.

## Suggested action

1. Create 🎯T6 for the MCP architecture rewrite with acceptance criteria reflecting the new design.
2. Reframe 🎯T5's acceptance criteria for MCP (implicit shadow via cwd, no CLI flags).
3. Move 🎯T3 to Achieved section.
4. Commit the uncommitted MCP changes.

<!-- convergence-deps
evaluated: 2026-03-11T19:00:00Z
sha: 44be630

🎯T3:
  gap: achieved
  assessment: "All acceptance criteria met. v0.9.0 released. Should move to Achieved."
  read:
    - docs/targets.md

🎯T4:
  gap: not started
  assessment: "No implementation work. Framing needs update for MCP architecture."
  read:
    - docs/targets.md
    - main.go
    - help-agent.md

🎯T5:
  gap: needs reframing
  assessment: "CLI auto-discovery implemented but superseded by MCP rewrite. Acceptance criteria reference removed subcommands."
  read:
    - docs/targets.md
    - main.go
    - help-agent.md
-->
