# ADR 4: R2 Observability and Refactor

## Status: Accepted (2026-05-09)

## Context

After the MVP and the P0/P1 sweep documented in ADR-0003, several gaps had
accumulated:

1. **No first-class metrics surface.** The server emitted structured logs
   only — no Prometheus endpoint, no per-route histograms, no model-call
   counters. Operators had to grep logs to compute basic SRE signals.
2. **No in-process profiler.** Diagnosing CPU/heap/goroutine issues in a
   running deployment required either restart-with-pprof or out-of-band
   trace capture.
3. **CLI timeout regression.** `cmd/saker` constructed
   `context.WithTimeout` without retaining the cancel func in the right
   scope, causing the timer to fire prematurely on long-running runs.
4. **Six files >800 LOC** still dragged maintainability — flagged in
   ADR-0003 as B3 but only partially addressed.
5. **No OpenAPI surface.** REST handlers had no machine-readable
   schema, blocking SDK generation and external integration.
6. **No automated dependency upgrades.** Dependabot / Renovate was not
   configured; security patches landed only when noticed manually.

The R2 round was scoped to close these gaps without touching public
runtime APIs.

## Decision

The following items shipped under R2:

### Prometheus metrics middleware

Added `pkg/metrics/` with two entry points:

- `metrics.go` — HTTP middleware emitting request count / latency
  histograms keyed by route and status code.
- `model_wrapper.go` — model-call counters and token-usage histograms
  wrapping the provider layer.

The `/metrics` endpoint is registered alongside the existing Gin router
and is gated by the same auth policy as other operational endpoints.

### pprof endpoints in debug mode

`/debug/pprof/*` is mounted via `gin.WrapH` when the server starts in
debug mode. Production builds keep the routes off by default, matching
the Go standard-library convention.

### CLI `context.WithTimeout` fix

Commit `9d2b5b9` ("Rename cmd/cli to cmd/saker, add CLI file-only
logging, fix `context.WithTimeout` bug") restored correct cancel-func
scoping in `cmd/saker`. The timer no longer fires before the wrapped
operation completes.

### Partial B3 split — `compact.go` and `registry.go`

`pkg/api/compact.go` (816 LOC) was carved into focused siblings:

- `compact_compactor.go`
- `compact_media.go`
- `compact_prompt.go`
- `compact_restore.go`
- `compact_session_memory.go`

`pkg/api/registry.go` was split along the same lines (registry vs.
schema-validation vs. permission-resolution boundaries called out in
ADR-0003 §B3). The remaining ten >800-LOC files were deferred to R3.

### Swagger / OpenAPI annotations

`swaggo`-style annotations were added to the public REST endpoints so
the OpenAPI spec can be generated as part of the build. This unblocks
external SDK generation and self-documenting API browsers.

### Dependabot

A Dependabot config was committed for Go modules and GitHub Actions.
Security patches and minor upgrades now arrive as PRs without manual
poll.

### Test coverage

Commit `bd7b05f` ("Add 247 tests for `pkg/media`, `pkg/version`,
`pkg/runtime/cache/checkpoint`") backfilled coverage on the
previously-thin packages so the file splits above land on a green
baseline.

## Consequences

### Positive

- Operators get Prometheus-native dashboards without log scraping.
- Live `/debug/pprof/*` shortens MTTR for CPU/heap incidents.
- CLI timeout no longer cuts long runs short.
- `pkg/api/compact_*` files are each well under the 800-LOC threshold,
  matching the modularity goal of ADR-0003 §B3.
- OpenAPI spec is generated from source — schema drift is now a build
  failure rather than a documentation chore.
- Dependabot keeps CVE exposure bounded with no human polling.

### Negative

- Ten of the originally-flagged sixteen large files remain >800 LOC and
  were rolled into R3 scope.
- `/metrics` and `/debug/pprof/*` widen the operational surface area —
  must stay behind auth in any externally-reachable deployment.
- Swagger annotations add boilerplate at handler sites; drift between
  annotations and behavior is now a possible class of bug.

### Pointers

- `pkg/metrics/` — Prometheus middleware and model wrapper
- `pkg/api/compact_*.go` — split-out compaction subsystem
- Commit `9d2b5b9` — CLI `context.WithTimeout` fix
- Commit `bd7b05f` — 247 new tests
- ADR-0003 §B3 — original split scope (R3 absorbs the remainder)
