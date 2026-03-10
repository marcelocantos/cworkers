# cworkers Setup

Install cworkers and configure Claude Code to use it permanently.

## 1. Install and start the broker

```bash
brew install marcelocantos/tap/cworkers
brew services start marcelocantos/tap/cworkers
```

## 2. Configure Claude Code

Append the output of `cworkers --help-agent` to `~/.claude/CLAUDE.md` so
that every future session has the operational instructions available
automatically.

## 3. Start using it

Follow the operational guide you just added to set up task delegation for
this session.
