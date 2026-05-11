#!/usr/bin/env bash
# pprof-snapshot.sh — capture a full pprof profile bundle from a running saker.
#
# Usage:
#   ./scripts/pprof-snapshot.sh [--url URL] [--cpu SECONDS] [--out DIR] [--auth USER:PASS]
#
# Defaults:
#   URL=http://localhost:10112    (override with --url or PPROF_URL)
#   CPU=30                        (CPU profile sampling window in seconds)
#   OUT=./pprof-<timestamp>       (output directory; created if missing)
#
# The target server must be started with --debug to expose /debug/pprof.
# Captured profiles: cpu, heap, goroutine, allocs, block, mutex, threadcreate,
# plus the goroutine-debug=2 text dump (full stacks, useful for deadlocks).
#
# After capture, inspect with:
#   go tool pprof -http=localhost:8081 <profile>
#   go tool pprof -top <profile>
set -euo pipefail

URL="${PPROF_URL:-http://localhost:10112}"
CPU_SECONDS=30
OUT=""
AUTH=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --url)  URL="$2"; shift 2;;
    --cpu)  CPU_SECONDS="$2"; shift 2;;
    --out)  OUT="$2"; shift 2;;
    --auth) AUTH="$2"; shift 2;;
    -h|--help)
      sed -n '2,18p' "$0"
      exit 0;;
    *)
      echo "unknown flag: $1" >&2
      exit 2;;
  esac
done

if [[ -z "$OUT" ]]; then
  OUT="./pprof-$(date -u +%Y%m%dT%H%M%SZ)"
fi
mkdir -p "$OUT"

CURL_FLAGS=(--silent --show-error --fail --max-time $((CPU_SECONDS + 30)))
if [[ -n "$AUTH" ]]; then
  CURL_FLAGS+=(--user "$AUTH")
fi

# Reachability probe before launching long captures.
if ! curl "${CURL_FLAGS[@]}" -o /dev/null "$URL/debug/pprof/" 2>/dev/null; then
  echo "ERROR: $URL/debug/pprof/ is not reachable." >&2
  echo "       Start saker with --debug, or set PPROF_URL / --url." >&2
  exit 1
fi

echo "==> capturing pprof bundle into $OUT"
echo "    target: $URL"
echo "    cpu:    ${CPU_SECONDS}s"

# CPU profile is the slow one — kick it first and keep going while it samples.
echo "--> cpu (${CPU_SECONDS}s, blocking)"
curl "${CURL_FLAGS[@]}" -o "$OUT/cpu.pgz" "$URL/debug/pprof/profile?seconds=${CPU_SECONDS}"

for name in heap goroutine allocs block mutex threadcreate; do
  echo "--> $name"
  curl "${CURL_FLAGS[@]}" -o "$OUT/${name}.pgz" "$URL/debug/pprof/${name}"
done

echo "--> goroutine-debug=2 (full stack dump)"
curl "${CURL_FLAGS[@]}" -o "$OUT/goroutine-stacks.txt" \
  "$URL/debug/pprof/goroutine?debug=2"

echo
echo "Done. Profiles saved to: $OUT"
echo
echo "Quick inspection:"
echo "  go tool pprof -top $OUT/heap.pgz"
echo "  go tool pprof -top $OUT/cpu.pgz"
echo "  go tool pprof -http=localhost:8081 $OUT/heap.pgz"
echo "  less $OUT/goroutine-stacks.txt"
