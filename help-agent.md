# cworkers — Task Delegation via MCP

cworkers is a task broker that delegates work to worker agents. Workers are
pre-spawned `claude -p` processes managed by the broker — dispatched tasks
start instantly with no startup overhead.

## Usage

Call `cwork` to delegate a task. Pass your working directory and the task
description. Workers start with a clean context — they have access to the
project's CLAUDE.md and tools, but no awareness of your conversation. Write
task descriptions that include all the context the worker needs.

## Writing Good Task Descriptions

Workers start fresh. They don't know what you've been discussing, what files
you've read, or what decisions you've made. Include:

- **What to do** — the specific action or question
- **Why** — enough background that the worker can make good judgement calls
- **Where** — file paths, function names, relevant code locations
- **What to return** — the format and level of detail you need back

Bad: "fix the build error"
Good: "Run `make` in /path/to/project and fix any compilation errors. The
recent change was adding a `db_path` field to the config struct in main.go.
Report what was wrong and what you changed."

## When to Delegate

**Default to delegating.** Every tool call, file read, search, build, or test
you run in the root session grows your context window and brings you closer to
compression. Workers absorb that cost instead.

Delegate aggressively:
- **Any file reads or searches** — send a worker to explore and summarise
- **Code changes** — describe what to change, let a worker implement it
- **Builds and tests** — workers run them and report results
- **Research** — codebase exploration, doc reading, dependency analysis
- **Bulk work** — applying patterns across files, migrations, refactors

The only things that **must** stay in the root session:
- Direct conversation with the user (clarifying questions, presenting options)
- Orchestration decisions (what to do next based on worker results)
- Operations whose output you already know is trivial (e.g., `git status`
  when you just committed). If the output *might* be large — builds, tests,
  file reads — delegate it. You cannot predict output size, so err on the
  side of delegating.

## Model Selection

The `model` parameter on `cwork` controls which model the worker uses.

- **sonnet** (default) — Well-scoped coding tasks, mechanical changes, writing
  tests, running builds, triaging errors, anything with clear structure.
- **opus** — Complex reasoning, architectural decisions, novel problem-solving,
  deep code analysis, tasks where getting it right matters more than speed.
- **haiku** — Fast and lightweight. Good for simple lookups, mechanical
  find-and-replace, running builds/tests, and triaging output.

When in doubt, use sonnet. Reserve opus for tasks that genuinely need deeper
reasoning.

## Parallelism

**Workers are extremely cheap to start.** The broker pre-warms the pool — after
each dispatch, a replacement worker is spawned so the next task starts instantly.
Leverage this aggressively:

- **Fan out independent work.** If you need to read 3 files, search for 2
  patterns, and run a build — fire all of them as parallel `cwork` calls
  rather than sequencing them. Each one is a separate worker with its own
  context window.
- **Don't batch into a single worker** what could be separate parallel tasks.
  A worker that reads a file, then searches, then builds is slower than three
  workers doing each concurrently. Split along natural boundaries.
- **Research fan-out.** When investigating a problem, dispatch multiple workers
  to explore different hypotheses or codepaths simultaneously.
- **Bulk changes.** When applying a pattern across N files, dispatch one worker
  per file (or per small group) rather than one worker for all files.

The only reason to sequence `cwork` calls is when a later task depends on the
result of an earlier one.

## Tips

- **Context is the bottleneck.** The whole point of delegating is to keep the
  root session's context window small. The worker's result flows back into your
  context, so tell the worker what format you need — a one-line summary, a
  list of failures, specific values. Don't ask for raw logs or full file
  contents unless you actually need them.
- Each `cwork` call is synchronous: it blocks until the worker completes and
  returns the result. Use multiple parallel calls to avoid idle waiting.
