#!/usr/bin/env bash
# coverage-summary.sh — generate per-package coverage table from coverage.out
#
# Usage:
#   make coverage           # produces coverage.out
#   ./scripts/coverage-summary.sh [--top-low N]
#
# Outputs three sections:
#   1. Project overall %
#   2. Top-N lowest-covered packages
#   3. Per-package breakdown sorted by coverage desc
set -euo pipefail

COVERAGE_FILE="${COVERAGE_FILE:-coverage.out}"
TOP_LOW="${1:-10}"
if [[ "$TOP_LOW" == --top-low ]]; then
  TOP_LOW="${2:-10}"
fi

if [[ ! -f "$COVERAGE_FILE" ]]; then
  echo "ERROR: $COVERAGE_FILE not found. Run 'make coverage' first." >&2
  exit 1
fi

# Per-function lines: cover-mode preamble starts with "mode:"
go tool cover -func="$COVERAGE_FILE" > /tmp/coverage-func.txt

OVERALL=$(grep -E '^total:' /tmp/coverage-func.txt | awk '{print $NF}')

echo "=========================================="
echo "Saker test coverage summary"
echo "=========================================="
echo "Overall: $OVERALL"
echo

# Per-package: aggregate by stripping function name
awk '
  /^total:/ { next }
  {
    # field 1: github.com/.../pkg/foo/bar.go:LINE:
    #         strip the file part to get the package path
    split($1, a, "/")
    file = a[length(a)]
    pkg = ""
    for (i = 1; i < length(a); i++) {
      pkg = pkg (i > 1 ? "/" : "") a[i]
    }
    # parse coverage as number
    cov = $NF
    gsub(/%$/, "", cov)
    pkgcov[pkg] += cov + 0
    pkgcnt[pkg] += 1
  }
  END {
    for (p in pkgcov) {
      printf "%6.1f%% %s\n", pkgcov[p] / pkgcnt[p], p
    }
  }
' /tmp/coverage-func.txt | sort -n > /tmp/coverage-pkg.txt

echo "Lowest-covered packages (top $TOP_LOW):"
echo "------------------------------------------"
head -n "$TOP_LOW" /tmp/coverage-pkg.txt
echo

echo "All packages (sorted ascending):"
echo "------------------------------------------"
cat /tmp/coverage-pkg.txt
