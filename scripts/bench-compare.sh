#!/usr/bin/env bash
# bench-compare.sh — run package benchmarks on the current branch, compare
# against bench/baseline.txt with benchstat, and print a delta table.
#
# Usage:
#   scripts/bench-compare.sh                 # compares HEAD vs bench/baseline.txt
#   scripts/bench-compare.sh path/to/old.txt # explicit baseline file
#
# CI use: capture bench/current.txt as a PR artifact, but do NOT block merges
# on this script — micro-benchmarks are noisy and need human review.
#
# Requires: benchstat (`go install golang.org/x/perf/cmd/benchstat@latest`).
set -euo pipefail

ROOT="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")"/.. && pwd)"
cd "$ROOT"

BASELINE="${1:-bench/baseline.txt}"
CURRENT="bench/current.txt"

if ! command -v benchstat >/dev/null 2>&1; then
    echo "ERROR: benchstat not on PATH." >&2
    echo "Install with: go install golang.org/x/perf/cmd/benchstat@latest" >&2
    exit 2
fi

if [[ ! -f "$BASELINE" ]]; then
    echo "ERROR: baseline file '$BASELINE' not found." >&2
    echo "Generate one with: make bench" >&2
    exit 2
fi

mkdir -p bench

echo "==> Running benchmarks (5 iterations) into $CURRENT ..."
go test -run='^$' -bench=. -benchmem -count=5 \
    ./pkg/api/... ./pkg/tool/... ./pkg/middleware/... \
    | tee "$CURRENT"

echo
echo "==> benchstat $BASELINE vs $CURRENT"
benchstat "$BASELINE" "$CURRENT"
