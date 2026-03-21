# cworkers Setup

Install cwork and configure Claude Code to use it.

## 1. Install

```bash
# From source:
cc -std=c11 -Wall -Wextra -Os -Isrc -o cwork \
  src/cwork.c src/work.c src/json.c src/log.c src/worker.c \
  src/help_agent.s -lpthread
cp cwork ~/.local/bin/
```

## 2. Configure MCP

Add to your `~/.claude.json` (or project `.mcp.json`):

```json
{
  "mcpServers": {
    "cworkers": {
      "type": "stdio",
      "command": "cwork"
    }
  }
}
```

## 3. Add CLAUDE.md directive

Add **near the top** of your project's `CLAUDE.md` (or `~/.claude/CLAUDE.md`
for global):

```markdown
## cworkers

**MANDATORY**: Never run builds (make, go build, npm run, etc.), tests,
file reads, or searches directly in the root session. Always delegate
via the `cwork` MCP tool. The only exceptions are trivial git commands
(git status, git diff) whose output you already know will be small.
When in doubt, delegate.
```

## 4. Start using it

The `cwork` MCP tool is now available. Call it with a `task` and your
`cwd` (working directory). Workers start fresh — include all necessary
context in the task description. See `cwork --help-agent` for the full
operational guide.
