# Changelog

All notable changes to Saker will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Go agent runtime with CLI, streaming runs, skills, subagents, memory, hooks, model routing, MCP, and sandbox backends
- Next.js creative workspace web UI for chat, canvas-style creative work, assets, and project state
- Browser video editor mounted at `/editor/` in the embedded server (OpenCut-derived)
- Multimodal tool adapters through `aigo` (image, video, audio, speech, transcription, media intelligence)
- Go SDK, HTTP/server mode, and developer surfaces
- Docker and docker-compose deployment support
- 20 example programs covering basic, CLI, HTTP, custom tools, hooks, multimodal, sandbox, pipeline, and video use cases
- Evaluation harnesses and Terminal-Bench 2 integration
- Docker-based end-to-end test suites
- CI pipeline with lint, test, race detector, coverage, frontend build, and bundle-size checks
- Multi-platform release workflow (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64)
- Conventional Commits commit message convention
- golangci-lint v2 configuration
- SECURITY.md, CODE_OF_CONDUCT.md, and CONTRIBUTING.md