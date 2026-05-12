# 21 — OpenAI-compatible /v1/* gateway

Smoke-test client for the saker `/v1/*` HTTP gateway. Demonstrates four call
patterns that the OpenAI Python/Go/JS SDKs make against the same endpoints:

1. `GET /v1/models` — discover the saker tier ids exposed to clients
2. `POST /v1/chat/completions` (stream=false) — single-shot blocking response
3. `POST /v1/chat/completions` (stream=true) — Server-Sent Events streaming
4. `POST /v1/chat/completions` with `extra_body.human_input_mode=never` —
   forces the AskUserQuestion fallback path so the model is told to ask in its
   reply text instead of pausing the run

The example uses only the Go standard library so the wire format is visible
on both sides — no `openai-go` SDK dependency.

## Setup

### 1. Start saker with the gateway enabled

```bash
make build

./bin/saker --server \
  --server-addr 127.0.0.1:10112 \
  --server-data-dir ~/.saker/server \
  --openai-gw-enabled
```

You should see:

```
OpenAI-compatible gateway enabled at /v1/* (max_runs=256, ring=512, expires=600s)
Saker server listening on 127.0.0.1:10112
```

### 2. Issue a Bearer API key

In a second shell:

```bash
./bin/saker openai-key create \
  --user $USER \
  --project new \
  --name openai-gw-demo
```

Copy the `ak_…` plaintext shown — it is **only printed once**. Export it:

```bash
export SAKER_API_KEY=ak_...
```

### Dev shortcut (skips auth)

For local development you can replace step 2 with `--openai-gw-dev-bypass` on
the server. Any non-empty `--api-key` value will then be accepted:

```bash
./bin/saker --server \
  --server-addr 127.0.0.1:10112 \
  --openai-gw-enabled \
  --openai-gw-dev-bypass
```

## Run

All four demos in order:

```bash
go run ./examples/21-openai-gateway --api-key "$SAKER_API_KEY"
```

A single demo at a time:

```bash
go run ./examples/21-openai-gateway --api-key "$SAKER_API_KEY" --demo models
go run ./examples/21-openai-gateway --api-key "$SAKER_API_KEY" --demo sync
go run ./examples/21-openai-gateway --api-key "$SAKER_API_KEY" --demo stream
go run ./examples/21-openai-gateway --api-key "$SAKER_API_KEY" --demo never
```

Useful flags:

| Flag | Default | Notes |
|---|---|---|
| `--addr` | `http://127.0.0.1:10112` | Server base URL |
| `--api-key` | `$SAKER_API_KEY` | Bearer key from `saker openai-key create` |
| `--model` | `saker-default` | Tier id from `/v1/models` |
| `--prompt` | "hello" sentence | User prompt for sync/stream demos |
| `--demo` | `all` | One of `models\|sync\|stream\|never\|all` |
| `--timeout` | `60s` | Per-request HTTP timeout |

## Equivalent curl

```bash
# /v1/models
curl -sS -H "Authorization: Bearer $SAKER_API_KEY" \
  http://127.0.0.1:10112/v1/models | jq

# /v1/chat/completions, blocking
curl -sS -H "Authorization: Bearer $SAKER_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "saker-default",
    "messages": [{"role":"user","content":"say hi"}]
  }' \
  http://127.0.0.1:10112/v1/chat/completions

# /v1/chat/completions, streaming SSE
curl -sS -N -H "Authorization: Bearer $SAKER_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "saker-default",
    "stream": true,
    "messages": [{"role":"user","content":"say hi"}]
  }' \
  http://127.0.0.1:10112/v1/chat/completions

# human_input_mode=never (fallback path)
curl -sS -H "Authorization: Bearer $SAKER_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "saker-default",
    "messages": [{"role":"user","content":"pick a color, ask if unsure"}],
    "extra_body": {"human_input_mode": "never", "cancel_on_disconnect": true}
  }' \
  http://127.0.0.1:10112/v1/chat/completions
```

## What to expect

- **models** — four tier ids (`saker-default`, `saker-low`, `saker-mid`,
  `saker-high`) owned by `saker`.
- **sync** — one short assistant reply with `finish_reason: "stop"`.
- **stream** — characters print as deltas arrive; final line shows `[finish=stop]`.
- **never** — assistant replies in plain text and asks its clarifying question
  inline rather than calling `ask_user_question` (the gateway routes the call
  to the fallback path when `human_input_mode=never`).

## Persistence + reconnect

By default the runhub keeps events in memory only — a process restart loses
all in-flight runs and a client that drops mid-stream cannot resume. Pass
`--openai-gw-runhub-dsn` to switch backends:

| DSN | Backend | Restart-replay | Cross-process fan-out | Build tag |
|---|---|---|---|---|
| _empty_ (default) | `MemoryHub` | no | no | none |
| `sqlite:///path/runhub.db` or `/path/runhub.db` | `PersistentHub` (SQLite) | yes | no (single-process) | none |
| `postgres://user:pass@host/db?sslmode=disable` | `PersistentHub` (Postgres) | yes | yes via `LISTEN/NOTIFY` | `-tags postgres` |

```bash
# SQLite single-process persistence (default Go build, no extra deps)
./bin/saker --server \
  --server-addr 127.0.0.1:10112 \
  --openai-gw-enabled \
  --openai-gw-runhub-dsn 'sqlite:///tmp/saker-runhub.db'

# Postgres multi-process fan-out (requires -tags postgres at build time)
go build -tags postgres -o ./bin/saker ./cmd/saker
./bin/saker --server \
  --server-addr 127.0.0.1:10112 \
  --openai-gw-enabled \
  --openai-gw-runhub-dsn 'postgres://saker:secret@db:5432/saker?sslmode=disable'
```

### Reconnecting with `Last-Event-ID`

Once persistence is on, a client that drops mid-stream can resume from the
last event id it saw using either the SSE-standard header or a query string:

```bash
# 1. Start a streaming run; capture the X-Saker-Run-Id response header
#    (curl -D dumps headers to a file so we can grab the run id later).
curl -sS -D /tmp/headers.log -N -H "Authorization: Bearer $SAKER_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "saker-default",
    "stream": true,
    "messages": [{"role":"user","content":"count slowly to 20"}]
  }' \
  http://127.0.0.1:10112/v1/chat/completions > /tmp/sse.log

RUN_ID=$(awk -F': ' 'tolower($1)=="x-saker-run-id"{print $2}' /tmp/headers.log | tr -d '\r\n')

# 2. Note the highest `id:` seen in /tmp/sse.log. The wire format is
#    `<run_id>:<seq>` — pass the full token as your cursor verbatim so
#    the server can detect cross-run probe attempts.
LAST=$(grep -E '^id: ' /tmp/sse.log | tail -n 1 | sed -E 's/^id:[[:space:]]*//; s/[[:space:]]+$//')

# 3. Resume from that seq via the dedicated reconnect endpoint:
curl -sS -N -H "Authorization: Bearer $SAKER_API_KEY" \
  "http://127.0.0.1:10112/v1/runs/$RUN_ID/events?last_event_id=$LAST"

# Equivalent using the SSE-standard header (browsers' EventSource sends this
# automatically on auto-reconnect):
curl -sS -N -H "Authorization: Bearer $SAKER_API_KEY" \
  -H "Last-Event-ID: $LAST" \
  "http://127.0.0.1:10112/v1/runs/$RUN_ID/events"
```

The server replays every event with `seq > last_event_id` from the ring
buffer (or from the persistent store when the ring has rotated past), then
tails live events until the run terminates and writes `data: [DONE]\n\n`.
A run that has aged past its retention window returns `410 Gone`; one whose
events have been ring-evicted on a `MemoryHub` backend (no DB to fall back
to) also returns `410` with `event_replay_unrecoverable`.

Cross-tenant access returns `404` (not `403`) so an attacker cannot probe
for run-id existence.

## Limitations

- `extra_body.ask_user_question_mode=tool_call` returns `400` —
  the pause/resume protocol is unimplemented.
- `/v1/responses` is not yet wired.

See `.docs/openai-inbound-gateway.md` for the full protocol reference.
