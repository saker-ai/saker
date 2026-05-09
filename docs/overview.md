# Project Overview

Saker is organized around one production path: a user gives the agent a goal,
the runtime coordinates tools and models, and the web surfaces expose the work
as project state, media assets, and editable outputs.

## Runtime

The Go runtime lives in `pkg/` and is exposed through `cmd/saker`.

Important packages:

- `pkg/api`: high-level runtime API used by the CLI, examples, and tests.
- `pkg/model`: model provider adapters, routing, failover, and model metadata.
- `pkg/tool`: builtin tools and tool registration.
- `pkg/runtime`: skills, commands, subagents, tasks, cache, and checkpoints.
- `pkg/server`: embedded web server and API endpoints.
- `pkg/canvas`, `pkg/artifact`, `pkg/media`: creative project and media layers.
- `pkg/sandbox`: host, Landlock, gVisor, Docker, and govm-related backends.

## Frontends

- `web/` is the main workspace and development UI.
- `web-editor-next/` is the browser editor exported as static files and mounted
  by the Go server at `/editor/`.

The production server build copies `web/out` into `cmd/saker/frontend/dist` and
`web-editor-next/out` into `cmd/saker/editor/dist` before compiling the Go binary.

## Generated Files

Generated frontend bundles, node dependencies, local runtime state, binaries,
coverage files, and media outputs are ignored by git. Source files, examples,
tests, deployment manifests, and stable docs should remain versioned.
