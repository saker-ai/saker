# Saker

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white" alt="Go 1.26+">
  <img src="https://img.shields.io/badge/Node.js-22+-339933?logo=nodedotjs&logoColor=white" alt="Node.js 22+">
  <img src="https://img.shields.io/badge/License-SKL--1.0-blue" alt="License: SKL-1.0">
  <br>
  <a href="https://github.com/cinience/saker/actions/workflows/ci.yml"><img src="https://github.com/cinience/saker/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/cinience/saker/actions/workflows/codeql.yml"><img src="https://github.com/cinience/saker/actions/workflows/codeql.yml/badge.svg" alt="CodeQL"></a>
  <a href="https://goreportcard.com/report/github.com/cinience/saker"><img src="https://goreportcard.com/badge/github.com/cinience/saker" alt="Go Report Card"></a>
  <a href="https://codecov.io/gh/cinience/saker"><img src="https://codecov.io/gh/cinience/saker/branch/main/graph/badge.svg" alt="Coverage"></a>
</p>

<p align="center">
  An agent runtime with a web workspace and browser video editor —<br>
  prompt, generate, review, and automate creative work in one binary.
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> •
  <a href="#features">Features</a> •
  <a href="#documentation">Docs</a> •
  <a href="#development">Development</a> •
  <a href="#license">License</a> •
  <a href="README_zh.md">中文</a>
</p>

---

## What is Saker

Saker is a source-available agent runtime. It combines a Go backend, a Next.js web workspace, and a browser-based video editor into a single binary that covers the full creative workflow — from writing prompts and generating media to reviewing output and automating repetitive steps.

```
┌──────────────┐     ┌──────────────┐     ┌──────────────────┐
│  Prompting &  │ ──▶ │    Media     │ ──▶ │   Review &       │
│   Planning    │     │  Generation  │     │   Automation     │
└──────────────┘     └──────────────┘     └──────────────────┘
        │                     │                     │
        ▼                     ▼                     ▼
   ┌──────────────────────────────────────────────────────────┐
   │                    Saker Runtime                         │
   │  ┌───────────┐  ┌───────────┐  ┌─────────────────────┐  │
   │  │ Go Backend│  │  Web UI   │  │ Browser Video Editor│  │
   │  └───────────┘  └───────────┘  └─────────────────────┘  │
   └──────────────────────────────────────────────────────────┘
```

## Why Saker

| Problem | Typical approach | Saker |
|---------|-----------------|-------|
| Creative workflow split across tools | Separate backends for prompting, media gen, and editing | Single binary with embedded web UI and editor — no cross-service glue |
| Sandbox too insecure or too restrictive | Docker-only or host-only | Five backends (host / landlock / gVisor / Docker / govm), degrades to what the host supports |
| Vendor lock-in for tools and models | Tight coupling to one API | Multi-provider with failover and routing; 37+ tools, swappable at runtime |
| Hard to deploy for remote multi-tenant use | Local CLI only | Built-in auth (OAuth / LDAP / Bearer), CSRF, CORS, SSRF protection, path traversal hardening |
| Observability bolted on later | Add OTel after the fact | Prometheus metrics, structured slog, request IDs wired through the stack |

## Quick Start

### Prerequisites

- Go 1.26 or later
- Node.js 22 or later
- pnpm (the repo uses a pnpm workspace for `web/` and `web-editor-next/`)
- Docker (optional — used by sandbox backends and e2e tests)

### Build and run

```bash
# Clone the repository
git clone https://github.com/cinience/saker.git
cd saker

# Install frontend dependencies
pnpm install

# Build frontends, embed them, and start the server
make run
```

The server listens on `http://localhost:10112`.

### CLI usage

```bash
# Build the CLI binary
make saker

# Set your API key
export ANTHROPIC_API_KEY=sk-ant-...

# Run a one-shot prompt
./bin/saker --print "Draft a 30-second product video concept"
```

### Development mode

```bash
# Run frontend dev servers separately
make web-dev          # http://localhost:10111
make web-editor-dev   # Editor dev server
```

## Features

### Agent runtime

| Feature | Description |
|---------|-------------|
| Core loop | Configurable iteration limit, timeout, and stop-reason classification |
| Budget guard | Aborts when cumulative cost or token count exceeds a limit |
| Loop detection | Detects repeated identical tool calls and stops; optional self-correction prompt |
| SSE streaming | Anthropic-compatible SSE protocol with agent-specific event extensions |
| Session history | In-memory ring buffer (default 1000 turns, configurable) |
| Context compaction | Prompt summarization and history truncation (compact / microcompact) |
| Profile isolation | Named profiles for settings, memory, and history separation |

### Model routing

| Feature | Description |
|---------|-------------|
| Providers | Anthropic, OpenAI, DashScope |
| Failover | Multi-model fallback with exponential backoff and stream buffering |
| Smart routing | Prompt-complexity-based, cost-aware model selection |
| Rate-limit tracking | Per-provider rate-limit header capture; HTTP transport wrapper |
| Prompt caching | System and recent-message prompt caching |

### Tools (37 built-in)

<details>
<summary>Expand to see tool list</summary>

| Category | Tools |
|----------|-------|
| File | Read, Write, Edit, Glob, Grep, ImageRead |
| Shell | Bash, BashOutput, BashStatus |
| Web | WebFetch, WebSearch, Webhook (SSRF-safe) |
| Interaction | AskUserQuestion, Skill, SlashCommand |
| Memory | MemorySave, MemoryRead |
| Canvas | CanvasGetNode, CanvasListNodes, CanvasTableWrite |
| Tasks | TaskCreate, TaskGet, TaskList, TaskUpdate, KillTask, TodoWrite |
| Video & Media | AnalyzeVideo, VideoSampler, VideoSummarizer, FrameAnalyzer, MediaIndex, MediaSearch |
| Stream | StreamCapture, StreamMonitor |
| Browser | Browser, Aigo (YAML-driven) |

</details>

### Sandbox & security

| Feature | Description |
|---------|-------------|
| Five backends | Host, Landlock (LSM), gVisor (runsc), Docker (network disabled by default), GoVM (lightweight VM) |
| SSRF protection | Blocks localhost, private IP ranges, and metadata endpoints; safe-close on DNS errors |
| Leak detection | Regex-based secret scanning with severity levels, masking, and cleanup |
| Permission matrix | Per-tool rules from permissions.json (allow / deny / ask) |

### Canvas & media

- Canvas document: nodes, edges (flow / reference / context), viewport in JSON
- Canvas executor: topological DAG traversal, dispatching generation nodes to the runtime
- 40+ node types: Agent, AI, Audio, Composition, Export, ImageGen, LLM, Mask, Prompt, VideoGen, VoiceGen, and more
- Media indexing: searchable index with keyframes and Chromem vector embeddings
- Video analysis: frame sampling, summarization, content description

### Video editor (browser)

| Feature | Description |
|---------|-------------|
| Timeline | Multi-track layout with audio, video, text, and effect tracks |
| Animation | Keyframe-based with Bezier curves and interpolation |
| Effects | Registry, components, parameter channel animation |
| Subtitles | ASS / SRT parsing, building, and insertion |
| Transcription | LLM-based audio transcription with diagnostics |
| Preview & guides | Render overlay, zoom, grid, and snap |
| WASM processing | Browser-side media rendering via WebAssembly |
| Undo / Redo | Command pattern with clipboard support |

### IM gateway

Saker can bridge to instant-messaging platforms so users interact with the agent through chat apps. Supported platforms:

Telegram, Discord, Feishu, Slack, DingTalk, WeCom, QQ, QQ Bot, LINE, WeChat

```bash
# Start the IM bridge for a single platform
./bin/saker --gateway telegram --gateway-token "your-bot-token"

# Or use a config file for multi-platform setups
./bin/saker --gateway-config gateway.toml
```

Channel credentials can also be managed through the TUI (`im_config` tool) or the Web UI settings panel.

## Architecture

```
                          ┌──────────────────────────────────────┐
                          │            Entry Points              │
                          │                                      │
                          │  ┌──────────┐  ┌──────────┐         │
                          │  │   CLI    │  │  Server  │         │
                          │  │ (TUI /   │  │ (HTTP /  │         │
                          │  │  REPL /  │  │  WS /    │         │
                          │  │  print)  │  │  SSE)    │         │
                          │  └────┬─────┘  └────┬─────┘         │
                          │       │              │               │
                          │       │  ┌───────────┘               │
                          │       │  │                           │
                          │       ▼  ▼                           │
                          │  ┌──────────────┐                    │
                          │  │  IM Gateway  │                    │
                          │  │ (Telegram,   │                    │
                          │  │  Discord,    │                    │
                          │  │  Feishu...)  │                    │
                          │  └──────┬───────┘                    │
                          └─────────┼────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                          pkg/api (Runtime API)                          │
│                                                                         │
│   Request → Agent Loop → Response / Stream                              │
│                                                                         │
│   ┌───────────────┐  ┌───────────────┐  ┌──────────────────────────┐   │
│   │  Budget Guard │  │ Loop Detector │  │  Context Compaction      │   │
│   │  (cost/token) │  │ (repeat call) │  │  (compact / microcompact)│   │
│   └───────────────┘  └───────────────┘  └──────────────────────────┘   │
│                                                                         │
└──────────────────────────────────┬──────────────────────────────────────┘
                                   │
         ┌─────────────┬───────────┼───────────┬─────────────┐
         ▼             ▼           ▼           ▼             ▼
┌─────────────┐ ┌───────────┐ ┌──────────┐ ┌──────────┐ ┌───────────┐
│   pkg/model │ │ pkg/tool  │ │  pkg/    │ │  pkg/    │ │  pkg/     │
│             │ │           │ │ runtime  │ │ security │ │ sandbox   │
│ • Anthropic │ │ 37+ built │ │          │ │          │ │           │
│ • OpenAI    │ │ -in tools │ │ • skills │ │ • SSRF   │ │ • host    │
│ • DashScope │ │ • file    │ │ • sub-   │ │ • leak   │ │ • landlock│
│ • failover  │ │ • shell   │ │   agents │ │   detect │ │ • gVisor  │
│ • routing   │ │ • web     │ │ • tasks  │ │ • permis-│ │ • Docker  │
│ • rate      │ │ • canvas  │ │ • cache  │ │   sions  │ │ • GoVM    │
│   limit     │ │ • media   │ │ • MCP    │ │ • auth   │ │           │
│             │ │ • browser │ │ • hooks  │ │          │ │           │
└──────┬──────┘ └───────────┘ └──────────┘ └──────────┘ └───────────┘
       │
       ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                       External Services                                  │
│                                                                          │
│  ┌─────────────────────┐   ┌─────────────────────┐                       │
│  │   LLM Providers     │   │   aigo Multimodal   │                       │
│  │                     │   │                     │                       │
│  │  Anthropic API      │   │  Image generation   │                       │
│  │  OpenAI API         │   │  Video generation   │                       │
│  │  DashScope API      │   │  TTS / STT          │                       │
│  │                     │   │  Media analysis     │                       │
│  └─────────────────────┘   └─────────────────────┘                       │
│                                                                          │
│  ┌──────────────────────────────────────────────┐                        │
│  │              IM Platforms                    │                        │
│  │                                              │                        │
│  │  Telegram • Discord • Feishu • Slack         │                        │
│  │  DingTalk • WeCom • QQ • LINE • WeChat       │                        │
│  └──────────────────────────────────────────────┘                        │
└──────────────────────────────────────────────────────────────────────────┘

                          ┌────────────────────┐
                          │   Creative Layer   │
                          │                    │
                          │  pkg/canvas        │
                          │  • DAG executor    │
                          │  • 40+ node types  │
                          │                    │
                          │  pkg/artifact      │
                          │  • lineage tracking│
                          │                    │
                          │  pkg/media         │
                          │  • index / search  │
                          │  • transcribe      │
                          └────────┬───────────┘
                                   │
                                   ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                       Web Frontend (embedded)                            │
│                                                                          │
│  ┌───────────────────────────┐   ┌──────────────────────────────────┐   │
│  │   web (Next.js)           │   │   web-editor-next (OpenCut)      │   │
│  │                           │   │                                  │   │
│  │  • Chat workspace         │   │  • Multi-track timeline          │   │
│  │  • Canvas DAG view        │   │  • Keyframe animation            │   │
│  │  • Project management     │   │  • Effects / subtitles           │   │
│  │  • Settings panels        │   │  • WASM rendering                │   │
│  │  • Skill plaza            │   │  • Undo / redo                   │   │
│  └───────────────────────────┘   └──────────────────────────────────┘   │
│                                                                          │
│                          Served by pkg/server                            │
│                          (REST • WebSocket • SSE • Auth • Cron)          │
└──────────────────────────────────────────────────────────────────────────┘

                          ┌────────────────────┐
                          │  Data & Storage    │
                          │                    │
                          │  pkg/sessiondb     │
                          │  pkg/memory        │
                          │  pkg/pipeline      │
                          │  pkg/config        │
                          │  pkg/storage       │
                          │  pkg/project       │
                          └────────────────────┘
```

## Repository structure

```
saker/
├── cmd/                 # CLI, embedded web server, desktop entry point
├── pkg/                 # Go runtime, tools, server, model providers, media, sandbox
├── web/                 # Main Next.js web workspace
├── web-editor-next/     # Browser video editor, mounted at /editor/
├── examples/            # SDK, CLI, HTTP, hooks, multi-model, and pipeline examples
├── test/                # Integration and pipeline tests
├── e2e/                 # Docker-based end-to-end test suites
├── eval/                # Evaluation harness
├── skills/              # Built-in skills
└── docs/                # Project documentation
```

## Documentation

| Document | Description |
|----------|-------------|
| [Overview](docs/overview.md) | System architecture and design |
| [Development guide](docs/development.md) | Local development and contribution workflow |
| [Configuration](docs/configuration.md) | Configuration options |
| [Deployment](docs/deployment.md) | Production deployment |
| [Security policy](SECURITY.md) | Reporting vulnerabilities |
| [Security model](docs/security.md) | Security architecture |
| [API reference](docs/api-reference.md) | REST API documentation |
| [Third-party notices](docs/third-party-notices.md) | Dependency license inventory |
| [Roadmap](ROADMAP.md) | Planned features and direction |
| [Changelog](CHANGELOG.md) | Version history |

## Development

### Common commands

```bash
# Testing
make test-short       # Fast subset
make test-unit        # Unit tests
make test-pipeline    # Pipeline tests

# Dev servers
make server-dev       # Development server
make server           # Production server

# Full build
make build            # Production build
```

### Frontend checks

```bash
pnpm --filter ./web run test
pnpm --filter ./web run build
pnpm --filter ./web-editor-next run build
```

## Configuration

Saker keeps project-local runtime state under `.saker/` (git-ignored).

### Environment variables

```bash
ANTHROPIC_API_KEY=      # Anthropic API key
OPENAI_API_KEY=         # OpenAI API key
DASHSCOPE_API_KEY=      # DashScope API key
SAKER_MODEL=            # Default model, e.g. claude-sonnet-4-5-20250929
```

### Server authentication

```bash
# Set credentials for the web UI
./bin/saker --auth-user admin --auth-pass '<password>'
./bin/saker --server
```

## Contributing

Issues and pull requests are welcome. Please run the relevant tests and builds before submitting, and include setup notes in the PR description. See [CONTRIBUTING.md](CONTRIBUTING.md) for the full guide.

## License

Saker is licensed under the **Saker Source License Version 1.0 (SKL-1.0)** — a source-available license based on Apache 2.0 with additional terms.

### Summary

| Scenario | Terms |
|----------|-------|
| Small teams & individuals | Free for production use if annual revenue ≤ ¥1,000,000 **and** registered users ≤ 100 |
| Commercial license required | Annual revenue > ¥1,000,000 **or** registered users > 100 |
| Non-production use | Always free — evaluation, testing, development, learning, and research |
| Derivative works | Must display "Powered by Saker.cc" in the UI and documentation |

For commercial licensing: [cinience@hotmail.com](mailto:cinience@hotmail.com)

**Upstream notices**: maintained in the [NOTICE](NOTICE) file. Dependency licenses are listed in [docs/third-party-notices.md](docs/third-party-notices.md).

**Browser editor**: code under `web-editor-next/` is derived from OpenCut (MIT). Asset attributions are in `web-editor-next/ASSET_LICENSES.md`.

**Remote dependencies**: the `godeps` packages (aigo, goim, govm) are remote Go modules resolved via `go.mod`, not local directories.

---

<p align="center">
  Built by <a href="https://saker.cc">Saker.cc</a>
</p>
