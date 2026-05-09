#!/bin/bash
# update-china-model-data.sh — Build a curated China model catalog from LiteLLM base data
# plus verified official China pricing metadata.
#
# Usage: ./scripts/update-china-model-data.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BASE="$REPO_ROOT/pkg/model/data/model_prices_and_context_window.json"
TARGET="$REPO_ROOT/pkg/model/data/china_model_catalog.json"

if [ ! -f "$BASE" ]; then
  echo "Base model data not found: $BASE"
  echo "Run ./scripts/update-model-data.sh first."
  exit 1
fi

python3 - "$BASE" "$TARGET" <<'PY'
import json
import sys
from pathlib import Path

BASE = Path(sys.argv[1])
TARGET = Path(sys.argv[2])
SOURCE_VERIFIED_AT = "2026-04-04"


def price_point(
    *,
    input_per_million=0.0,
    output_per_million=0.0,
    output_thinking_per_million=0.0,
    cache_hit_per_million=0.0,
    cache_read_per_million=0.0,
    cache_write_per_million=0.0,
):
    return {
        "input_per_million": input_per_million,
        "output_per_million": output_per_million,
        "output_thinking_per_million": output_thinking_per_million,
        "cache_hit_per_million": cache_hit_per_million,
        "cache_read_per_million": cache_read_per_million,
        "cache_write_per_million": cache_write_per_million,
    }


def price_tier(*, min_input_tokens=0, max_input_tokens=0, input_per_million=0.0, output_per_million=0.0, output_thinking_per_million=0.0):
    return {
        "min_input_tokens": min_input_tokens,
        "max_input_tokens": max_input_tokens,
        "input_per_million": input_per_million,
        "output_per_million": output_per_million,
        "output_thinking_per_million": output_thinking_per_million,
    }


def per_million_from_token_cost(value):
    if value in (None, "", 0):
        return 0.0
    return round(float(value) * 1_000_000, 6)


def doubao_tiers(base_record):
    tiers = []
    for tier in base_record.get("tiered_pricing", []):
        lower, upper = tier.get("range", [0, 0])
        tiers.append(
            price_tier(
                min_input_tokens=int(lower or 0),
                max_input_tokens=int(upper or 0),
                input_per_million=per_million_from_token_cost(tier.get("input_cost_per_token")),
                output_per_million=per_million_from_token_cost(tier.get("output_cost_per_token")),
            )
        )
    return tiers


with BASE.open() as f:
    litellm = json.load(f)

seed = [
    {
        "name": "deepseek-chat",
        "vendor": "DeepSeek",
        "family": "DeepSeek-V3.2",
        "provider": "deepseek",
        "base_litellm_key": "deepseek-chat",
        "aliases": ["deepseek-v3.2", "deepseek-chat-v3", "deepseek/deepseek-chat"],
        "pricing": {
            "currency": "CNY",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=2.0, output_per_million=3.0, cache_hit_per_million=0.2),
        },
        "source_url": "https://api-docs.deepseek.com/zh-cn/quick_start/pricing",
        "notes": "Official China pricing page lists DeepSeek-V3.2 for both deepseek-chat and deepseek-reasoner.",
    },
    {
        "name": "deepseek-reasoner",
        "vendor": "DeepSeek",
        "family": "DeepSeek-V3.2",
        "provider": "deepseek",
        "base_litellm_key": "deepseek-reasoner",
        "aliases": ["deepseek-r1", "deepseek/deepseek-reasoner"],
        "pricing": {
            "currency": "CNY",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=2.0, output_per_million=3.0, cache_hit_per_million=0.2),
        },
        "source_url": "https://api-docs.deepseek.com/zh-cn/quick_start/pricing",
        "notes": "Official China pricing page lists DeepSeek-V3.2 for both deepseek-chat and deepseek-reasoner.",
    },
    {
        "name": "qwen-max",
        "vendor": "Alibaba Cloud",
        "family": "Qwen Max",
        "provider": "dashscope",
        "base_litellm_key": "dashscope/qwen-max",
        "aliases": ["dashscope/qwen-max", "qwen-max-latest"],
        "pricing": {
            "currency": "USD",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=0.345, output_per_million=1.377),
        },
        "source_url": "https://www.alibabacloud.com/help/zh/model-studio/model-pricing",
        "notes": "China Mainland deployment pricing.",
    },
    {
        "name": "qwen-plus",
        "vendor": "Alibaba Cloud",
        "family": "Qwen Plus",
        "provider": "dashscope",
        "base_litellm_key": "dashscope/qwen-plus",
        "aliases": ["dashscope/qwen-plus", "qwen-plus-latest"],
        "pricing": {
            "currency": "USD",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=0.4, output_per_million=1.2, output_thinking_per_million=4.0),
            "tiers": [
                price_tier(min_input_tokens=0, max_input_tokens=256_000, input_per_million=0.4, output_per_million=1.2, output_thinking_per_million=4.0),
                price_tier(min_input_tokens=256_000, max_input_tokens=1_000_000, input_per_million=1.2, output_per_million=3.6, output_thinking_per_million=12.0),
            ],
        },
        "source_url": "https://www.alibabacloud.com/help/zh/model-studio/model-pricing",
        "notes": "China Mainland deployment pricing with tiered rates by input-token range.",
    },
    {
        "name": "qwen-turbo",
        "vendor": "Alibaba Cloud",
        "family": "Qwen Turbo",
        "provider": "dashscope",
        "base_litellm_key": "dashscope/qwen-turbo",
        "aliases": ["dashscope/qwen-turbo", "qwen-turbo-latest"],
        "pricing": {
            "currency": "USD",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=0.044, output_per_million=0.087, output_thinking_per_million=0.431),
            "modes": {
                "non_thinking": price_point(input_per_million=0.044, output_per_million=0.087),
                "thinking": price_point(input_per_million=0.044, output_per_million=0.431),
            },
        },
        "source_url": "https://www.alibabacloud.com/help/zh/model-studio/model-pricing",
        "notes": "China Mainland deployment pricing; thinking mode bills a higher output rate.",
    },
    {
        "name": "qwen-vl-max",
        "vendor": "Alibaba Cloud",
        "family": "Qwen VL",
        "provider": "dashscope",
        "base_litellm_key": "dashscope/qwen-vl-max",
        "aliases": ["dashscope/qwen-vl-max", "qwen-vl-max-latest"],
        "max_input_tokens": 131_072,
        "supports_vision": True,
        "pricing": {
            "currency": "USD",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=0.8, output_per_million=3.2),
        },
        "source_url": "https://www.alibabacloud.com/help/zh/model-studio/model-pricing",
        "notes": "China Mainland deployment pricing.",
    },
    {
        "name": "qwen-vl-plus",
        "vendor": "Alibaba Cloud",
        "family": "Qwen VL",
        "provider": "dashscope",
        "base_litellm_key": "dashscope/qwen-vl-plus",
        "aliases": ["dashscope/qwen-vl-plus", "qwen-vl-plus-latest"],
        "max_input_tokens": 131_072,
        "supports_vision": True,
        "pricing": {
            "currency": "USD",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=0.21, output_per_million=0.63),
        },
        "source_url": "https://www.alibabacloud.com/help/zh/model-studio/model-pricing",
        "notes": "China Mainland deployment pricing.",
    },
    {
        "name": "kimi-k2.5",
        "vendor": "Moonshot AI",
        "family": "Kimi K2.5",
        "provider": "moonshot",
        "base_litellm_key": "moonshot/kimi-k2.5",
        "aliases": ["moonshot/kimi-k2.5", "moonshotai.kimi-k2.5"],
        "pricing": {
            "currency": "CNY",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=4.0, output_per_million=21.0, cache_hit_per_million=0.7),
        },
        "source_url": "https://platform.moonshot.cn/",
        "notes": "Official platform home page pricing card.",
    },
    {
        "name": "kimi-k2",
        "vendor": "Moonshot AI",
        "family": "Kimi K2",
        "provider": "moonshot",
        "base_litellm_key": "moonshot/kimi-k2-0905-preview",
        "aliases": ["moonshot/kimi-k2-0905-preview", "moonshot/kimi-k2", "kimi-k2-0905"],
        "pricing": {
            "currency": "CNY",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=4.0, output_per_million=16.0, cache_hit_per_million=1.0),
        },
        "source_url": "https://platform.moonshot.cn/",
        "notes": "Official platform home page pricing card labelled K2 0905.",
    },
    {
        "name": "kimi-k2-thinking",
        "vendor": "Moonshot AI",
        "family": "Kimi K2 Thinking",
        "provider": "moonshot",
        "base_litellm_key": "moonshot/kimi-k2-thinking",
        "aliases": ["moonshot/kimi-k2-thinking", "kimi-k2-thinking-251104"],
        "pricing": {
            "currency": "CNY",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=4.0, output_per_million=16.0, cache_hit_per_million=1.0),
        },
        "source_url": "https://platform.moonshot.cn/",
        "notes": "Official platform home page pricing card.",
    },
    {
        "name": "glm-4.5",
        "vendor": "Zhipu AI",
        "family": "GLM-4.5",
        "provider": "zai",
        "base_litellm_key": "zai/glm-4.5",
        "aliases": ["zai/glm-4.5", "glm-4.5-air", "zai/glm-4.5-air"],
        "pricing": {
            "currency": "CNY",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=0.8, output_per_million=2.0),
        },
        "source_url": "https://docs.bigmodel.cn/cn/guide/models/text/glm-4.5",
        "notes": "Official GLM-4.5 page states pricing is as low as input 0.8 CNY / output 2 CNY per million tokens.",
    },
    {
        "name": "minimax-m2.5",
        "vendor": "MiniMax",
        "family": "MiniMax M2.5",
        "provider": "minimax",
        "base_litellm_key": "minimax/MiniMax-M2.5",
        "aliases": ["MiniMax-M2.5", "minimax/MiniMax-M2.5"],
        "pricing": {
            "currency": "CNY",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=2.1, output_per_million=8.4, cache_read_per_million=0.21, cache_write_per_million=2.625),
        },
        "source_url": "https://platform.minimaxi.com/docs/guides/pricing-paygo",
        "notes": "Official pay-as-you-go pricing page.",
    },
    {
        "name": "minimax-m2.1",
        "vendor": "MiniMax",
        "family": "MiniMax M2.1",
        "provider": "minimax",
        "base_litellm_key": "minimax/MiniMax-M2.1",
        "aliases": ["MiniMax-M2.1", "minimax/MiniMax-M2.1"],
        "pricing": {
            "currency": "CNY",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=2.1, output_per_million=8.4, cache_read_per_million=0.21, cache_write_per_million=2.625),
        },
        "source_url": "https://platform.minimaxi.com/docs/guides/pricing-paygo",
        "notes": "Official pay-as-you-go pricing page; listed under historical text models.",
    },
    {
        "name": "minimax-m2",
        "vendor": "MiniMax",
        "family": "MiniMax M2",
        "provider": "minimax",
        "base_litellm_key": "minimax/MiniMax-M2",
        "aliases": ["MiniMax-M2", "minimax/MiniMax-M2"],
        "pricing": {
            "currency": "CNY",
            "unit": "per_million_tokens",
            "default": price_point(input_per_million=2.1, output_per_million=8.4, cache_read_per_million=0.21, cache_write_per_million=2.625),
        },
        "source_url": "https://platform.minimaxi.com/docs/guides/pricing-paygo",
        "notes": "Official pay-as-you-go pricing page; listed under historical text models.",
    },
]

for base_key in [
    "volcengine/doubao-seed-2-0-pro-260215",
    "volcengine/doubao-seed-2-0-lite-260215",
    "volcengine/doubao-seed-2-0-mini-260215",
    "volcengine/doubao-seed-2-0-code-preview-260215",
]:
    base_record = litellm.get(base_key, {})
    name = base_key.split("/", 1)[1]
    family = "Doubao Seed 2.0"
    if "code" in name:
        family = "Doubao Seed Code"
    seed.append(
        {
            "name": name,
            "vendor": "Volcengine",
            "family": family,
            "provider": "volcengine",
            "base_litellm_key": base_key,
            "aliases": [base_key, name.replace("-", ".")],
            "pricing": {
                "currency": "CNY",
                "unit": "per_million_tokens",
                "tiers": doubao_tiers(base_record),
            },
            "source_url": base_record.get("source", "https://www.volcengine.com/docs/82379/1544106?lang=zh"),
            "notes": "Tiered China pricing derived from the official Volcengine source URL embedded in the LiteLLM base catalog.",
        }
    )

catalog = {}
for entry in seed:
    base_key = entry["base_litellm_key"]
    base = litellm.get(base_key, {})
    aliases = sorted({alias for alias in [entry["name"], base_key, *entry.get("aliases", [])] if alias})
    max_output_tokens = base.get("max_output_tokens") or base.get("max_tokens") or 0
    catalog[entry["name"]] = {
        "name": entry["name"],
        "vendor": entry["vendor"],
        "provider": entry.get("provider") or base.get("litellm_provider", ""),
        "family": entry["family"],
        "region": "china_mainland",
        "base_litellm_key": base_key,
        "aliases": aliases,
        "mode": base.get("mode", "chat"),
        "max_input_tokens": int(entry.get("max_input_tokens") or base.get("max_input_tokens") or 0),
        "max_output_tokens": int(entry.get("max_output_tokens") or max_output_tokens or 0),
        "supports_vision": bool(entry.get("supports_vision", base.get("supports_vision", False))),
        "supports_function_calling": bool(entry.get("supports_function_calling", base.get("supports_function_calling", False))),
        "supports_prompt_caching": bool(entry.get("supports_prompt_caching", base.get("supports_prompt_caching", False))),
        "supports_response_schema": bool(entry.get("supports_response_schema", base.get("supports_response_schema", False))),
        "supports_reasoning": bool(entry.get("supports_reasoning", base.get("supports_reasoning", False))),
        "pricing": {
            "currency": entry["pricing"]["currency"],
            "unit": entry["pricing"]["unit"],
            "default": entry["pricing"].get("default", price_point()),
            "modes": entry["pricing"].get("modes", {}),
            "tiers": entry["pricing"].get("tiers", []),
        },
        "source_url": entry["source_url"],
        "source_verified_at": SOURCE_VERIFIED_AT,
        "notes": entry["notes"],
    }

TARGET.write_text(json.dumps(catalog, indent=2, ensure_ascii=False) + "\n")
print(f"Updated {TARGET}")
print(f"  Models: {len(catalog)} entries")
print(f"  Size: {TARGET.stat().st_size} bytes")
PY
