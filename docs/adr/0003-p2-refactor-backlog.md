# ADR 3: P2 Refactor Backlog (Deferred from "全部优化" Sweep)

## Status: Accepted (B1 ✅ shipped 2026-05; B2/B3/B4/B5/B6 still deferred)

> **2026-05 update:** B1 (HTTP handler `context.Background` anti-pattern)
> shipped — see `pkg/server/handler_config.go` and `handler_media.go`
> (commit landed alongside R2 metrics work). The remaining file-count
> figures below were re-checked against current main and updated where
> drift was significant. B2/B3 remain deferred; B4/B5/B6 unchanged.

## Context

The "全部优化" sweep (P0 security → P1 quick wins → P2 structural
refactor → P3 selected items) completed P0, P1, and a subset of P3 in
one session. The originally-scoped P2 items are documented here for
future scheduling rather than rushed mid-session.

The P2 audit was based on partly-stale numbers. Reconciliation against
current code showed:

- `pkg/provider` (2 files), `pkg/profile` (2 files), `pkg/persona` (12
  files) all exist but are small and focused — no consolidation needed.
  The earlier audit's "merge these" recommendation was based on a
  misread of the codebase and was dropped.
- `pkg/server` has 98 files (60 non-test) but is *already* split by
  responsibility (`auth_*`, `apps_rest_*`, `handler_*`, `canvas_*`,
  `cron_*`); the god-package label was overstated.
- `pkg/api` is a true god-package: 124 files (49 non-test) including
  `options.go` (966 LOC), `compact.go` (816 LOC), `runtime_helpers.go`
  (641 LOC — already partially carved out into `runtime_helpers_skills.go`,
  `runtime_helpers_commands.go`, `runtime_helpers_tools.go`).
- Giant non-test files >900 LOC, confirmed:
  - `pkg/eval/terminalbench/runner.go` — 1222
  - `pkg/tool/builtin/bash.go` — 1096
  - `pkg/runtime/skills/loader.go` — 999
  - `pkg/clikit/tui/app.go` — 974
  - `pkg/api/options.go` — 966
  - `pkg/tool/builtin/webfetch.go` — 944
  - `pkg/tool/registry.go` — 912
- `context.Background()` non-test sites: 70 (matches the original
  audit's 69; ~50 are defensive `if ctx == nil` guards or boot/lifecycle
  code; ~20 are HTTP-handler reach-around anti-patterns).

## Decision

The following P2 items are **deferred** with documented scope, risk,
and order-of-operations. None should be rushed.

### Backlog items (priority order)

#### B1. HTTP handler `context.Background()` anti-pattern ✅ DONE (2026-05)

The two ADR-flagged true anti-patterns shipped:

- `pkg/server/handler_media.go:cacheArtifactMedia` now takes a
  `ctx context.Context` parameter; `executeTurnWithBlocks` passes the
  per-request ctx so the 2-minute timeout chains off the request scope.
- `pkg/server/handler_config.go:handleModelSwitch` now takes
  `ctx context.Context`; the JSON-RPC handler dispatch was switched from
  `adaptNone` to `adaptCtx` for this method.
- `cacheArtifactAsync` retained `context.Background()` deliberately
  (background goroutine outlives the request) and is documented inline.

The other ~15 `context.Background()` sites in `pkg/server` were
triaged and classified: most are intentional background goroutines
(cron, GC, lifecycle) or boot/lifecycle paths and stay as-is. B6 below
adds the lint to prevent regression.

#### B2. Split `pkg/api/options.go` (L / 2-3 days)

966 LOC; mixes 7 concerns:

- L35–80: enum/const types (EntryPoint, ModelTier, OutputSchemaMode)
- L80–116: context structs (CLIContext, CIContext, PlatformContext, ModeContext)
- L117–147: SandboxOptions + permissions
- L148–180: Skill/Command/Subagent registrations + ModelFactory
- L182–362: `Options` struct + `DefaultOptions`
- L363–449: Result/Request/Response/SkillExecution/CommandExecution
- L451–966: `WithXxx` functional options + normalize/validate/freeze

**Suggested split**:
- `options_types_context.go` — context structs + entry/tier/mode enums
- `options_sandbox.go` — sandbox/permission types
- `options_registration.go` — skill/command/subagent/model-factory
- `options.go` — keep `Options` struct + defaults only
- `request_response.go` — Request/Result/Response/Execution
- `options_with.go` — all `WithXxx` functional options
- `options_normalize.go` — normalize/validate/freeze helpers

**Risk**: Medium. 32 KB file with many imports; test coverage in
`agent_test.go` (42 KB) makes regression detection feasible. Keep
public API stable — pure file-split refactor.

#### B3. Split other giant files (M / 2 days, parallelizable)

- `pkg/api/compact.go` (816) — splits naturally into compact-detect,
  compact-execute, compact-restore (the `compact_restore.go` already
  carved out a slice).
- `pkg/tool/builtin/bash.go` (1096) — split into bash-exec, bash-stream,
  bash-validate, bash-helpers.
- `pkg/tool/registry.go` (912) — registry vs schema-validation vs
  permission-resolution.
- `pkg/runtime/skills/loader.go` (999) — frontmatter-parse vs
  resolution vs caching.
- `pkg/clikit/tui/app.go` (974) — model vs view vs update (Bubbletea Elm
  pattern).

Skip `pkg/eval/terminalbench/runner.go` (1222) — eval tooling, not main
code path; not worth the churn.

#### B4. Build-tag SKU split (M / 2 days)

Introduce build tags so deployments can opt into lean binaries:

| Tag | What it gates |
| --- | --- |
| `embed_frontend` | embedded `web/out` and `web-editor-next/out` assets |
| `im` | gateway code in `pkg/im/*` (Telegram/Lark/Discord/Slack/DingTalk) |
| `otel` | OpenTelemetry SDK (already exists per `pkg/api/otel.go` notes) |
| `eval` | `pkg/eval/terminalbench/*` (not needed in production) |

**Risk**: Medium. Requires moving asset embedding behind tag, ensuring
default-build still works with placeholder, updating Makefile and CI
matrix to build at least 2 SKUs (default + lean).

**Win**: Cuts binary size meaningfully for headless CLI users who don't
need the Web frontend (~30 MB embedded assets).

#### B5. `tool/runtime` decoupling (M / 1-2 days)

Some `pkg/tool/builtin/*` files import `pkg/runtime/*` types directly.
Define minimal interfaces in `pkg/tool` so builtins depend on
abstractions, not concrete runtime types. Improves testability of tool
implementations in isolation.

**Risk**: Low if done as interface-extraction without behavior change.

#### B6. Full `context.Background()` audit + lint (S / 0.5 day)

After B1, write a `go vet`-style lint or simple AST grep that flags any
new `context.Background()` outside an allow-listed file (init code,
boot, OTel, top-level CLI). Prevents regression.

### Total estimate

~9-12 engineer-days, parallelizable into 3 streams (B1+B6 / B2+B3 /
B4+B5). Realistic single-developer wall-clock: 2-3 weeks.

## Why not now

- Mid-session, can't safely rewrite ~10 KLOC across god-package without
  full review pass.
- Lacks separate verifier review (this session has no second reviewer
  in scope).
- P0/P1 work + observability/license/govulncheck items are higher
  marginal ROI right now.

## Re-entry checklist

When picking this up:

1. Re-run `find pkg -name '*.go' -not -name '*_test.go' | xargs wc -l |
   sort -rn | head -20` to confirm the file sizes haven't changed.
2. Re-grep `context\.Background\(\)` non-test count to verify B1 scope.
3. Verify `pkg/im`, `pkg/eval/terminalbench` directories still exist
   before scoping B4.
4. Check whether `WithOTEL` already implements the build-tag pattern
   (`pkg/api/otel_config.go` says so) — copy that pattern for `im`.
