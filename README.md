<div align="center">

# Saker

<p>
  <img src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white" alt="Go 1.26+">
  <img src="https://img.shields.io/badge/Node.js-22%2B-339933?logo=nodedotjs&logoColor=white" alt="Node.js 22+">
  <img src="https://img.shields.io/badge/License-SKL--1.0-c2410c" alt="License: SKL-1.0">
  <a href="https://github.com/cinience/saker/actions/workflows/ci.yml"><img src="https://github.com/cinience/saker/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/cinience/saker/actions/workflows/codeql.yml"><img src="https://github.com/cinience/saker/actions/workflows/codeql.yml/badge.svg" alt="CodeQL"></a>
  <a href="https://goreportcard.com/report/github.com/cinience/saker"><img src="https://goreportcard.com/badge/github.com/cinience/saker" alt="Go Report Card"></a>
  <a href="https://codecov.io/gh/cinience/saker"><img src="https://codecov.io/gh/cinience/saker/branch/main/graph/badge.svg" alt="Coverage"></a>
</p>

**A source-available agent runtime for creative work.**
Prompt, generate, review, and automate &mdash; all in a single binary.

<a href="#quick-start">Quick start</a> ·
<a href="#features">Features</a> ·
<a href="#architecture">Architecture</a> ·
<a href="#documentation">Docs</a> ·
<a href="README_zh.md">中文</a>

<br>

<img src="docs/images/workflow.svg" alt="Prompting → Media generation → Review &amp; automation" width="820">

</div>

---

## What is Saker

Saker fuses three things that creative teams usually run as separate stacks &mdash; an agent runtime, a Web workspace, and a browser-based video editor &mdash; into a single Go binary. It owns the full creative loop: write a prompt, plan the work, generate media, review it, and ship it through automation or messaging channels. Everything is local-first, embedded, and works the same way whether you launch it as a CLI, a TUI, an HTTP server, an IM bot, or a Wails desktop app.

## Why Saker

| Problem | Typical approach | Saker |
|---|---|---|
| Creative pipeline scattered across tools | Separate backends for prompting, generation, and editing | One binary that embeds the workspace, editor, runtime, and gateways |
| Sandbox is either insecure or impractical | Docker-only or host-only | Five backends &mdash; host, Landlock, gVisor, Docker, govm &mdash; selected per host |
| Vendor lock-in for models and tools | One provider, one tool table | Multi-provider with failover and routing; 33 builtin tools, MCP servers, remote tools |
| Hard to deploy multi-tenant on a server | Local CLI only | Built-in OAuth/LDAP/Bearer auth, CSRF, CORS, SSRF guards, path-traversal hardening, per-project scopes |
| Observability bolted on later | Wire OTel after the fact | Prometheus metrics, structured slog, OTel spans, request IDs through the stack out of the box |

## Quick start

### Prerequisites

- Go 1.26 or newer
- Node.js 22 or newer
- pnpm (the repo is a pnpm workspace covering `web/`, `web-editor-next/`, `packages/`)
- Docker (optional &mdash; required by Docker / govm sandbox backends and the e2e suite)

### Build and run

```bash
git clone https://github.com/cinience/saker.git
cd saker

pnpm install                      # frontend dependencies
make run                          # build frontends, embed, start server on :10112
```

Open `http://localhost:10112` for the workspace, `http://localhost:10112/editor/` for the video editor.

### CLI usage

```bash
make saker                        # build the CLI

export ANTHROPIC_API_KEY=sk-ant-...
./bin/saker --print "Draft a 30-second product video concept"
./bin/saker                       # interactive TUI
```

### Frontend dev mode

```bash
make web-dev                      # workspace at http://localhost:10111
make web-editor-dev               # editor dev server
```

## Features

### Agent runtime

| Capability | Notes |
|---|---|
| Core loop | Iteration cap, deadline, classified `StopReason` (`completed` / `max_iterations` / `max_budget` / `max_tokens` / `repeat_loop` / aborted variants / `model_error`) |
| Budget guard | Aborts on cumulative cost or token ceiling |
| Loop detection | Halts on identical repeated tool calls; optional self-correction |
| SSE streaming | Anthropic-compatible SSE with agent-specific event extensions |
| Session history | In-memory ring buffer (default 1000 turns, configurable) |
| Context compaction | `compact` and `microcompact` strategies, prompt summarisation, history trimming |
| Profiles | Named profiles isolate settings, memory, and history |
| Subagents | Forked sub-runtimes with optional git worktree, transcript streamed back |
| Checkpoints | Resumable session/run state via memory or file backend |

### Models

Backed by [Bifrost](https://github.com/maximhq/bifrost) (`pkg/model/bifrost_adapter.go`); saker `Provider` types wrap Bifrost as the SDK-level engine.

| Capability | Notes |
|---|---|
| Providers | 23+ via Bifrost: Anthropic, OpenAI, AWS Bedrock, Google Vertex, Azure OpenAI, Ollama, Cohere, Mistral, Groq, Gemini, XAI, DashScope (via OpenAI-compatible), Cerebras, Fireworks, OpenRouter, HuggingFace, Replicate, … |
| Auth | API key, AWS IAM (Bedrock), GCP service account or IAM role (Vertex), Azure API key or client_secret/tenant (Azure) |
| Failover | Bifrost SDK-level `Fallbacks` cross-provider routing; observer plugin emits per-request switch events |
| Prompt caching | System and recent-message ephemeral cache_control on Anthropic / Bedrock-Anthropic |
| Observability | Optional `ObservationSink` plugin captures per-request provider/model/usage/duration; OTel span attrs include cache & total tokens |

### Tools (33 builtin + memory + MCP)

<details>
<summary>Expand to see the registered builtin tools</summary>

| Group | Tools |
|---|---|
| `core_io` | `bash`, `file_read`, `file_write`, `file_edit`, `grep`, `glob` |
| `bash_mgmt` | `bash_output`, `bash_status`, `kill_task` |
| `task_mgmt` | `task` (subagent spawn), `task_create`, `task_list`, `task_get`, `task_update` |
| `web` | `web_fetch`, `web_search` |
| `media` | `image_read`, `video_sampler`, `stream_capture`, `stream_monitor`, `frame_analyzer`, `video_summarizer`, `analyze_video`, `media_index`, `media_search` |
| `interaction` | `ask_user_question`, `skill`, `slash_command` |
| `canvas` | `canvas_get_node`, `canvas_list_nodes`, `canvas_table_write` |
| `browser` | `browser` (chromedp), `webhook` (SSRF-safe) |
| (auto) | `memory_save`, `memory_read` (enabled when `MemoryDir` is set) |

Each runtime mode selects a curated subset via **mode presets**:

| Preset | Groups | Use case |
|---|---|---|
| `cli` | core_io, bash_mgmt, task_mgmt, web, media, interaction | Interactive terminal / TUI |
| `server_web` | core_io, bash_mgmt, task_mgmt, web, media, canvas, browser | Web workspace with UI |
| `server_api` | core_io, bash_mgmt, task_mgmt, web, media, interaction, canvas | API-only backend (no browser) |
| `ci` | core_io, bash_mgmt | CI pipelines (minimal) |

Override with `Options.ModePreset` or `--api-only` flag. Further filter with `Options.EnabledBuiltinTools` (whitelist) or `Options.DisallowedTools` (blacklist). MCP and remote tools register on top of the preset.

Source of truth: `pkg/api/tool_groups.go`, `pkg/api/runtime_tools_register.go`.

</details>

### Sandbox & security

| Capability | Notes |
|---|---|
| Five backends | `host`, `landlock` (LSM, helper process), `gvisor` (runsc, helper process), `docker` (network off by default), `govm` (microVM via `godeps/govm`) |
| Filesystem policy | Allow / deny lists with path mapping (`pkg/sandbox/pathmap`) and `O_NOFOLLOW` opens |
| SSRF guard | Blocks loopback, private ranges, link-local, metadata endpoints, plus DNS-rebinding safe close |
| Leak detection | Regex-based secret scanning with severity, masking, and cleanup |
| Permission matrix | Per-tool `allow / deny / ask` rules from `permissions.json`, runtime resolver, and approval prompts |
| Auth | Local credentials, OIDC, LDAP, Bearer tokens; per-project / per-user scope middleware |

### Canvas & media

- DAG document with typed nodes and edges (flow / reference / context)
- 40+ node types (Agent, AI, Audio, Composition, Export, ImageGen, LLM, Mask, Prompt, VideoGen, VoiceGen, …)
- Topological executor that dispatches generation nodes back into the agent runtime
- Media index with keyframes and `chromem-go` vector embeddings; full-text and semantic search
- Audio transcription, video summarisation, and frame-level analysis pipelines

### Browser video editor

| Capability | Notes |
|---|---|
| Timeline | Multi-track audio / video / text / effects |
| Animation | Keyframes with Bezier interpolation |
| Effects | Registry, per-effect components, parameter channel animation |
| Subtitles | ASS / SRT parse / build / insert |
| Transcription | LLM-driven audio transcription with diagnostics |
| Preview | Render overlay, zoom, grid, snap |
| WASM rendering | Browser-side media rendering via WebAssembly |
| History | Command-pattern undo / redo with clipboard support |

Derived from [OpenCut](https://github.com/OpenCut-app/OpenCut) (MIT). Asset attributions live in `web-editor-next/ASSET_LICENSES.md`.

### IM gateway

Saker can bridge to ten chat platforms so users interact with the agent through the apps they already use:

`telegram` · `feishu` · `discord` · `slack` · `dingtalk` · `wecom` · `qq` · `qqbot` · `line` · `weixin`

```bash
./bin/saker --gateway telegram --gateway-token "<bot-token>"
./bin/saker --gateway-config gateway.toml          # multi-platform
```

Channels can also be configured from the TUI (`im_config` tool) or the workspace settings panel.

## Architecture

<div align="center">
  <img src="docs/images/architecture.svg" alt="Saker architecture: surfaces → runtime → engines → creative + data → frontend + external" width="100%">
</div>

For per-package roles see [docs/architecture.md](docs/architecture.md).

### Data flow (one request)

1. **Surface** &mdash; CLI/TUI/HTTP/IM/ACP entry point parses input and resolves a profile.
2. **Runtime** &mdash; `pkg/api.Runtime` loads settings, builds the sandbox, registers builtin + MCP + remote tools, attaches persona / memory / sessiondb / skills / subagents / cache.
3. **Loop** &mdash; `pkg/agent.Agent.Run` iterates until a `StopReason` fires; budget, loop-detect, and compaction guard the loop.
4. **Model** &mdash; `pkg/model` provider wraps Bifrost; cross-provider failover is handled by Bifrost SDK-level `Fallbacks`. Calls are instrumented by `pkg/metrics`, the optional `ObservationSink` plugin, and (when built with `-tags otel`) by `pkg/api/otel.go`.
5. **Tool** &mdash; resolved permission, PreToolUse hook, dispatched to a builtin / MCP / remote tool. File-touching tools cross the `pkg/sandbox` boundary.
6. **Stream** &mdash; results flow back as `StreamEvent`s for SSE / WebSocket clients, the TUI waterfall, or the IM gateway.

## Repository structure

```
saker/
├── cmd/                  # CLI dispatcher (cmd/saker) and Wails desktop (cmd/desktop)
├── pkg/                  # Go runtime: api, agent, model, tool, runtime, server, sandbox, security,
│                         # canvas, pipeline, media, artifact, sessiondb, memory, persona, project,
│                         # storage, config, middleware, metrics, clikit, mcp, acp, im, skillhub …
├── web/                  # Next.js 16 web workspace (saker-web)
├── web-editor-next/      # Browser video editor derived from OpenCut (saker-web-editor)
├── packages/             # Shared TS workspace packages (editor-protocol)
├── examples/             # 20 numbered examples (01-basic … 20-realtime-video)
├── test/                 # Integration, pipeline, runtime, security suites
├── e2e/                  # Docker-based end-to-end suites
├── eval/                 # Eval framework (offline + LLM + Terminal-Bench)
├── docs/                 # Documentation, ADRs, diagrams (mermaid + rendered SVG)
├── bench/                # Benchmark baselines
└── scripts/              # Repo maintenance scripts
```

## Documentation

| Document | Description |
|---|---|
| [Overview](docs/overview.md) | High-level summary |
| [Architecture](docs/architecture.md) | Detailed mermaid architecture and request sequence |
| [Development guide](docs/development.md) | Local dev workflow, tests, conventions |
| [Configuration](docs/configuration.md) | Settings, profiles, env vars |
| [Deployment](docs/deployment.md) | Production deployment notes |
| [Security model](docs/security.md) | Threat model and defences |
| [Observability](docs/observability.md) | Metrics, logs, OTel |
| [Testing](docs/testing.md) | Test taxonomy and harness |
| [API reference](docs/api-reference.md) | REST / WS / SSE surface |
| [ADRs](docs/adr/) | Architecture decision records |
| [Security policy](SECURITY.md) | Reporting vulnerabilities |
| [Third-party notices](docs/third-party-notices.md) | Dependency licenses |
| [Roadmap](ROADMAP.md) | Planned work |
| [Changelog](CHANGELOG.md) | Release history |

## Development

```bash
make test-short        # quick subset, dev loop
make test-unit         # unit tests with race detector
make test-pipeline     # pipeline integration tests
make lint              # golangci-lint
make bench             # benchmarks → bench/baseline.txt

make server-dev        # Go-only dev server (no embedded frontend)
make server            # full build + embed + serve
make build             # composite production build (web + editor + binary)
make diagrams          # re-render docs/images/*.svg from docs/diagrams/*.mmd
```

Frontend checks:

```bash
pnpm --filter saker-web        run test
pnpm --filter saker-web        run build
pnpm --filter saker-web-editor run build
```

## Configuration

Project-local runtime state lives under `.saker/` (git-ignored).

```bash
ANTHROPIC_API_KEY=    # Anthropic
OPENAI_API_KEY=       # OpenAI
DASHSCOPE_API_KEY=    # DashScope (via OpenAI-compatible)
SAKER_MODEL=          # Default model, e.g. claude-sonnet-4-5-20250929

# Optional — Bifrost-backed providers
AWS_REGION=           # Bedrock (or AWS_DEFAULT_REGION); IAM role used when AWS_ACCESS_KEY_ID empty
GOOGLE_CLOUD_PROJECT= # Vertex; GOOGLE_CLOUD_REGION optional (defaults us-central1)
AZURE_OPENAI_ENDPOINT=  # Azure OpenAI; pair with AZURE_OPENAI_API_KEY or client_secret tuple
OLLAMA_BASE_URL=      # Ollama (defaults http://localhost:11434)
```

Server authentication:

```bash
./bin/saker --auth-user admin --auth-pass '<password>'
./bin/saker --server
```

## Contributing

Issues and pull requests are welcome. Run the relevant tests and builds before submitting and document setup steps in the PR description. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Saker is released under the **Saker Source License Version 1.0 (SKL-1.0)** &mdash; source-available, based on Apache 2.0 with additional terms.

| Scenario | Terms |
|---|---|
| Small teams & individuals | Free for production if annual revenue ≤ ¥1,000,000 **and** registered users ≤ 100 |
| Commercial license required | Annual revenue > ¥1,000,000 **or** registered users > 100 |
| Non-production use | Always free &mdash; evaluation, testing, development, learning, research |
| Derivative works | Must display "Powered by Saker.cc" in product UI and documentation |

Commercial licensing: [cinience@hotmail.com](mailto:cinience@hotmail.com).

- Upstream notices live in [NOTICE](NOTICE); dependency licenses in [docs/third-party-notices.md](docs/third-party-notices.md).
- Code under `web-editor-next/` is derived from OpenCut (MIT); asset credits in `web-editor-next/ASSET_LICENSES.md`.
- The `godeps/*` packages (`aigo`, `goim`, `govm`) are remote Go modules resolved through `go.mod`, not local directories.

---

<div align="center">
  Built by <a href="https://saker.cc">Saker.cc</a>
</div>
