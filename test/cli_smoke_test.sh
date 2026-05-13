#!/usr/bin/env bash
# CLI smoke tests for saker — validates binary build, flags, and mode presets.
# Usage: ./test/cli_smoke_test.sh [path-to-binary]
# Exit code 0 = all passed, non-zero = at least one failed.

set -u

BINARY="${1:-./bin/saker}"
PASS=0
FAIL=0
FAILURES=""

pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); FAILURES="${FAILURES}\n  FAIL: $1"; echo "  FAIL: $1"; }

echo "=== Saker CLI Smoke Tests ==="
echo "Binary: $BINARY"
echo ""

# ---------------------------------------------------------------------------
# 1. Binary builds and runs
# ---------------------------------------------------------------------------
echo "--- Build & Help ---"

if [ ! -f "$BINARY" ]; then
    echo "Binary not found, building..."
    make saker 2>/dev/null || go build -o "$BINARY" ./cmd/saker/
fi

if "$BINARY" --help 2>&1 | grep -q "Usage:"; then
    pass "--help prints usage"
else
    fail "--help prints usage"
fi

# ---------------------------------------------------------------------------
# 2. --print mode (requires model key, skip if not set)
# ---------------------------------------------------------------------------
echo ""
echo "--- Print Mode ---"

if [ -n "${ANTHROPIC_API_KEY:-}" ] || [ -n "${OPENAI_API_KEY:-}" ]; then
    OUTPUT=$("$BINARY" --print --model "${SAKER_MODEL:-claude-haiku-4-5-20251001}" "Say exactly: SMOKE_OK" 2>/dev/null || true)
    if echo "$OUTPUT" | grep -qi "SMOKE_OK"; then
        pass "--print basic prompt"
    else
        fail "--print basic prompt (output: ${OUTPUT:0:100})"
    fi

    # JSON output format
    JSON_OUT=$("$BINARY" --print --output-format json --model "${SAKER_MODEL:-claude-haiku-4-5-20251001}" "Say hello" 2>/dev/null || true)
    if echo "$JSON_OUT" | python3 -c "import sys,json; json.load(sys.stdin)" 2>/dev/null; then
        pass "--print --output-format json is valid JSON"
    else
        fail "--print --output-format json is valid JSON"
    fi
else
    echo "  SKIP: No API key set (ANTHROPIC_API_KEY or OPENAI_API_KEY)"
fi

# ---------------------------------------------------------------------------
# 3. Entry point flag validation
# ---------------------------------------------------------------------------
echo ""
echo "--- Entry Point Flags ---"

# --entry accepts valid values
for entry in cli ci platform; do
    if "$BINARY" --entry "$entry" --help 2>&1 | grep -q "Usage:"; then
        pass "--entry $entry accepted"
    else
        fail "--entry $entry accepted"
    fi
done

# ---------------------------------------------------------------------------
# 4. Tool listing via EnabledBuiltinToolKeys (compile-time test)
# ---------------------------------------------------------------------------
echo ""
echo "--- Tool Preset Validation (go test) ---"

if go test ./pkg/api/... -run "TestPresetTools|TestBuiltinOrder" -count=1 2>&1 | grep -q "^ok"; then
    pass "preset tool tests pass"
else
    fail "preset tool tests"
fi

if go test ./test/runtime/toolgroups/... -count=1 2>&1 | grep -q "^ok"; then
    pass "toolgroups integration tests pass"
else
    fail "toolgroups integration tests"
fi

# ---------------------------------------------------------------------------
# 5. Server mode starts and responds (quick check, kill after 2s)
# ---------------------------------------------------------------------------
echo ""
echo "--- Server Mode ---"

PORT=19876
"$BINARY" --server --server-addr ":$PORT" --dangerously-skip-permissions 2>/dev/null &
SERVER_PID=$!
sleep 3

if curl -sf "http://localhost:$PORT/health" >/dev/null 2>&1 || curl -sf "http://localhost:$PORT/" >/dev/null 2>&1; then
    pass "server starts and responds on :$PORT"
else
    fail "server starts and responds on :$PORT"
fi

kill "$SERVER_PID" 2>/dev/null || true
wait "$SERVER_PID" 2>/dev/null || true

# ---------------------------------------------------------------------------
# 6. --api-only flag starts without error
# ---------------------------------------------------------------------------
echo ""
echo "--- API-Only Mode ---"

PORT=19877
"$BINARY" --server --server-addr ":$PORT" --api-only --dangerously-skip-permissions 2>/dev/null &
SERVER_PID=$!
sleep 3

if kill -0 "$SERVER_PID" 2>/dev/null; then
    pass "--api-only server starts without crash"
else
    fail "--api-only server starts without crash"
fi

kill "$SERVER_PID" 2>/dev/null || true
wait "$SERVER_PID" 2>/dev/null || true

# ---------------------------------------------------------------------------
# 7. Pipeline flag accepted
# ---------------------------------------------------------------------------
echo ""
echo "--- Pipeline Flag ---"

PIPELINE_FILE=$(mktemp /tmp/saker-pipeline-XXXXXX.json)
echo '{"steps":[{"id":"s1","tool":"bash","params":{"command":"echo ok"}}]}' > "$PIPELINE_FILE"

if "$BINARY" --pipeline "$PIPELINE_FILE" --dangerously-skip-permissions 2>&1 | head -5 | grep -q "Model:\|pipeline\|Session:\|bye"; then
    pass "--pipeline flag accepted without crash"
else
    fail "--pipeline flag accepted without crash"
fi
rm -f "$PIPELINE_FILE"

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo "=== Results ==="
echo "  Passed: $PASS"
echo "  Failed: $FAIL"
if [ "$FAIL" -gt 0 ]; then
    echo -e "\nFailures:$FAILURES"
    exit 1
fi
echo "  All smoke tests passed!"
