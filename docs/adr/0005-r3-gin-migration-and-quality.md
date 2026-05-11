# ADR 5: R3 File Splits, Quality Linters, and Deferred Gin Migration

## Status: Accepted (2026-05-11)

## Context

After R2 (ADR-0004) shipped, several follow-ups remained:

1. **B3 file splits incomplete.** Ten files identified in ADR-0003 §B3
   were still >800 LOC, including `cmd/saker/main.go` (1503),
   `pkg/eval/terminalbench/runner.go` (1222), and
   `pkg/tool/builtin/bash.go` (1097).
2. **No fuzz tests.** Critical parsers (path cleanup, JSON-RPC, MCP
   schema, base64 detection, settings merge) had only example-based
   coverage.
3. **No benchmark suite.** Performance regressions in compact, registry,
   and trace had no automated detection.
4. **Permissive lint config.** `gosec`, `gocognit`, `prealloc`,
   `unconvert`, and `bodyclose` were not enabled — new code could land
   with shape regressions invisible to CI.
5. **Half-finished Gin migration.** ADR-0001 committed the framework
   move, but the router is the only fully-Gin layer. Seventy-one
   business handlers still use `net/http.HandlerFunc` wrapped with
   `gin.WrapH(http.HandlerFunc(...))`. The wrappers work but obscure
   the route-parameter ergonomics that motivated the migration.
6. **No distributed tracing.** Cross-service request correlation
   required ad-hoc log threading.
7. **One stale TODO.** `pkg/tool/registry_mcp.go` still called the
   deprecated `BuildSessionTransport` instead of `ConnectSession`.

R3 was scoped to close items 1–4, 6, 7 in one round and to explicitly
defer item 5 with a documented re-entry plan.

## Decision

### Complete the B3 file splits

Commit `b0a3434` ("Split 10 large files (>800 LOC) into focused sibling
files for R3-P0.1") finished the ADR-0003 §B3 work:

| File | Before | After |
| --- | --- | --- |
| `cmd/saker/main.go` | 1503 | 525 LOC |
| `pkg/eval/terminalbench/runner.go` | 1222 | split into 4 files |
| `pkg/tool/builtin/bash.go` | 1097 | split into 4 files |
| `pkg/runtime/skills/loader.go` | 999 | split into 5 files |
| `pkg/clikit/tui/app.go` | 974 | split into 5 files |
| `pkg/tool/builtin/webfetch.go` | 944 | split into 4 files |
| `pkg/tool/builtin/anthropic.go` | 828 | split into 4 files |
| `pkg/api/service.go` | 801 | split into 5 files |
| `pkg/server/skillhub_handler.go` | 791 | split into 4 files |
| `pkg/server/skills_import.go` | 788 | split into 4 files |

After R3 every non-generated, non-protobuf Go file in the repo is
under 800 LOC.

### Final TODO: `registry_mcp.go`

Migrated `pkg/tool/registry_mcp.go` from the deprecated
`BuildSessionTransport` to `ConnectSession`, removing the last
in-tree TODO marker.

### Stricter lint configuration

Enabled `gosec`, `gocognit` (cognitive-complexity threshold 50),
`prealloc`, `unconvert`, and `bodyclose` in the golangci-lint config.

The full repo currently surfaces 337 + 127 issues from the new linters
— these are pre-existing tech debt, not regressions introduced by R3.
Rather than block on a full sweep, R3 adopted **incremental enforcement**
via a new make target:

```
make lint-new
```

which runs `golangci-lint run --new-from-rev=origin/main`. New code
must pass the strict ruleset; legacy code is grandfathered until each
package gets its own cleanup pass.

### Fuzz tests for 5 critical paths

Added `Fuzz*` corpora and entry points for:

- path cleanup
- JSON-RPC envelope parsing
- MCP tool-schema validation
- base64-content detection
- settings merge

These run under `go test -fuzz=...` locally and as time-boxed jobs in
CI.

### Benchmark suite

Added `make bench` aggregating benchmarks across the `compact`,
`registry`, and `trace` packages — the three subsystems most likely to
regress under load.

### OpenTelemetry tracing

Integrated OpenTelemetry as opt-in:

- Activated by setting `OTEL_EXPORTER_OTLP_ENDPOINT`.
- HTTP layer instrumented via `otelgin` middleware.
- Zero overhead when the env var is unset (no exporter, no span
  creation past the middleware shortcut).

### Deferred: Gin handler migration

Seventy-one business handlers still use the
`gin.WrapH(http.HandlerFunc(...))` shim. R3 explicitly **does not**
migrate them, because:

- The shims work correctly — there is no behavioral defect today.
- The migration is mostly aesthetic for endpoints that don't read
  path parameters.
- The three REST dispatchers (`handleAppsREST`, `handleCanvasREST`,
  `handleAppsPublic`) are the real prize: replacing manual
  `strings.Split(strings.TrimPrefix(r.URL.Path, ...))` parsing with
  `c.Param(...)` is the ergonomic win ADR-0001 promised.
- Estimated cost: 71 handlers × ~4.5 hours = multi-session work.
  Squeezing it into R3 would either rush the dispatcher rewrite or
  leave R3 half-finished.

The migration is tracked as a separate dedicated session with a
4–6 hour budget targeting the three dispatchers first.

## Consequences

### Positive

- **All Go files now under 800 LOC** except generated/protobuf.
- **New code held to a higher quality bar** automatically via
  `make lint-new`; legacy backlog can be drained per-package without
  blocking forward progress.
- **Distributed tracing available** with zero runtime cost when
  disabled.
- **Critical parsers** now have fuzz coverage; performance-sensitive
  paths have a benchmark baseline.
- **No more in-tree TODO** in `pkg/tool/registry_mcp.go`.

### Negative

- **Lint backlog visible.** The 337 + 127 pre-existing issues now
  surface in `make lint` output and create noise during incremental
  review until packages are swept.
- **Gin migration debt persists.** Operators still see two HTTP
  handler styles in `pkg/server/*`. Path-parameter ergonomics in the
  three REST dispatchers continue to rely on string-split parsing.
- **OTel adds dependency surface** even when disabled; `otelgin`
  pulls a non-trivial transitive graph.

### Pointers

- Commit `b0a3434` — 10-file split-out
- `pkg/tool/registry_mcp.go` — `ConnectSession` migration
- `make lint-new` — incremental strict enforcement
- `make bench` — compact / registry / trace baselines
- `OTEL_EXPORTER_OTLP_ENDPOINT` — opt-in tracing
- ADR-0001 — original Gin migration decision
- ADR-0003 §B3 — file-split scope this ADR closes
- ADR-0004 — R2 round (compact / registry partial split)
