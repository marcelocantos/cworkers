# TODO

- Task acknowledgment and retry: once a task is written to a worker connection, it's considered delivered. If the worker crashes mid-execution the task is lost. Consider a dead-letter mechanism or at-least-once delivery.
- Automatic transcript discovery: the root agent must manually pass `--transcript <path>`. Explore auto-detecting the active session's JSONL file (e.g. from `~/.claude/projects/` by recency).
- Proactive stale worker detection: currently dead workers are only discovered when the broker tries to write a task to them. The 60s reconnect interval bounds staleness but a heartbeat or keepalive could detect it sooner.
- Consider MCP server for dispatch if richer interaction is needed: streaming progress back to the caller, structured results, or tool-calling chains from workers. Current fire-and-forget protocol is simpler than MCP warrants, but these use cases would benefit from it.
