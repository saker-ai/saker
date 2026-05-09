#!/usr/bin/env bash
# run-eval-tb2.sh — One-shot Terminal-Bench 2 runner.
#
# What it does:
#   1. Verifies docker daemon and a model API key are available
#   2. Builds bin/saker when missing or stale
#   3. Optionally clones the public TB2 dataset to ./.cache/tb2
#   4. Invokes `saker eval terminalbench` with sensible defaults
#
# Usage:
#   scripts/run-eval-tb2.sh                       # full run, default dataset, concurrency=4
#   scripts/run-eval-tb2.sh --smoke               # only one task (hello-world*) for plumbing check
#   scripts/run-eval-tb2.sh --dataset /path/tb2   # use an existing dataset checkout
#   scripts/run-eval-tb2.sh --concurrency 8       # raise parallelism
#   scripts/run-eval-tb2.sh --filter cli-*        # restrict task names (repeatable)
#   scripts/run-eval-tb2.sh --repeat-threshold 8  # tolerate 8 identical tool calls before abort
#   scripts/run-eval-tb2.sh --no-transcripts      # disable per-task conversation dumps
#   scripts/run-eval-tb2.sh --with-mirror         # opt in to agent-visible China mirror env (off by default)
#   scripts/run-eval-tb2.sh --mirror PIP_INDEX_URL=... # add a custom agent-visible mirror env (repeatable)
#   scripts/run-eval-tb2.sh --no-verifier-mirror  # disable verifier-only mirror env (on by default; isolated from agent shell)
#   scripts/run-eval-tb2.sh --proxy http://127.0.0.1:7890  # route container HTTP(S) via host Clash/Mihomo
#   scripts/run-eval-tb2.sh --max-tokens 2000000  # per-task token cap (input+output cumulative)
#   scripts/run-eval-tb2.sh --max-budget-usd 5    # per-task USD cap (requires provider with known pricing)
#   scripts/run-eval-tb2.sh --                    # everything after -- is forwarded to saker
#
# Environment:
#   ANTHROPIC_API_KEY  required unless --provider/--model overrides target a different vendor
#   SAKER_BIN         use a pre-built binary instead of `go build`
#   TB2_DATASET_REPO   override the upstream dataset URL (default: NousResearch/Terminal-Bench-2)
#   TB2_OUTPUT_DIR     override report destination (default: ./.artifacts/eval/tb2-<ts>)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

# --- defaults --------------------------------------------------------------
DATASET_DIR=""
SMOKE=0
CONCURRENCY=4
TASK_TIMEOUT="30m"
TERMINAL_TIMEOUT="15m"     # 15m leaves headroom for apt-get install r-base etc.
MAX_ITERS=50
PULL_POLICY="if-missing"
SKIP_INCOMPAT=1
VERBOSE=1
REPEAT_THRESHOLD=0          # 0 = use saker default (5); -1 disables loop detection
NO_TRANSCRIPTS=0
WITH_MIRROR=0               # opt in to terminalbench.DefaultMirrorEnv (China firewall workaround)
NO_VERIFIER_MIRROR=0        # disable per-call verifier mirror env (on by default; isolated from agent)
MIRRORS=()                  # repeated --mirror KEY=VAL pairs
PROXY_URL=""                # http(s) proxy injected into containers (e.g. http://127.0.0.1:7890)
# Safety nets for per-task spend. Picked well above typical TB2 task usage so a
# normal run never trips them, but a runaway loop (or a regression in repeat-
# detection) can't quietly drain a budget overnight.
#   MAX_TOKENS=2000000  → ~50 iterations of 40K-context tool turns
#   MAX_BUDGET_USD=10   → hard cap; requires --provider/--model with known pricing
# Set to 0 to disable.
MAX_TOKENS=2000000
MAX_BUDGET_USD=10
EXTRA_ARGS=()
FILTERS=()
EXCLUDES=()

DATASET_REPO="${TB2_DATASET_REPO:-https://github.com/harbor-framework/Terminal-Bench-2.git}"
DATASET_CACHE="$REPO_ROOT/.cache/tb2"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
OUTPUT_DIR="${TB2_OUTPUT_DIR:-$REPO_ROOT/.artifacts/eval/tb2-$TIMESTAMP}"

# --- arg parsing -----------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dataset)            DATASET_DIR="$2"; shift 2 ;;
        --output)             OUTPUT_DIR="$2"; shift 2 ;;
        --concurrency)        CONCURRENCY="$2"; shift 2 ;;
        --task-timeout)       TASK_TIMEOUT="$2"; shift 2 ;;
        --terminal-timeout)   TERMINAL_TIMEOUT="$2"; shift 2 ;;
        --max-iters)          MAX_ITERS="$2"; shift 2 ;;
        --pull)               PULL_POLICY="$2"; shift 2 ;;
        --filter)             FILTERS+=("$2"); shift 2 ;;
        --exclude)            EXCLUDES+=("$2"); shift 2 ;;
        --no-skip-incompat)   SKIP_INCOMPAT=0; shift ;;
        --quiet)              VERBOSE=0; shift ;;
        --smoke)              SMOKE=1; shift ;;
        --repeat-threshold)   REPEAT_THRESHOLD="$2"; shift 2 ;;
        --no-transcripts)     NO_TRANSCRIPTS=1; shift ;;
        --with-mirror)        WITH_MIRROR=1; shift ;;
        --no-verifier-mirror) NO_VERIFIER_MIRROR=1; shift ;;
        --mirror)             MIRRORS+=("$2"); shift 2 ;;
        --proxy)              PROXY_URL="$2"; shift 2 ;;
        --max-tokens)         MAX_TOKENS="$2"; shift 2 ;;
        --max-budget-usd)     MAX_BUDGET_USD="$2"; shift 2 ;;
        --)                   shift; EXTRA_ARGS=("$@"); break ;;
        -h|--help)
            sed -n '2,30p' "$0"
            exit 0
            ;;
        *)
            echo "unknown arg: $1" >&2
            echo "run with --help to see usage." >&2
            exit 2
            ;;
    esac
done

# --- preflight -------------------------------------------------------------
if ! command -v docker >/dev/null 2>&1; then
    echo "error: docker CLI not on PATH" >&2
    exit 1
fi
if ! docker info >/dev/null 2>&1; then
    echo "error: docker daemon is not reachable (try 'docker info')" >&2
    exit 1
fi
if [[ -z "${ANTHROPIC_API_KEY:-}" && -z "${ANTHROPIC_AUTH_TOKEN:-}" ]]; then
    echo "warning: neither ANTHROPIC_API_KEY nor ANTHROPIC_AUTH_TOKEN is set." >&2
    echo "         the runner will fail unless --provider/--model targets a vendor that doesn't need it." >&2
fi

# --- binary ----------------------------------------------------------------
SAKER_BIN="${SAKER_BIN:-$REPO_ROOT/bin/saker}"

# Decide whether bin/saker needs rebuilding. We rebuild when:
#   - the binary is missing, OR
#   - any tracked .go file or go.mod is newer than the binary.
# Honors SAKER_SKIP_REBUILD=1 for explicit opt-out (e.g. release smoke).
needs_rebuild() {
    [[ ! -x "$SAKER_BIN" ]] && return 0
    [[ -n "${SAKER_SKIP_REBUILD:-}" ]] && return 1
    local newer
    newer="$(find "$REPO_ROOT/cmd" "$REPO_ROOT/pkg" "$REPO_ROOT/go.mod" "$REPO_ROOT/go.sum" \
                  -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) \
                  -newer "$SAKER_BIN" -print 2>/dev/null | head -n 1 || true)"
    [[ -n "$newer" ]]
}

if needs_rebuild; then
    if [[ -x "$SAKER_BIN" ]]; then
        echo "==> rebuilding saker (sources newer than $SAKER_BIN; set SAKER_SKIP_REBUILD=1 to skip)"
    else
        echo "==> building saker (no binary at $SAKER_BIN)"
    fi
    mkdir -p "$REPO_ROOT/bin" "$REPO_ROOT/cmd/cli/frontend/dist"
    touch "$REPO_ROOT/cmd/cli/frontend/dist/.gitkeep"
    (cd "$REPO_ROOT" && go build -trimpath -o "$SAKER_BIN" ./cmd/cli)
fi

# --- dataset ---------------------------------------------------------------
# Two on-disk layouts are accepted:
#   • upstream  → $root/<task>/task.toml      (NousResearch / harbor-framework)
#   • converted → $root/tasks/<task>/task.json (legacy fetch-tb2.sh)
dataset_has_tasks() {
    local root="$1"
    [[ -d "$root/tasks" ]] && return 0
    # any first-level dir with a task.toml counts as a valid upstream dataset
    if compgen -G "$root"/*/task.toml >/dev/null 2>&1; then
        return 0
    fi
    return 1
}

if [[ -z "$DATASET_DIR" ]]; then
    if ! dataset_has_tasks "$DATASET_CACHE"; then
        if [[ -e "$DATASET_CACHE" ]]; then
            echo "error: $DATASET_CACHE exists but contains no tasks; remove it or pass --dataset" >&2
            exit 1
        fi
        echo "==> cloning TB2 dataset to $DATASET_CACHE"
        mkdir -p "$(dirname "$DATASET_CACHE")"
        git clone --depth 1 "$DATASET_REPO" "$DATASET_CACHE"
    else
        echo "==> using cached TB2 dataset at $DATASET_CACHE"
    fi
    DATASET_DIR="$DATASET_CACHE"
fi
if ! dataset_has_tasks "$DATASET_DIR"; then
    echo "error: $DATASET_DIR is not a TB2 dataset (no tasks/ subdir and no <name>/task.toml)" >&2
    exit 1
fi

# --- smoke mode tweaks -----------------------------------------------------
if [[ "$SMOKE" -eq 1 && "${#FILTERS[@]}" -eq 0 ]]; then
    # Default smoke target: pick the first task alphabetically so something always matches.
    if [[ -d "$DATASET_DIR/tasks" ]]; then
        FIRST_TASK="$(ls -1 "$DATASET_DIR/tasks" 2>/dev/null | sort | head -n 1 || true)"
    else
        # upstream layout: look for <root>/<name>/task.toml
        FIRST_TASK="$(for d in "$DATASET_DIR"/*/task.toml; do [[ -f "$d" ]] && basename "$(dirname "$d")"; done | sort | head -n 1)"
    fi
    if [[ -z "$FIRST_TASK" ]]; then
        echo "error: dataset has no tasks" >&2
        exit 1
    fi
    FILTERS=("$FIRST_TASK")
    CONCURRENCY=1
    echo "==> smoke mode: filtering to single task '$FIRST_TASK'"
fi

mkdir -p "$OUTPUT_DIR"

# --- assemble args ---------------------------------------------------------
ARGS=(
    eval terminalbench
    --dataset "$DATASET_DIR"
    --output "$OUTPUT_DIR"
    --concurrency "$CONCURRENCY"
    --max-iters "$MAX_ITERS"
    --task-timeout "$TASK_TIMEOUT"
    --terminal-timeout "$TERMINAL_TIMEOUT"
    --pull "$PULL_POLICY"
)
[[ "$SKIP_INCOMPAT" -eq 1 ]] && ARGS+=(--skip-incompatible)
[[ "$VERBOSE" -eq 1 ]] && ARGS+=(--verbose)
[[ "$NO_TRANSCRIPTS" -eq 1 ]] && ARGS+=(--no-transcripts)
[[ "$WITH_MIRROR" -eq 1 ]] && ARGS+=(--with-mirror)
[[ "$NO_VERIFIER_MIRROR" -eq 1 ]] && ARGS+=(--no-verifier-mirror)
[[ -n "$PROXY_URL" ]] && ARGS+=(--proxy "$PROXY_URL")
[[ "$REPEAT_THRESHOLD" -ne 0 ]] && ARGS+=(--repeat-threshold "$REPEAT_THRESHOLD")
# Pass non-zero token/budget caps. 0 means "disabled" both here and in the CLI.
[[ "$MAX_TOKENS" -gt 0 ]] && ARGS+=(--max-tokens "$MAX_TOKENS")
# Float comparison via awk: avoid bash's integer-only -gt.
if awk -v v="$MAX_BUDGET_USD" 'BEGIN { exit !(v+0 > 0) }'; then
    ARGS+=(--max-budget-usd "$MAX_BUDGET_USD")
fi
for f in "${FILTERS[@]}";  do ARGS+=(--filter  "$f"); done
for e in "${EXCLUDES[@]}"; do ARGS+=(--exclude "$e"); done
for m in "${MIRRORS[@]}";  do ARGS+=(--mirror  "$m"); done
ARGS+=("${EXTRA_ARGS[@]}")

# --- run -------------------------------------------------------------------
echo "==> $SAKER_BIN ${ARGS[*]}"
exec "$SAKER_BIN" "${ARGS[@]}"
