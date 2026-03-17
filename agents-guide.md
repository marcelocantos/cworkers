# cworkers Setup

Install cworkers and configure Claude Code to use it as an MCP server.

## 1. Install and start the broker

```bash
brew install marcelocantos/tap/cworkers
brew services start marcelocantos/tap/cworkers
```

The broker runs as an MCP server on port 4242 by default.

## 2. Configure MCP

Add to your project's `.mcp.json` (or `~/.claude.json` for global):

```json
{
  "mcpServers": {
    "cworkers": {
      "type": "streamable-http",
      "url": "http://localhost:4242/mcp"
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

## 4. Configure claude path (optional)

If the `claude` binary isn't on `PATH` (e.g., when running as a brew service),
create `~/.config/cworkers/config.json`:

```json
{
  "claude_path": "/Users/you/.local/bin/claude"
}
```

## 5. Start using it

The `cwork` MCP tool is now available. Call it with a `task` and your
`cwd` (working directory). The broker auto-discovers your transcript
and injects conversation context into workers. See `cworkers --help-agent`
for the full operational guide.
