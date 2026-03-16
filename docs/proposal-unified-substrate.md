# Proposal: Unified Agent Substrate (cworkers + doit)

## Thesis

cworkers and doit solve complementary problems within the same scope:
a single Claude Code session's operational substrate. cworkers provides
**context-compressed delegation** — keeping the root agent's context lean
by offloading work to pooled workers whose execution detail never pollutes
the root. doit provides **safe execution** — a tiered policy chain that
gates every shell command through deterministic rules, learned patterns,
and LLM reasoning before it runs. Both are invisible to the user. Both
operate within a session. Both enhance the agent automatically. They
belong together.

Jevon, by contrast, is a **user-facing product** — a cross-repo
orchestrator with its own UI, conversation loop, and intent model. It
sits above sessions, directing them. It would consume the unified
substrate as infrastructure, not merge with it.

## The Stack

```
jevon             user intent → sessions (cross-repo, explicit, user-facing)
  └─ substrate    session → safe, context-clean delegation (intra-session, automatic)
       ├─ pool    warm workers, model routing, shadow context
       └─ gate    policy chain, audit trail, safe execution
```

Jevon spawns sessions. Each session's agent uses the substrate
automatically. The substrate handles capacity (pool) and safety (gate).
The user never interacts with the substrate directly — it's plumbing.

## Why Merge

### The problems are coupled

A delegated task includes command execution. Today, a cworkers worker
runs commands with no policy awareness. A doit-gated session has no
context-compressed delegation. To get both, you'd configure two
daemons, two MCP servers, two brew services — and somehow wire them
together. Merging eliminates this: every delegated task flows through
the policy chain automatically.

### Shared infrastructure

Both projects:
- Are single-binary Go projects with zero/minimal external deps
- Use `mark3labs/mcp-go` for MCP server scaffolding
- Manage Claude CLI process lifecycle (spawn, pipe, read output)
- Maintain session-scoped state (shadow transcripts / policy context)
- Distribute via Homebrew with `agents-guide.md` + `help-agent.md`
- Target the same user: someone running Claude Code who wants it to
  work better without manual intervention

### Coherent configuration

Pool sizing, model preferences, safety tiers, per-project policy,
shadow context depth — these are all facets of "how this session's
agent operates." One config surface, one daemon, one MCP server.

### Single audit trail

"What was delegated" and "what was executed" belong in the same log.
Today, cworkers has no audit trail and doit has no delegation awareness.
Merged, every entry captures: who delegated, what task, what commands
ran, what policy decisions were made, what the outcome was.

## Architecture

### Daemon

One persistent process per user, started as a brew service. Manages:
- **Worker pool**: pre-warmed Claude CLI processes, keyed by
  cwd + model. Self-warming replenishment after each dispatch.
- **Shadow registry**: per-token transcript tailers providing
  conversation context to workers.
- **Policy engine**: L1 (deterministic rules) → L2 (learned patterns)
  → L3 (LLM gatekeeper). Evaluated for every command a worker executes.
- **Audit log**: hash-chained, covering both delegation and execution.

### MCP Surface

Exposed as a StreamableHTTP MCP server. Tools:

| Tool | Purpose |
|------|---------|
| `shadow(cwd, token)` | Register transcript, start tailing |
| `delegate(task, token, model?)` | Dispatch task to pooled worker |
| `dry_run(command, cwd)` | Evaluate command against policy without executing |
| `status()` | Pool state, shadow count, policy stats |

Workers spawned by the pool have the policy engine wired in as their
execution layer — commands flow through the gate automatically. The
root agent doesn't call a separate "execute safely" tool; safety is
a property of delegation, not a separate action.

### Policy Chain (from doit)

Retained wholesale. Three levels:

1. **L1 — Deterministic rules** (<1ms): hardcoded safety tiers
   (read/build/write/dangerous), argument blocklists (`rm -rf /`,
   `git push --force`), per-project policy (`.doit/config.yaml`,
   tighten-only merge). Future: Starlark-expressed rules.
2. **L2 — Learned patterns** (<10ms): repo-specific decisions that
   have stabilised from L3 adjudication.
3. **L3 — LLM gatekeeper** (1–5s): novel commands evaluated by a
   lightweight model with project context. Decisions migrate to L2,
   then L1 as patterns stabilise.

### Worker Lifecycle (from cworkers)

Retained with adaptation:

- Broker spawns `claude -p` processes, holds stdin pipe open.
- On dispatch: writes prompt (shadow context + task) to stdin, reads
  NDJSON response from stdout, process exits.
- Broker immediately spawns a replacement (self-warming).
- Workers inherit the policy engine — their command execution routes
  through the gate, not raw `sh -c`.

### Shadow Context (from cworkers)

Retained as-is. Token-indexed transcript tailers with rolling windows.
Workers receive recent conversation context so they understand the
session's current state without the root agent summarising.

## What Changes for Each Project

### cworkers absorbs

- doit's policy engine (`engine/`, `internal/policy/`,
  `internal/cap/`, `internal/config/`)
- doit's audit infrastructure (`internal/audit/`)
- doit's MCP tools (merged into the unified MCP surface)
- doit's per-project config (`.doit/config.yaml` semantics)

### doit contributes, then sunsets

- Policy engine, audit, config, capability registry move into the
  merged project.
- `cmd/doit-mcp/` and `cmd/doit/` are replaced by the unified binary.
- doit repo archived with a pointer to the merged project.
- Legacy code paths (`internal/daemon/`, `internal/client/`,
  `internal/ipc/`, unicode operators) are dropped — they were already
  targeted for removal (🎯T6).

### Jevon consumes

- When jevon spawns a worker session for a repo, that session uses
  the unified substrate automatically (if installed). No jevon code
  changes needed — it's transparent.
- jevon does **not** merge. It remains a separate user-facing product.

## Naming

"cworkers" describes the pooling/delegation side. "doit" describes the
execution/safety side. Neither name captures the merged concept.

Options to consider:
- **cworkers** (keep it, expand scope) — established, has releases,
  Homebrew tap. "workers" is broad enough to encompass "workers that
  execute safely." Least disruptive.
- **agentops** — describes the category (agent operations substrate).
  Clear but generic.
- **substrate** — technically precise but obscure.
- **Something new** — open question.

Recommendation: keep **cworkers** as the project name and absorb doit's
capabilities as a feature ("cworkers now gates command execution"). The
brand is established, the Homebrew formula exists, and the name is
flexible enough. The policy engine becomes a module within cworkers,
not a separately branded thing.

## Migration Path

### Phase 1: Shared interfaces

Extract the common Claude process management code (spawn `claude -p`,
pipe stdin, read NDJSON stdout, manage lifecycle) into a shared
internal package. Both projects already do this differently — align on
one implementation.

### Phase 2: Import doit's engine

Bring doit's `engine/`, `internal/policy/`, `internal/cap/`,
`internal/config/`, and `internal/audit/` packages into the cworkers
repo. Wire the policy engine into worker command execution. Add
`dry_run` to the MCP surface.

### Phase 3: Unified MCP and config

Merge the MCP tool surfaces. Unify configuration (pool settings +
policy settings in one file or config directory). Single daemon, single
brew service.

### Phase 4: Archive doit

Archive the doit repo with a README pointing to cworkers. Update
Homebrew formula. Bump cworkers to a new minor version reflecting the
expanded scope.

## What This Enables

- **Every delegated task is safe by default.** No separate setup, no
  second daemon, no extra MCP config. Delegate a task, and its commands
  flow through the policy chain automatically.
- **Single audit trail** covering delegation decisions and execution
  decisions. "Why did the agent run `rm -rf build/`?" is answered in
  one place.
- **Context compression + safety composing.** The root agent stays lean
  (cworkers' value) while workers execute responsibly (doit's value).
  Neither benefit requires the other, but together they define what
  "well-behaved agent infrastructure" means.
- **Jevon gets safety for free.** Any session jevon spawns that has
  cworkers installed inherits both pooling and policy. No integration
  work in jevon itself.
- **One install, one config, one service.** Users (and their agents)
  manage one thing instead of three.

## Open Questions

1. **Does doit's per-project config (`.doit/config.yaml`) keep its
   path, or move under a cworkers-specific directory?** Keeping `.doit/`
   avoids churn for existing users. Adding `.cworkers/policy.yaml` is
   cleaner long-term.

2. **Should the policy engine be optional?** Some users may want
   delegation without safety gates (development/experimentation).
   A `--no-policy` flag or config toggle could handle this, but
   defaults matter — safe by default is the right call.

3. **L3 gatekeeper model selection.** doit uses Claude for L3. In the
   merged system, should L3 use one of the pooled workers (efficient
   but circular) or a dedicated lightweight model (haiku)?

4. **Naming.** See above. This warrants user input.
