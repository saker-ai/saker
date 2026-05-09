#!/bin/bash
# update-model-data.sh — Download the latest LiteLLM model pricing & context window data.
# Usage: ./scripts/update-model-data.sh
#
# This updates the embedded JSON used by pkg/model for context window lookups
# and cost estimation. After running, rebuild the binary to include the new data.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TARGET="$REPO_ROOT/pkg/model/data/model_prices_and_context_window.json"

echo "Downloading LiteLLM model data..."
curl -sL 'https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json' \
  -o "$TARGET"

SIZE=$(wc -c < "$TARGET" | tr -d ' ')
MODELS=$(python3 -c "import json; print(len(json.load(open('$TARGET'))))" 2>/dev/null || echo "?")

echo "Updated $TARGET"
echo "  Size: ${SIZE} bytes"
echo "  Models: ${MODELS} entries"
echo ""
echo "Rebuild the binary to include updated data:"
echo "  make build"
