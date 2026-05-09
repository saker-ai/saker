#!/usr/bin/env bash
# seed-demo-app.sh — wire a comprehensive "Publish Canvas as App" demo end-to-end.
#
# This demo exercises ALL four executable gen-node types in a single canvas:
#   • textGen   (LLM)
#   • imageGen  (DashScope qwen-image)
#   • videoGen  (DashScope wan2.7-t2v, async — needs waitForCompletion)
#   • voiceGen  (DashScope qwen3-tts-flash)
# It also exercises the three appInput field types: text, number, select.
#
# Prereqs:
#   - Server running (e.g. `make run` in another terminal). Default base URL
#     is http://localhost:10112; override with SAKER_API_BASE.
#   - jq installed (used to parse JSON IDs out of API responses).
#   - ANTHROPIC_API_KEY set in the server's env so textGen has an LLM to call.
#   - DASHSCOPE_API_KEY set in the server's env so the alibabacloud aigo
#     providers configured in .saker/settings.local.json can authenticate.
#   - .saker/settings.local.json must declare three aigo providers:
#       dashscope-image, dashscope-video, dashscope-tts
#     (see the matching block this repo ships with).
#
# Multi-tenant note:
#   Localhost requests are auto-bound to the local admin user. The script
#   discovers the admin's personal project via /api/rpc/project/list and
#   uses the multi-tenant REST paths (/api/apps/{projectId}/...). Override
#   the project with SAKER_PROJECT_ID=<uuid>.
#
# Usage:
#   bash scripts/seed-demo-app.sh
#   SAKER_API_BASE=http://127.0.0.1:10112 bash scripts/seed-demo-app.sh
#   COOKIE='session=...' bash scripts/seed-demo-app.sh    # multi-tenant mode

set -euo pipefail

BASE="${SAKER_API_BASE:-http://localhost:10112}"
COOKIE_HEADER=()
if [[ -n "${COOKIE:-}" ]]; then
  COOKIE_HEADER=(-H "Cookie: ${COOKIE}")
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "error: jq not found; install with 'apt-get install jq' or 'brew install jq'." >&2
  exit 1
fi

if ! curl -fsS "${BASE}/health" >/dev/null 2>&1; then
  echo "error: server unreachable at ${BASE}. Run 'make run' first, or set SAKER_API_BASE." >&2
  exit 1
fi

# ---------- 0. Resolve project (multi-tenant mode) ---------------------------

PROJECT_ID="${SAKER_PROJECT_ID:-}"
if [[ -z "${PROJECT_ID}" ]]; then
  LIST_RESP=$(curl -fsS -X POST "${BASE}/api/rpc/project/list" \
    "${COOKIE_HEADER[@]}" \
    -H 'Content-Type: application/json' \
    -d '{}' 2>/dev/null || true)
  PROJECT_ID=$(echo "${LIST_RESP}" | jq -r '.projects[0].id // empty' 2>/dev/null || true)
fi

if [[ -n "${PROJECT_ID}" ]]; then
  echo "→ Using project: ${PROJECT_ID}"
  APPS_BASE="${BASE}/api/apps/${PROJECT_ID}"
  CANVAS_PROJECT_FIELD=",\"projectId\":\"${PROJECT_ID}\""
else
  echo "→ Single-tenant mode (no projectId)"
  APPS_BASE="${BASE}/api/apps"
  CANVAS_PROJECT_FIELD=""
fi

THREAD_ID="demo-fullstack-app"

# Inputs (3 types)
APPIN_TOPIC_ID="appin_topic"        # text
APPIN_WORDS_ID="appin_words"        # number
APPIN_STYLE_ID="appin_style"        # select

# Gen nodes (4 types)
TEXTGEN_ID="textgen_haiku"
IMAGEGEN_ID="imagegen_cover"
VIDEOGEN_ID="videogen_clip"
VOICEGEN_ID="voicegen_narration"

# Outputs (4 kinds)
APPOUT_TEXT_ID="appout_text"
APPOUT_IMAGE_ID="appout_image"
APPOUT_VIDEO_ID="appout_video"
APPOUT_AUDIO_ID="appout_audio"

# ---------- 1. Save canvas ---------------------------------------------------

echo "→ Saving canvas thread '${THREAD_ID}'..."
SAVE_BODY=$(cat <<EOF
{
  "threadId": "${THREAD_ID}"${CANVAS_PROJECT_FIELD},
  "nodes": [
    {
      "id": "${APPIN_TOPIC_ID}",
      "type": "appInput",
      "position": {"x": 80, "y": 40},
      "data": {
        "nodeType": "appInput",
        "label": "Topic",
        "appVariable": "topic",
        "appFieldType": "text",
        "appRequired": true,
        "appDefault": "a red panda eating sushi"
      }
    },
    {
      "id": "${APPIN_WORDS_ID}",
      "type": "appInput",
      "position": {"x": 80, "y": 180},
      "data": {
        "nodeType": "appInput",
        "label": "Word count",
        "appVariable": "wordCount",
        "appFieldType": "number",
        "appRequired": false,
        "appDefault": 12,
        "appMin": 5,
        "appMax": 40
      }
    },
    {
      "id": "${APPIN_STYLE_ID}",
      "type": "appInput",
      "position": {"x": 80, "y": 320},
      "data": {
        "nodeType": "appInput",
        "label": "Visual style",
        "appVariable": "style",
        "appFieldType": "select",
        "appRequired": true,
        "appDefault": "watercolor",
        "appOptions": ["watercolor", "pixel-art", "anime", "photorealistic"]
      }
    },
    {
      "id": "${TEXTGEN_ID}",
      "type": "textGen",
      "position": {"x": 380, "y": 40},
      "data": {
        "nodeType": "textGen",
        "prompt": "Write a playful 3-line haiku (5-7-5 syllables) about a red panda eating sushi. Output only the haiku, no commentary."
      }
    },
    {
      "id": "${IMAGEGEN_ID}",
      "type": "imageGen",
      "position": {"x": 380, "y": 180},
      "data": {
        "nodeType": "imageGen",
        "engine": "alibabacloud/z-image-turbo",
        "prompt": "watercolor illustration of a red panda holding a piece of sushi, soft lighting, gentle pastel palette",
        "aspectRatio": "1:1"
      }
    },
    {
      "id": "${VIDEOGEN_ID}",
      "type": "videoGen",
      "position": {"x": 380, "y": 320},
      "data": {
        "nodeType": "videoGen",
        "engine": "alibabacloud/wan2.7-t2v",
        "prompt": "a red panda playfully nibbling a piece of sushi on a wooden table, soft daylight, cinematic close-up",
        "aspectRatio": "16:9",
        "duration": 5
      }
    },
    {
      "id": "${VOICEGEN_ID}",
      "type": "voiceGen",
      "position": {"x": 380, "y": 460},
      "data": {
        "nodeType": "voiceGen",
        "engine": "alibabacloud/qwen3-tts-flash",
        "prompt": "Behold: the smallest sushi chef the forest has ever known.",
        "voice": "Cherry",
        "language": "en"
      }
    },
    {
      "id": "${APPOUT_TEXT_ID}",
      "type": "appOutput",
      "position": {"x": 720, "y": 40},
      "data": {
        "nodeType": "appOutput",
        "label": "Haiku",
        "appOutputKind": "text"
      }
    },
    {
      "id": "${APPOUT_IMAGE_ID}",
      "type": "appOutput",
      "position": {"x": 720, "y": 180},
      "data": {
        "nodeType": "appOutput",
        "label": "Cover image",
        "appOutputKind": "image"
      }
    },
    {
      "id": "${APPOUT_VIDEO_ID}",
      "type": "appOutput",
      "position": {"x": 720, "y": 320},
      "data": {
        "nodeType": "appOutput",
        "label": "Teaser clip",
        "appOutputKind": "video"
      }
    },
    {
      "id": "${APPOUT_AUDIO_ID}",
      "type": "appOutput",
      "position": {"x": 720, "y": 460},
      "data": {
        "nodeType": "appOutput",
        "label": "Narration",
        "appOutputKind": "audio"
      }
    }
  ],
  "edges": [
    {"id": "e_topic_text",  "source": "${APPIN_TOPIC_ID}", "target": "${TEXTGEN_ID}",   "type": "context"},
    {"id": "e_topic_image", "source": "${APPIN_TOPIC_ID}", "target": "${IMAGEGEN_ID}",  "type": "context"},
    {"id": "e_style_image", "source": "${APPIN_STYLE_ID}", "target": "${IMAGEGEN_ID}",  "type": "context"},
    {"id": "e_topic_video", "source": "${APPIN_TOPIC_ID}", "target": "${VIDEOGEN_ID}",  "type": "context"},
    {"id": "e_topic_voice", "source": "${APPIN_TOPIC_ID}", "target": "${VOICEGEN_ID}",  "type": "context"},
    {"id": "e_words_voice", "source": "${APPIN_WORDS_ID}", "target": "${VOICEGEN_ID}",  "type": "context"},

    {"id": "e_text_out",  "source": "${TEXTGEN_ID}",  "target": "${APPOUT_TEXT_ID}",  "type": "flow"},
    {"id": "e_image_out", "source": "${IMAGEGEN_ID}", "target": "${APPOUT_IMAGE_ID}", "type": "flow"},
    {"id": "e_video_out", "source": "${VIDEOGEN_ID}", "target": "${APPOUT_VIDEO_ID}", "type": "flow"},
    {"id": "e_voice_out", "source": "${VOICEGEN_ID}", "target": "${APPOUT_AUDIO_ID}", "type": "flow"}
  ]
}
EOF
)
curl -fsS -X POST "${BASE}/api/rpc/canvas/save" \
  "${COOKIE_HEADER[@]}" \
  -H 'Content-Type: application/json' \
  -d "${SAVE_BODY}" >/dev/null
echo "  ✓ canvas saved (3 inputs, 4 gen nodes, 4 outputs)"

# ---------- 2. Create app ----------------------------------------------------

echo "→ Creating app from thread..."
CREATE_BODY=$(cat <<EOF
{
  "name": "Multi-Modal Demo",
  "description": "Full-stack demo: text + image + video + TTS in one canvas.",
  "icon": "🎨",
  "sourceThreadId": "${THREAD_ID}"
}
EOF
)
APP_RESP=$(curl -fsS -X POST "${APPS_BASE}" \
  "${COOKIE_HEADER[@]}" \
  -H 'Content-Type: application/json' \
  -d "${CREATE_BODY}")
APP_ID=$(echo "${APP_RESP}" | jq -r '.id')
echo "  ✓ app created: ${APP_ID}"

# ---------- 3. Publish -------------------------------------------------------

echo "→ Publishing first version..."
curl -fsS -X POST "${APPS_BASE}/${APP_ID}/publish" \
  "${COOKIE_HEADER[@]}" \
  -H 'Content-Type: application/json' \
  -d '{}' >/dev/null
echo "  ✓ published"

# ---------- 4. Mint share token ---------------------------------------------

echo "→ Creating share token..."
SHARE_RESP=$(curl -fsS -X POST "${APPS_BASE}/${APP_ID}/share" \
  "${COOKIE_HEADER[@]}" \
  -H 'Content-Type: application/json' \
  -d '{"expiresInDays": 7, "rateLimit": 30}')
SHARE_TOKEN=$(echo "${SHARE_RESP}" | jq -r '.token // .Token // empty')
if [[ -z "${SHARE_TOKEN}" ]]; then
  echo "  ! share response missing token; raw: ${SHARE_RESP}" >&2
fi

# ---------- 5. Mint API key --------------------------------------------------

echo "→ Creating API key (7-day expiry)..."
KEY_RESP=$(curl -fsS -X POST "${APPS_BASE}/${APP_ID}/keys" \
  "${COOKIE_HEADER[@]}" \
  -H 'Content-Type: application/json' \
  -d '{"name": "demo-key", "expiresInDays": 7}')
API_KEY=$(echo "${KEY_RESP}" | jq -r '.apiKey // .ApiKey // empty')

# ---------- 6. Print URLs + curl --------------------------------------------

echo
echo "=========================================================="
echo " 🎉 Demo app provisioned"
echo "=========================================================="
echo " App page:   ${BASE}/#apps/${APP_ID}"
if [[ -n "${SHARE_TOKEN}" ]]; then
  echo " Share URL:  ${BASE}/share/${SHARE_TOKEN}/"
fi
if [[ -n "${API_KEY}" ]]; then
  echo
  echo " API key (shown once — save it):"
  echo "   ${API_KEY}"
  echo
  echo " Try it (all 4 inputs):"
  echo "   curl -X POST '${APPS_BASE}/${APP_ID}/run' \\"
  echo "     -H 'Authorization: Bearer ${API_KEY}' \\"
  echo "     -H 'Content-Type: application/json' \\"
  echo "     -d '{\"inputs\":{\"topic\":\"a fox at sunrise\",\"wordCount\":15,\"style\":\"anime\"}}'"
fi
echo
echo " The canvas wires inputs into all 4 gen nodes via context"
echo " edges. textGen prompts itself; image/video/voice nodes use"
echo " their fixed prompts (the runtime does not yet template app"
echo " inputs into downstream node prompts). Inputs still flow"
echo " through validation + type-coercion (number/select), and the"
echo " 4 outputs cover all media kinds (text/image/video/audio)."
echo
echo " Expected per-run latency: ~30-90s (videoGen async polling)."
echo "=========================================================="
