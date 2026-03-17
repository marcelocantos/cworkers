#!/bin/sh
# Integration tests for cwork.
# Run from repo root: sh test/test_cwork.sh

set -e

CWORK=./cwork
PASS=0
FAIL=0

if [ ! -x "$CWORK" ]; then
    echo "SKIP: $CWORK not found (build first)"
    exit 1
fi

check() {
    name="$1"
    expected="$2"
    actual="$3"
    if echo "$actual" | grep -qF -- "$expected"; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        echo "FAIL: $name"
        echo "  expected to contain: $expected"
        echo "  got: $actual"
    fi
}

# --- --version ---
out=$($CWORK --version 2>&1)
check "--version" "dev" "$out"

# --- --help ---
out=$($CWORK --help 2>&1)
check "--help" "Stdio MCP server" "$out"

# --- initialize ---
out=$(echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | $CWORK 2>/dev/null)
check "initialize: jsonrpc" '"jsonrpc":"2.0"' "$out"
check "initialize: id" '"id":1' "$out"
check "initialize: protocolVersion" '"protocolVersion":"2025-03-26"' "$out"
check "initialize: serverInfo name" '"name":"cworkers"' "$out"
check "initialize: capabilities" '"capabilities"' "$out"
check "initialize: instructions" '"instructions"' "$out"

# --- tools/list ---
out=$(echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | $CWORK 2>/dev/null)
check "tools/list: cwork tool" '"name":"cwork"' "$out"
check "tools/list: task param" '"task"' "$out"
check "tools/list: cwd param" '"cwd"' "$out"
check "tools/list: model param" '"model"' "$out"
check "tools/list: required" '"required":["task","cwd"]' "$out"

# --- tools/call: missing task ---
out=$(echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"cwork","arguments":{"cwd":"/tmp"}}}' | $CWORK 2>/dev/null)
check "missing task: isError" '"isError":true' "$out"
check "missing task: message" "missing required parameter: task" "$out"

# --- tools/call: missing cwd ---
out=$(echo '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"cwork","arguments":{"task":"hello"}}}' | $CWORK 2>/dev/null)
check "missing cwd: isError" '"isError":true' "$out"
check "missing cwd: message" "missing required parameter: cwd" "$out"

# --- tools/call: unknown tool ---
out=$(echo '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"bogus","arguments":{}}}' | $CWORK 2>/dev/null)
check "unknown tool: error code" "-32601" "$out"
check "unknown tool: message" "unknown tool" "$out"

# --- unknown method (request with id) ---
out=$(echo '{"jsonrpc":"2.0","id":6,"method":"bogus/method","params":{}}' | $CWORK 2>/dev/null)
check "unknown method: error" "-32601" "$out"

# --- notification (no id, no response expected) ---
out=$(echo '{"jsonrpc":"2.0","method":"notifications/initialized"}' | $CWORK 2>/dev/null)
check "notification: no response" "" "$out"

# --- multi-message sequence ---
out=$(printf '%s\n%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
    | $CWORK 2>/dev/null)
lines=$(echo "$out" | wc -l | tr -d ' ')
check "multi-message: two responses" "2" "$lines"

# --- tools/call: actual dispatch (requires claude on PATH) ---
if command -v claude >/dev/null 2>&1; then
    out=$(printf '%s\n%s\n%s\n' \
        '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
        '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
        '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"cwork","arguments":{"task":"respond with exactly the word pong","cwd":"/tmp","model":"haiku"}}}' \
        | timeout 60 $CWORK 2>/dev/null)
    check "dispatch: has result" '"id":10' "$out"
    check "dispatch: not error" '"content":[{"type":"text"' "$out"
    # Check log file was written.
    logfile="$HOME/.local/share/cworkers/events.jsonl"
    if [ -f "$logfile" ]; then
        check "dispatch: log exists" "start" "$(cat "$logfile")"
        check "dispatch: log done" "done" "$(cat "$logfile")"
    else
        FAIL=$((FAIL + 1))
        echo "FAIL: dispatch: log file not created at $logfile"
    fi
else
    echo "SKIP: dispatch tests (claude not on PATH)"
fi

# --- Summary ---
echo ""
echo "PASS: $PASS  FAIL: $FAIL"
[ "$FAIL" -eq 0 ]
