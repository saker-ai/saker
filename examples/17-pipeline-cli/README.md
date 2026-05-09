# 17-pipeline-cli — Pipeline CLI Demo

[中文](#中文说明) | English

## Overview

Self-contained demo showcasing the multimodal pipeline execution engine with **cache**, **timeline**, and **lineage** — no API key required.

The demo simulates a video processing pipeline:

```
input_video → [extract-frames] → 8 frames → [stylize] (fan-out, concurrency=4) → 8 styled frames → [compose] → final_video
```

All tools are local stubs with simulated latency, so the pipeline runs entirely offline.

## Features Demonstrated

| Feature | Description |
|---------|-------------|
| **Declarative Pipeline** | Pipeline defined in `pipeline.json` — Batch, FanOut with bounded concurrency |
| **Timeline** | Structured execution trace with 10 event kinds (tool_call, tool_result, cache_hit/miss, latency, artifacts) |
| **Lineage** | DAG of artifact derivations exported as Graphviz DOT |
| **Cache** | Content-addressed result cache — second run shows cache hits |

## Pipeline Definition

`pipeline.json` declares a three-stage batch:

```json
{
  "batch": {
    "steps": [
      { "name": "extract-frames", "tool": "frame_extractor", "input": [...] },
      { "fan_out": { "collection": "frames", "concurrency": 4, "step": { "name": "stylize", "tool": "stylizer" } } },
      { "name": "compose", "tool": "composer" }
    ]
  }
}
```

## Usage

### Basic Run

```bash
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json
```

Output:
```
=== PIPELINE RESULT ===
output: composed final video from styled frames
stop_reason: completed
artifacts: 1
  [video] final_video (generated)
```

### With Timeline

```bash
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json --timeline
```

Output includes a full event trace:
```
=== TIMELINE (25 events) ===
  input_artifact       extract-frames       input_video
  tool_call            extract-frames
  tool_result          extract-frames       extracted 8 frames
  latency_snapshot     extract-frames       5.3ms
  generated_artifact   extract-frames       frame_000
  ...
  tool_call            compose
  tool_result          compose              composed final video from styled frames
  latency_snapshot     compose              5.2ms
  generated_artifact   compose              final_video
```

### With Lineage Graph

```bash
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json --lineage dot
```

Output includes Graphviz DOT:
```dot
digraph lineage {
  rankdir=LR;
  "input_video" -> "frame_000" [label="extract-frames"];
  "input_video" -> "frame_001" [label="extract-frames"];
  ...
  "frame_000" -> "final_video" [label="compose"];
  "frame_001" -> "final_video" [label="compose"];
  ...
}
```

Render to PNG (requires Graphviz):
```bash
go run ./examples/17-pipeline-cli \
  --pipeline examples/17-pipeline-cli/pipeline.json \
  --lineage dot 2>/dev/null | grep -A999 'digraph' | dot -Tpng > lineage.png
```

### All Flags Combined

```bash
go run ./examples/17-pipeline-cli \
  --pipeline examples/17-pipeline-cli/pipeline.json \
  --timeline \
  --lineage dot
```

### Via Makefile

```bash
make demo-pipeline
```

## Stub Tools

| Tool | Input | Output | Latency |
|------|-------|--------|---------|
| `frame_extractor` | 1 video artifact | 8 image artifacts (`frame_000`..`frame_007`) | 5ms |
| `stylizer` | 1 image artifact | 1 styled image (`styled_<id>`) | 10ms |
| `composer` | N image artifacts | 1 video artifact (`final_video`) | 5ms |

## CLI Flags

| Flag | Type | Description |
|------|------|-------------|
| `--pipeline` | string | Path to pipeline JSON file (required) |
| `--timeline` | bool | Print timeline events after result |
| `--lineage` | string | Lineage output format (`dot` for Graphviz DOT) |

## agentctl Pipeline Mode

The main CLI (`cmd/cli`) also supports pipeline mode with the same flags:

```bash
# Build agentctl
make agentctl

# Run pipeline via agentctl (requires tools registered in runtime)
./bin/agentctl --pipeline path/to/pipeline.json --timeline --lineage dot
```

---

## 中文说明

### 概述

自包含的 Pipeline CLI 演示，展示多模态流水线执行引擎的 **缓存**、**时间线** 和 **血缘追踪** 能力 — 无需 API Key。

演示模拟一个视频处理流水线：

```
input_video → [抽帧] → 8 帧图片 → [风格化] (fan-out, 并发=4) → 8 张风格化图片 → [合成] → final_video
```

所有工具都是本地 stub，带模拟延迟，完全离线运行。

### 演示的核心能力

| 能力 | 说明 |
|------|------|
| **声明式 Pipeline** | 通过 `pipeline.json` 定义流水线 — 支持 Batch、FanOut 及并发控制 |
| **Timeline 时间线** | 结构化执行轨迹，包含 10 种事件类型（tool_call、cache_hit/miss、latency 等） |
| **Lineage 血缘** | 制品（Artifact）间的派生关系 DAG，可导出为 Graphviz DOT 格式 |
| **Cache 缓存** | 基于内容寻址的结果缓存 — 二次运行可看到 cache hit |

### 运行方式

```bash
# 基础运行
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json

# 显示时间线
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json --timeline

# 输出血缘图
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json --lineage dot

# 全部功能
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json --timeline --lineage dot

# 通过 Makefile
make demo-pipeline
```

### Pipeline JSON 结构

```json
{
  "batch": {
    "steps": [
      {"name": "extract-frames", "tool": "frame_extractor", "input": [...]},
      {"fan_out": {"collection": "frames", "concurrency": 4, "step": {"name": "stylize", "tool": "stylizer"}}},
      {"name": "compose", "tool": "composer"}
    ]
  }
}
```

支持的 Step 类型：
- **Leaf Step**: `{name, tool, input, with}` — 单步工具调用
- **Batch**: `{batch: {steps: [...]}}` — 顺序执行多步
- **FanOut**: `{fan_out: {collection, concurrency, step}}` — 并行映射
- **FanIn**: `{fan_in: {strategy, into}}` — 聚合结果
- **Retry**: `{retry: {attempts, backoff_ms, step}}` — 指数退避重试
- **Checkpoint**: `{checkpoint: {name, step}}` — 可恢复断点

### agentctl Pipeline 模式

主 CLI (`cmd/cli`) 同样支持 pipeline 模式：

```bash
make agentctl
./bin/agentctl --pipeline path/to/pipeline.json --timeline --lineage dot
```
