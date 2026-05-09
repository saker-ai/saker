[中文](README_zh.md) | English

# saker Examples

Seventeen examples → **Twenty examples**. Run everything from the repo root.

**Environment Setup**

1. Copy `.env.example` to `.env` and set your API key:
```bash
cp .env.example .env
# Edit .env and set ANTHROPIC_API_KEY=sk-ant-your-key-here
```

2. Load environment variables:
```bash
source .env
```

Alternatively, export directly:
```bash
export ANTHROPIC_API_KEY=sk-ant-your-key-here
```

**Learning path**

| # | Example | Lines | Key focus | API key? |
|---|---------|-------|-----------|----------|
| 01 | basic | ~36 | Single API call, minimal surface | Yes |
| 02 | cli | ~93 | Interactive REPL with session history | Yes |
| 03 | http | ~300 | REST + SSE server, production-ready wiring | Yes |
| 04 | advanced | ~1400 | Full stack: middleware, hooks, MCP, sandbox, skills | Yes |
| 05 | custom-tools | ~58 | Selective built-in tools + custom registration | Yes |
| 06 | embed | ~181 | `go:embed` for `.saker` directory | Yes |
| 07 | multimodel | ~130 | Tier-based model routing, cost optimization | Yes |
| 08 | askuserquestion | ~474 | AskUserQuestion with build-tag demos | Yes |
| 09 | task-system | ~56 | Task tracking with dependencies | Yes |
| 10 | hooks | ~85 | PreToolUse/PostToolUse shell hooks | Yes |
| 11 | reasoning | ~186 | DeepSeek-R1 reasoning_content passthrough | Yes* |
| 12 | multimodal | ~135 | Text + image content blocks | Yes |
| 13 | govm-sandbox | focused | Readonly/readwrite sandbox mounts | No |
| 14 | artifact-pipeline | ~65 | Artifact-first multimodal pipeline | Yes |
| 15 | resumable-review | ~130 | Checkpointed human review with resume | Yes |
| 16 | timeline | ~100 | Inspect execution timeline events | Yes |
| 17 | pipeline-cli | ~180 | Declarative pipeline, cache, timeline, lineage DAG | No |
| 18 | video-understanding | ~278 | Video content analysis with aigo adapters | Yes |
| 19 | video-stream | ~127 | Streaming video generation pipeline | Yes |
| 20 | realtime-video | ~170 | Real-time video frame processing | Yes |

\* Example 11 uses OpenAI API key + DeepSeek base URL.

## 01-basic — minimal entry
- Purpose: fastest way to see the SDK loop in action with one request/response.
- Run:
```bash
source .env
go run ./examples/01-basic
```

## 02-cli — interactive REPL
- Key features: interactive prompt, per-session history, optional `.saker/settings.json` load.
- Run:
```bash
source .env
go run ./examples/02-cli --session-id demo --settings-path .saker/settings.json
```

## 03-http — REST + SSE
- Key features: `/health`, `/v1/run` (blocking), `/v1/run/stream` (SSE, 15s heartbeat); defaults to `:8080`. Fully thread-safe runtime handles concurrent requests automatically.
- Run:
```bash
source .env
go run ./examples/03-http
```

## 04-advanced — full integration
- Key features: end-to-end pipeline with middleware chain, hooks, MCP client, sandbox controls, skills, subagents, streaming output.
- Run:
```bash
source .env
go run ./examples/04-advanced --prompt "安全巡检" --enable-mcp=false
```

## 05-custom-tools — custom tool registration
- Key features: selective built-in tools (`EnabledBuiltinTools`), custom tool implementation (`CustomTools`), demonstrates tool filtering and registration.
- Run:
```bash
source .env
go run ./examples/05-custom-tools
```
- See [05-custom-tools/README.md](05-custom-tools/README.md) for detailed usage and custom tool implementation guide.

## 06-embed — embedded filesystem
- Key features: `EmbedFS` for embedding `.saker` directory into the binary, priority resolution between embedded and on-disk configs.
- Run:
```bash
source .env
go run ./examples/06-embed
```

## 07-multimodel — multi-model support
- Key features: model pool configuration, tier-based model routing (low/mid/high), subagent-model mapping, cost optimization.
- Run:
```bash
source .env
go run ./examples/07-multimodel
```
- See [07-multimodel/README.md](07-multimodel/README.md) for configuration examples and best practices.

## 08-askuserquestion — AskUserQuestion tool
- Key features: three demo modes selected by build tags.
- Run:
```bash
source .env
(cd examples/08-askuserquestion && go run .)                 # full agent scenarios
(cd examples/08-askuserquestion && go run -tags demo_llm .)  # LLM integration test
(cd examples/08-askuserquestion && go run -tags demo_simple .) # tool-only test (no API key needed)
```
- See [08-askuserquestion/README.md](08-askuserquestion/README.md) for detailed usage and implementation patterns.

## 09-task-system — task tracking
- Key features: task creation, dependency management, status tracking via built-in task tools.
- Run:
```bash
source .env
go run ./examples/09-task-system
```

## 10-hooks — hooks system
- Key features: `PreToolUse`/`PostToolUse` shell hooks, async execution, once-per-session dedup.
- Run:
```bash
source .env
go run ./examples/10-hooks
```

## 11-reasoning — reasoning models
- Key features: `reasoning_content` passthrough for thinking models (DeepSeek-R1), streaming support, multi-turn conversations.
- Run:
```bash
export OPENAI_API_KEY=your-key
export OPENAI_BASE_URL=https://api.deepseek.com/v1
go run ./examples/11-reasoning
```

## 12-multimodal — multimodal content
- Key features: text + image content blocks (base64 and URL), `ContentBlocks` in `api.Request`.
- Run:
```bash
source .env
go run ./examples/12-multimodal
```

## 13-govm-sandbox — standalone govm sandbox demo
- Key features: readonly `/inputs`, readwrite `/shared`, automatic session workspace under `workspace/<session-id>`, host-side file verification.
- Run:
```bash
go run ./examples/13-govm-sandbox
```
- See [13-govm-sandbox/README.md](13-govm-sandbox/README.md) for expected output and generated files.

## 14-artifact-pipeline — multimodal artifacts
- Key features: pipeline-backed execution, artifact refs with kind/source metadata, lineage edges tracked across steps.
- Run:
```bash
source .env
go run ./examples/14-artifact-pipeline
```

## 15-resumable-review — checkpoint and resume
- Key features: checkpoint creation mid-pipeline, resume from checkpoint ID, human-in-the-loop gate pattern.
- Run:
```bash
source .env
go run ./examples/15-resumable-review
```

## 16-timeline — execution timeline
- Key features: structured timeline with 10 event kinds (tool_call, tool_result, cache_hit/miss, latency_snapshot, input/generated artifact, checkpoint, token_snapshot).
- Run:
```bash
source .env
go run ./examples/16-timeline
```

## 17-pipeline-cli — pipeline CLI demo
- Key features: declarative pipeline from JSON file, fan-out with bounded concurrency, timeline event trace, lineage DAG export (Graphviz DOT format). **No API key required** — all tools are local stubs.
- Run:
```bash
# Basic run
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json

# With timeline events
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json --timeline

# With lineage graph (DOT format)
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json --lineage dot

# All features
go run ./examples/17-pipeline-cli --pipeline examples/17-pipeline-cli/pipeline.json --timeline --lineage dot

# Via Makefile
make demo-pipeline
```
- See [17-pipeline-cli/README.md](17-pipeline-cli/README.md) for detailed usage, pipeline JSON schema, and expected output.

## 18-video-understanding — video content analysis
- Key features: video file analysis via aigo adapters, scene detection, transcript extraction, structured content summary.
- Run:
```bash
source .env
go run ./examples/18-video-understanding
```

## 19-video-stream — streaming video generation
- Key features: streaming video generation pipeline, progress callbacks, artifact output tracking.
- Run:
```bash
source .env
go run ./examples/19-video-stream
```

## 20-realtime-video — realtime frame processing
- Key features: real-time video frame processing, frame-by-frame analysis, live output streaming.
- Run:
```bash
source .env
go run ./examples/20-realtime-video
```
