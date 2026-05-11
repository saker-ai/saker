#!/bin/bash
# Usage: ./scripts/check-doc-sync.sh
#
# Detects mismatches between the Chinese (docs/zh/) and English documentation
# trees. The English tree is docs/en/ when it exists, otherwise the top-level
# docs/ directory (excluding docs/zh/ itself).
#
# Behavior:
#   * If docs/zh/ does not exist, exit 0 with a "skipping" message.
#   * Otherwise list any markdown file present in one tree but missing from
#     the other and exit 1 if any mismatch is found.
#   * Additionally warn (without failing) when an English file is older than
#     its Chinese counterpart by mtime, suggesting the translation is ahead.
#
# Intended for CI gating; safe to run locally.

set -euo pipefail

# Resolve repo root from this script's location so the script works no matter
# where the caller invokes it from.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

ZH_DIR="${REPO_ROOT}/docs/zh"
EN_DIR_DEDICATED="${REPO_ROOT}/docs/en"
EN_DIR_TOPLEVEL="${REPO_ROOT}/docs"

if [[ ! -d "${ZH_DIR}" ]]; then
  echo "No localization tree found, skipping (expected ${ZH_DIR})"
  exit 0
fi

# Pick the English root: prefer dedicated docs/en/, else top-level docs/.
if [[ -d "${EN_DIR_DEDICATED}" ]]; then
  EN_DIR="${EN_DIR_DEDICATED}"
  EN_LABEL="docs/en"
  EN_IS_TOPLEVEL=0
else
  EN_DIR="${EN_DIR_TOPLEVEL}"
  EN_LABEL="docs"
  EN_IS_TOPLEVEL=1
fi

# List markdown files relative to a root directory. When the English root is
# the top-level docs/, exclude the docs/zh/ subtree so the two sets are
# comparable.
list_md() {
  local root="$1"
  local exclude_zh="${2:-0}"
  if [[ ! -d "${root}" ]]; then
    return 0
  fi
  if [[ "${exclude_zh}" == "1" ]]; then
    ( cd "${root}" && find . -type f -name '*.md' -not -path './zh/*' \
        | sed 's|^\./||' | LC_ALL=C sort )
  else
    ( cd "${root}" && find . -type f -name '*.md' \
        | sed 's|^\./||' | LC_ALL=C sort )
  fi
}

ZH_LIST="$(list_md "${ZH_DIR}" 0)"
EN_LIST="$(list_md "${EN_DIR}" "${EN_IS_TOPLEVEL}")"

# Files only in zh (translation has no English original) and only in en.
only_in_zh="$(comm -23 <(printf '%s\n' "${ZH_LIST}") <(printf '%s\n' "${EN_LIST}") || true)"
only_in_en="$(comm -13 <(printf '%s\n' "${ZH_LIST}") <(printf '%s\n' "${EN_LIST}") || true)"

mismatch=0

if [[ -n "${only_in_zh}" ]]; then
  echo "Files in docs/zh/ with no counterpart in ${EN_LABEL}/:"
  while IFS= read -r f; do
    [[ -z "${f}" ]] && continue
    echo "  - ${f}"
  done <<< "${only_in_zh}"
  mismatch=1
fi

if [[ -n "${only_in_en}" ]]; then
  echo "Files in ${EN_LABEL}/ with no counterpart in docs/zh/:"
  while IFS= read -r f; do
    [[ -z "${f}" ]] && continue
    echo "  - ${f}"
  done <<< "${only_in_en}"
  mismatch=1
fi

# mtime check on the intersection: warn (do not fail) when English is older
# than Chinese, which usually means the translation has been updated and the
# source needs to catch up.
common="$(comm -12 <(printf '%s\n' "${ZH_LIST}") <(printf '%s\n' "${EN_LIST}") || true)"
if [[ -n "${common}" ]]; then
  while IFS= read -r f; do
    [[ -z "${f}" ]] && continue
    zh_path="${ZH_DIR}/${f}"
    en_path="${EN_DIR}/${f}"
    [[ -f "${zh_path}" && -f "${en_path}" ]] || continue
    zh_mtime="$(stat -c %Y "${zh_path}" 2>/dev/null || stat -f %m "${zh_path}" 2>/dev/null || echo 0)"
    en_mtime="$(stat -c %Y "${en_path}" 2>/dev/null || stat -f %m "${en_path}" 2>/dev/null || echo 0)"
    if [[ "${en_mtime}" -lt "${zh_mtime}" ]]; then
      echo "WARN: ${EN_LABEL}/${f} is older than docs/zh/${f} (en=${en_mtime} zh=${zh_mtime})"
    fi
  done <<< "${common}"
fi

if [[ "${mismatch}" -ne 0 ]]; then
  echo "Documentation sync check failed."
  exit 1
fi

echo "Documentation sync check passed."
exit 0
