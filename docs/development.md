# Development Guide

## Setup

```bash
cd web && npm ci
cd ../web-editor-next && npm ci
cd ..
```

Go dependencies are resolved through the root `go.mod`. The `godeps`
packages (github.com/godeps/aigo, goim, govm) are remote Go modules, not
local in-tree directories.

## Common Commands

```bash
make run          # build both frontends, build backend, start server
make build        # production build without starting the server
make saker        # build CLI/backend using existing frontend exports if present
make server-dev   # build backend with empty embedded frontend placeholders
make notices      # regenerate third-party dependency notices
make oss-check    # run open-source hygiene checks
make clean        # remove bin/ and coverage output
```

## Tests

```bash
make test-short
make test-unit
make test-pipeline
go test ./pkg/...
```

Frontend checks:

```bash
cd web && npm run test && npm run build
cd ../web-editor-next && npm run build
```

Docker-based suites are available through:

```bash
make e2e-build
make e2e-run
make e2e-clean
```

## Development Servers

```bash
make web-dev
make web-editor-dev
```

The embedded production server listens on `:10112` by default:

```bash
make server
./bin/saker --server --server-addr :10112
```

## Open Source Hygiene

Before publishing a release or large pull request, run:

```bash
make notices
make oss-check
```

`make notices` refreshes `docs/third-party-notices.md` from the frontend
lockfiles. `make oss-check` runs the notice generator, checks for stale internal
documentation references, and runs the local secret/leak detector tests.
