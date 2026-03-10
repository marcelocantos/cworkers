# cworkers Setup

Install cworkers and configure Claude Code to use it permanently.

## 1. Install and start the broker

```bash
brew install marcelocantos/tap/cworkers
brew services start marcelocantos/tap/cworkers
```

## 2. Configure Claude Code

1. Write the operational guide to a file:
   ```bash
   cworkers --help-agent > ~/.claude/cworkers-guide.md
   ```

2. Add a reference to `~/.claude/CLAUDE.md`:
   ```markdown
   ## cworkers — Task Delegation

   **At session start**, read
   [`~/.claude/cworkers-guide.md`](~/.claude/cworkers-guide.md) and follow
   its guidelines throughout the session. It covers when to delegate, model
   selection, session setup, worker pool sizing, dispatching, and cleanup.

   This feature is experimental so keep an eye out for glitches.
   ```

## 3. Start using it

Read `~/.claude/cworkers-guide.md` and follow the session setup instructions
to begin delegating work.
