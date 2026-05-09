#!/usr/bin/env bash
set -euo pipefail

echo "==> checking for stale internal documentation references"
if rg -n "web-editor/|web/docs|docs/plans|UPSTREAM.md|INSPIRATION|MANUSCRIPT|TODO\\(open-source\\)" \
  --glob '!**/node_modules/**' \
  --glob '!**/out/**' \
  --glob '!cmd/cli/**/dist/**' \
  --glob '!scripts/check-open-source-readiness.sh' \
  .; then
  echo "error: stale internal/open-source cleanup reference found" >&2
  exit 1
fi

echo "==> checking for obvious committed secrets"
if rg -n "BEGIN (RSA|DSA|EC|OPENSSH|PRIVATE) KEY|sk-(ant-|proj-|live_|test_)[A-Za-z0-9_-]{16,}|sk-[A-Za-z0-9]{32,}|AKIA[0-9A-Z]{16}" \
  --glob '!**/node_modules/**' \
  --glob '!**/out/**' \
  --glob '!cmd/cli/**/dist/**' \
  --glob '!**/*_test.go' \
  --glob '!README.md' \
  --glob '!README_zh.md' \
  --glob '!.env.example' \
  --glob '!scripts/check-open-source-readiness.sh' \
  .; then
  echo "error: possible secret found" >&2
  exit 1
fi

echo "==> running leak detector tests"
go test ./pkg/security

echo "open-source readiness checks passed"
