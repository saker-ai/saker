# Testing

## Test layouts

| Location | Purpose | Build tag |
| --- | --- | --- |
| `pkg/**/.*_test.go` | Unit tests, co-located with sources | — |
| `test/integration/...` | Integration tests | `integration` |
| `test/pipeline/...` | Pipeline-level tests | — |
| `e2e/` | Docker-based end-to-end tests | n/a — separate harness |
| `eval/` | Model/agent evaluation suites | — |

## Make targets

```bash
make test           # full suite, 2-min per-test timeout
make test-short     # -short flag, 60s timeout — dev loop
make test-unit      # -short -race ./pkg/... — fast race-checked subset
make test-race      # -race on full suite
make test-integration  # -tags integration ./test/integration/...
make test-pipeline  # pipeline tests
make coverage       # produces coverage.out + cover -func summary
make e2e-build      # build Docker images for E2E
make e2e-run        # run Docker E2E
```

## Coverage

`make coverage` writes `coverage.out` and prints `go tool cover -func`
output. To see per-package aggregation and lowest-covered targets:

```bash
make coverage
./scripts/coverage-summary.sh           # default: top-10 lowest
./scripts/coverage-summary.sh --top-low 20
```

### Baseline policy

We **track** coverage but do **not** enforce a hard CI threshold today.
Reasoning: the project is still in active feature work; a fixed gate
would either be set so low it's meaningless, or so high that legitimate
prototypes fail CI.

What we *do* enforce in CI (`.github/workflows/ci.yml`):

- All tests must pass (`go test -race ./...`).
- Codecov uploads the report so trends are visible per PR (no
  `fail_ci_if_error`, intentionally — Codecov outages should not block).

### When to add tests

| Situation | Required? |
| --- | --- |
| New public function in `pkg/api`, `pkg/server`, `pkg/security`, `pkg/sandbox` | Yes — table-driven |
| New tool builtin in `pkg/tool/builtin` | Yes — happy path + 1 failure mode |
| Bug fix | Yes — regression test for the exact bug |
| Internal refactor with no behavior change | Existing tests should cover it; if not, add before refactor |
| New CLI flag in `cmd/saker` | Smoke test through the CLI runner |

### Property/fuzz testing

Go 1.18+ fuzzing is available; we use it sparingly. Good candidates:

- `pkg/runtime/skills/loader.go` (frontmatter parser)
- `pkg/canvas/...` (DAG topology validation)
- `pkg/security/...` (path/URL validation, SSRF)

Add fuzz tests as `func FuzzXxx(f *testing.F)` co-located with the
source and they will be picked up by `go test -fuzz=. -fuzztime=30s`.

## Frontend tests

```bash
pnpm --filter ./web run test
pnpm --filter ./web run build
pnpm --filter ./web-editor-next run build
```

Web has Vitest unit tests; web-editor-next currently relies on build
success + lint as the test gate. Adding Vitest there is on the backlog.

## Running a single test fast

```bash
go test -run TestSpecificName ./pkg/server/ -count=1 -race
go test -v -run 'TestX/subtest_name' ./pkg/api/...
```

`-count=1` defeats the test cache when iterating on a flaky test.

## Common pitfalls

- **`pkg/api/agent_test.go` is 42 KB** — table-driven, hundreds of
  cases. Run focused subsets with `-run` rather than the whole file.
- **Pipeline tests touch the filesystem under `.saker/`** — clean
  between runs if state-sensitive: `rm -rf .saker/test-*`.
- **Race detector slows tests ~3-5x** — use `make test-short` for
  rapid iteration, `make test-race` before pushing.
- **Docker required for E2E + some sandbox tests** — gracefully
  skipped if Docker absent, but you'll miss real coverage.

## CI matrix

The `.github/workflows/ci.yml` matrix runs:

- `lint` — golangci-lint + govulncheck
- `test` — `go test -race -coverprofile=coverage.out ./...`
- `web` — pnpm install + i18n audit + tests + build + bundle-size check
- `web-editor-next` — pnpm install + lint + build + bundle-size check
- `build` — Go binary build (depends on lint+test)
- `e2e` — Docker E2E (depends on build)

Coverage is uploaded but not gated. Bundle size *is* gated against
`bundle-size-baseline.json` in each frontend.
