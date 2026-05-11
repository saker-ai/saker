# OTLP integration test

End-to-end check that `pkg/middleware/otel_http.go` actually exports spans
to a live collector — not just that the middleware code compiles.

## Run locally

```sh
# Start collector (binds 127.0.0.1:4317/4318 only)
docker compose -f test/integration/otlp/docker-compose.otel.yml up -d --wait

# Run test against it
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 \
  go test -tags integration -count=1 ./test/integration/otlp/...

# Tear down
docker compose -f test/integration/otlp/docker-compose.otel.yml down
```

The test uses a unique `OTEL_SERVICE_NAME` per run (`saker-otlp-itest-<ts>`)
so concurrent runs don't collide on the shared NDJSON output file.

## What it verifies

1. `OTLPConfigFromEnv` reads `OTEL_EXPORTER_OTLP_ENDPOINT` /
   `OTEL_SERVICE_NAME`.
2. `SetupOTLP` installs an exporter pointing at the spun-up collector.
3. `OTELHTTPMiddleware` produces a span the collector receives.
4. The exported span carries the resource attribute
   `service.name = saker-otlp-itest-<ts>` so the test can grep for its
   own emission and ignore noise from other runs.

## Why not run in CI by default?

The job needs Docker on the runner and has a few-second startup cost.
It's tagged behind `//go:build integration` so the standard
`go test ./...` lane stays fast and Docker-free. CI invokes it from a
dedicated `make test-otel` target only when the OTel middleware itself
changes.

## Files

- `docker-compose.otel.yml` — single-service collector
- `otel-collector-config.yaml` — receivers (OTLP), exporter (file +
  debug log), and the health_check extension on `:13133`
- `otlp_integration_test.go` — the test
- `otel-output/` — NDJSON span dump (gitignored)
