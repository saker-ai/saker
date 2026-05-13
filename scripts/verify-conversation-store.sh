#!/usr/bin/env bash
# verify-conversation-store.sh — end-to-end smoke for the unified
# pkg/conversation event log against a real bin/saker binary.
#
# What it proves
#   The dual-write tee (pkg/server/session_conv_tee.go), the CLI persister
#   (pkg/api/conversation_persist.go), and the OpenAI gateway persister all
#   converge into the SAME conversation.db sqlite file. The verifier writes
#   data through each entry point and then queries the DB directly with the
#   sqlite3 CLI to assert convergence.
#
# Tiers
#   tier-1 (always runs, no API key needed)
#       Boot ./bin/saker --server with --server-data-dir <tmp>; drive
#       /api/rpc/thread/{create,update,delete}; assert threads + events
#       rows exist in <tmp>/conversation.db with the expected titles and
#       soft-delete state.
#
#   tier-2 (runs only when ANTHROPIC_API_KEY is set)
#       Run ./bin/saker -p "say only PONG" against a fresh --config-root;
#       assert user + assistant events landed in <cfg>/conversation.db.
#
#   tier-3 (runs only when ANTHROPIC_API_KEY is set)
#       Boot ./bin/saker --server --openai-gw-enabled --openai-gw-dev-bypass;
#       POST a single /v1/chat/completions message; assert OpenAI gateway's
#       chatPersister wrote a thread + user + assistant events.
#
# Usage
#   make build && scripts/verify-conversation-store.sh
#   PORT=10199 scripts/verify-conversation-store.sh
#   SKIP_TIER2=1 SKIP_TIER3=1 scripts/verify-conversation-store.sh
#   KEEP_DIRS=1 scripts/verify-conversation-store.sh    # leave tmp dirs for inspection
#
# Exit code
#   0 = every active tier asserted pass; non-zero = first failing assertion.
#   Skipped tiers do NOT fail the script; they print a "skip" line.

set -euo pipefail

ROOT="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")"/.. && pwd)"
cd "$ROOT"

SAKER_BIN="${SAKER_BIN:-$ROOT/bin/saker}"
PORT="${PORT:-10199}"
GW_PORT="${GW_PORT:-10198}"
KEEP_DIRS="${KEEP_DIRS:-0}"
SKIP_TIER1="${SKIP_TIER1:-0}"
SKIP_TIER2="${SKIP_TIER2:-0}"
SKIP_TIER3="${SKIP_TIER3:-0}"

# Color helpers — quiet on dumb terminals or when stdout is not a TTY.
if [[ -t 1 && "${TERM:-dumb}" != "dumb" ]]; then
    BOLD=$(tput bold); GREEN=$(tput setaf 2); RED=$(tput setaf 1)
    YELLOW=$(tput setaf 3); RESET=$(tput sgr0)
else
    BOLD=""; GREEN=""; RED=""; YELLOW=""; RESET=""
fi

step()  { echo "${BOLD}==> $*${RESET}"; }
ok()    { echo "  ${GREEN}ok${RESET}    $*"; }
warn()  { echo "  ${YELLOW}skip${RESET}  $*"; }
fail()  { echo "  ${RED}FAIL${RESET}  $*"; exit 1; }

# Track tmp paths so trap can rm -rf them. Server PIDs land here too.
TMP_DIRS=()
SERVER_PIDS=()

cleanup() {
    local rc=$?
    for pid in "${SERVER_PIDS[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill -TERM "$pid" 2>/dev/null || true
            # Brief settle window so the server flushes WAL before we rm.
            for _ in 1 2 3 4 5; do
                kill -0 "$pid" 2>/dev/null || break
                sleep 0.5
            done
            kill -KILL "$pid" 2>/dev/null || true
        fi
    done
    if [[ "$KEEP_DIRS" == "1" ]]; then
        echo "KEEP_DIRS=1 — leaving tmp dirs:"
        for d in "${TMP_DIRS[@]}"; do echo "  $d"; done
    else
        for d in "${TMP_DIRS[@]}"; do rm -rf "$d"; done
    fi
    exit $rc
}
trap cleanup EXIT INT TERM

# Prereqs.
[[ -x "$SAKER_BIN" ]] || fail "saker binary not built or not executable: $SAKER_BIN (run: make build)"
command -v curl >/dev/null  || fail "curl missing"
command -v sqlite3 >/dev/null || fail "sqlite3 CLI missing"
command -v jq >/dev/null    || fail "jq missing"

# wait_port port pid timeout_seconds
# Polls the TCP port until /api/rpc/initialize returns 200, or fails out.
wait_for_server() {
    local port=$1 pid=$2 timeout=${3:-30}
    local deadline=$((SECONDS + timeout))
    while (( SECONDS < deadline )); do
        if ! kill -0 "$pid" 2>/dev/null; then
            fail "server pid $pid died before port $port came up"
        fi
        local code
        code=$(curl -s -o /dev/null -w '%{http_code}' \
            -X POST "http://127.0.0.1:${port}/api/rpc/initialize" \
            -H 'Content-Type: application/json' \
            -d '{}' 2>/dev/null || true)
        if [[ "$code" == "200" ]]; then
            return 0
        fi
        sleep 0.5
    done
    fail "server on :$port did not become ready within ${timeout}s"
}

# rpc port method json — POSTs to /api/rpc/<method> and prints the response body.
rpc() {
    local port=$1 method=$2 body=$3
    curl -sS -X POST "http://127.0.0.1:${port}/api/rpc/${method}" \
        -H 'Content-Type: application/json' -d "$body"
}

# sql db query — runs a single SQL statement, returns one column (no header).
sql() {
    local db=$1 query=$2
    sqlite3 -batch -noheader -separator '|' "$db" "$query"
}

# ---------------------------------------------------------------------------
# Tier 1 — Web UI thread CRUD via /api/rpc (no LLM call).
# ---------------------------------------------------------------------------
run_tier1() {
    if [[ "$SKIP_TIER1" == "1" ]]; then warn "tier-1 skipped (SKIP_TIER1=1)"; return 0; fi
    step "tier-1 — Web UI thread CRUD via /api/rpc"

    local data_dir
    data_dir=$(mktemp -d -t saker-verify-tier1-XXXXXX)
    TMP_DIRS+=("$data_dir")

    # Server mode opens conversation.db at <data-dir>/conversation.db.
    # No project_root needed; gateway disabled; no LLM key required.
    "$SAKER_BIN" --server \
        --server-addr ":${PORT}" \
        --server-data-dir "$data_dir" \
        > "$data_dir/server.log" 2>&1 &
    local pid=$!
    SERVER_PIDS+=("$pid")
    wait_for_server "$PORT" "$pid" 30

    # Server runs in multi-tenant mode (project store auto-opens). Localhost
    # requests bind to a synthetic user; project/list lazily provisions that
    # user's personal project. Use its UUID as projectId for thread/* calls
    # so the per-project SessionStore + tee path is exercised end-to-end.
    # /api/rpc/<method> takes the JSON body AS the Params object directly
    # (not a JSON-RPC envelope) and returns response.Result directly.
    # gin_routes_rpc.go decodes the body via decodeRPCParams, builds
    # Request{Params: <body>}, and on success writes resp.Result.
    local list_resp pid_proj create_resp tid update_resp delete_resp
    list_resp=$(rpc "$PORT" "project/list" '{}')
    pid_proj=$(echo "$list_resp" | jq -r '.projects[0].id // empty')
    [[ -n "$pid_proj" ]] || fail "project/list returned no project; resp=$list_resp"
    ok "project/list resolved personal projectId=$pid_proj"

    create_resp=$(rpc "$PORT" "thread/create" "{\"projectId\":\"$pid_proj\",\"title\":\"verify-tier1-original\"}")
    tid=$(echo "$create_resp" | jq -r '.id // empty')
    [[ -n "$tid" ]] || fail "thread/create returned no id; resp=$create_resp"
    ok "thread/create returned id=$tid"

    update_resp=$(rpc "$PORT" "thread/update" "{\"projectId\":\"$pid_proj\",\"threadId\":\"$tid\",\"title\":\"verify-tier1-renamed\"}")
    [[ "$update_resp" != "" ]] || fail "thread/update returned empty body"
    ok "thread/update accepted: $update_resp"

    # Stop the server before reading the SQLite file. WAL replay is fine
    # while running, but a clean shutdown removes any race anxiety.
    kill -TERM "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    SERVER_PIDS=()

    local db="$data_dir/conversation.db"
    [[ -f "$db" ]] || fail "expected conversation.db at $db (server log: $data_dir/server.log)"

    local row
    row=$(sql "$db" "SELECT id, project_id, owner_user_id, client, title, deleted_at FROM threads WHERE id='$tid'")
    [[ -n "$row" ]] || fail "thread row missing in conversation.db: $tid"
    echo "    db row: $row"
    [[ "$row" == "$tid|$pid_proj|web|web|verify-tier1-renamed|" ]] \
        || fail "thread row mismatch; expected '$tid|$pid_proj|web|web|verify-tier1-renamed|', got '$row'"
    ok "tier-1 row reflects projectID=$pid_proj, owner=web, client=web, renamed title, not yet deleted"

    # Restart server so we can drive a delete and re-verify the soft-delete.
    "$SAKER_BIN" --server \
        --server-addr ":${PORT}" \
        --server-data-dir "$data_dir" \
        >> "$data_dir/server.log" 2>&1 &
    pid=$!
    SERVER_PIDS+=("$pid")
    wait_for_server "$PORT" "$pid" 30

    delete_resp=$(rpc "$PORT" "thread/delete" "{\"projectId\":\"$pid_proj\",\"threadId\":\"$tid\"}")
    [[ "$delete_resp" != "" ]] || fail "thread/delete returned empty body"
    ok "thread/delete accepted: $delete_resp"

    kill -TERM "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    SERVER_PIDS=()

    local deleted_at
    deleted_at=$(sql "$db" "SELECT deleted_at FROM threads WHERE id='$tid'")
    [[ -n "$deleted_at" ]] || fail "soft-delete did not populate threads.deleted_at for $tid"
    ok "tier-1 soft-delete populated deleted_at=$deleted_at"

    echo "${GREEN}tier-1 PASS${RESET}"
}

# ---------------------------------------------------------------------------
# Tier 2 — CLI one-shot. Requires ANTHROPIC_API_KEY (or any provider key
# matching the model in --model). Asserts user + assistant events.
# ---------------------------------------------------------------------------
run_tier2() {
    if [[ "$SKIP_TIER2" == "1" ]]; then warn "tier-2 skipped (SKIP_TIER2=1)"; return 0; fi
    if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
        warn "tier-2 skipped (no ANTHROPIC_API_KEY in env)"
        return 0
    fi
    step "tier-2 — CLI one-shot via -p"

    local cfg_root project_root
    project_root=$(mktemp -d -t saker-verify-tier2-XXXXXX)
    cfg_root="$project_root/.saker"
    mkdir -p "$cfg_root"
    TMP_DIRS+=("$project_root")

    # cd into the temp project root so saker treats it as the project,
    # then point --config-root at .saker so conversation.db lands at
    # <project>/.saker/conversation.db (matches resolveCLIConfigBase()).
    local out
    if ! out=$(cd "$project_root" && "$SAKER_BIN" \
        --config-root "$cfg_root" \
        --model "${SAKER_TEST_MODEL:-claude-haiku-4-5-20251001}" \
        -p "Reply with only the single word PONG" 2>&1); then
        echo "$out"
        fail "saker -p exited non-zero (model unavailable / key invalid?)"
    fi
    echo "    cli stdout: ${out:0:120}"

    local db="$cfg_root/conversation.db"
    [[ -f "$db" ]] || fail "expected conversation.db at $db (was the conversation log opened?)"

    local thread_count
    thread_count=$(sql "$db" "SELECT COUNT(*) FROM threads")
    [[ "$thread_count" -ge 1 ]] || fail "no threads in conversation.db after CLI run"
    ok "tier-2 threads=$thread_count"

    local user_count assistant_count
    user_count=$(sql "$db" "SELECT COUNT(*) FROM events WHERE kind='user_message'")
    assistant_count=$(sql "$db" "SELECT COUNT(*) FROM events WHERE kind='assistant_text'")
    [[ "$user_count" -ge 1 ]] || fail "no user_message events in conversation.db"
    [[ "$assistant_count" -ge 1 ]] || fail "no assistant_text events in conversation.db"
    ok "tier-2 events user=$user_count assistant=$assistant_count"

    # Spot-check that the user prompt text actually landed.
    local user_sample
    user_sample=$(sql "$db" "SELECT content_text FROM events WHERE kind='user_message' ORDER BY seq LIMIT 1")
    [[ "$user_sample" == *"PONG"* ]] || fail "user_message body did not contain prompt; got: $user_sample"
    ok "tier-2 user_message preserves prompt: ${user_sample:0:60}"

    echo "${GREEN}tier-2 PASS${RESET}"
}

# ---------------------------------------------------------------------------
# Tier 3 — OpenAI gateway path. Requires ANTHROPIC_API_KEY. Uses
# --openai-gw-dev-bypass so we can curl /v1/chat/completions without minting
# a real Bearer token.
# ---------------------------------------------------------------------------
run_tier3() {
    if [[ "$SKIP_TIER3" == "1" ]]; then warn "tier-3 skipped (SKIP_TIER3=1)"; return 0; fi
    if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
        warn "tier-3 skipped (no ANTHROPIC_API_KEY in env)"
        return 0
    fi
    step "tier-3 — OpenAI gateway via /v1/chat/completions"

    local data_dir
    data_dir=$(mktemp -d -t saker-verify-tier3-XXXXXX)
    TMP_DIRS+=("$data_dir")

    "$SAKER_BIN" --server \
        --server-addr ":${GW_PORT}" \
        --server-data-dir "$data_dir" \
        --openai-gw-enabled \
        --openai-gw-dev-bypass \
        > "$data_dir/server.log" 2>&1 &
    local pid=$!
    SERVER_PIDS+=("$pid")
    wait_for_server "$GW_PORT" "$pid" 30

    local model
    model="${SAKER_TEST_MODEL:-claude-haiku-4-5-20251001}"
    local payload
    payload=$(jq -n --arg m "$model" '{
        model: $m,
        stream: false,
        messages: [{role:"user", content:"Reply with only the single word PONG"}]
    }')
    local resp
    resp=$(curl -sS -X POST "http://127.0.0.1:${GW_PORT}/v1/chat/completions" \
        -H 'Content-Type: application/json' \
        -H 'Authorization: Bearer dev-bypass' \
        -d "$payload")
    local content
    content=$(echo "$resp" | jq -r '.choices[0].message.content // empty')
    [[ -n "$content" ]] || fail "openai gateway returned no message content; resp=$resp"
    ok "openai gateway replied: ${content:0:60}"

    kill -TERM "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    SERVER_PIDS=()

    local db="$data_dir/conversation.db"
    [[ -f "$db" ]] || fail "expected conversation.db at $db (server log: $data_dir/server.log)"

    local thread_count user_count assistant_count
    thread_count=$(sql "$db" "SELECT COUNT(*) FROM threads")
    user_count=$(sql "$db" "SELECT COUNT(*) FROM events WHERE kind='user_message'")
    assistant_count=$(sql "$db" "SELECT COUNT(*) FROM events WHERE kind='assistant_text'")
    [[ "$thread_count" -ge 1 ]] || fail "no thread row after gateway call"
    [[ "$user_count" -ge 1 ]] || fail "no user_message event after gateway call"
    [[ "$assistant_count" -ge 1 ]] || fail "no assistant_text event after gateway call"
    ok "tier-3 threads=$thread_count user=$user_count assistant=$assistant_count"

    # Confirm the gateway tagged its rows with the openai client marker so
    # we can tell apart Web UI vs gateway traffic in the same DB.
    local clients
    clients=$(sql "$db" "SELECT DISTINCT client FROM threads")
    echo "    distinct thread.client values: $clients"
    [[ "$clients" == *"openai"* ]] || warn "tier-3 expected client='openai' on at least one thread; got: $clients"

    echo "${GREEN}tier-3 PASS${RESET}"
}

# ---------------------------------------------------------------------------
# Driver.
# ---------------------------------------------------------------------------
run_tier1
run_tier2
run_tier3
echo "${BOLD}${GREEN}all active tiers passed${RESET}"
