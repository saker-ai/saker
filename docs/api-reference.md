# Saker API Reference

Saker exposes a REST and WebSocket API for interacting with the runtime, managing canvas workflows, apps, files, and authentication.

## OpenAPI Specification

The full OpenAPI (Swagger) specification is auto-generated from handler annotations in the `pkg/server/` package and the global `@title`/`@host`/`@securityDefinitions` block in `cmd/saker/main.go`. To regenerate the spec, first install the `swag` CLI once:

```bash
go install github.com/swaggo/swag/cmd/swag@latest
```

Then run:

```bash
make swagger
```

This runs `swag init` and writes the output to `docs/swagger/`. The generated spec includes:

- `docs/swagger/swagger.json` -- JSON format
- `docs/swagger/swagger.yaml` -- YAML format
- `docs/swagger/docs.go` -- Go embedded format

To explore the API interactively, you can load `swagger.json` or `swagger.yaml` into [Swagger UI](https://swagger.io/tools/swagger-ui/) or any compatible OpenAPI viewer.

## API Overview

### Health

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Liveness check, returns `{status: "ok", time: "..."}` |

### WebSocket

| Method | Path | Description |
|--------|------|-------------|
| GET | `/ws` | Upgrade to WebSocket for JSON-RPC bidirectional communication |

### Files

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/files/{path}` | Serve local file for media preview |
| POST | `/api/upload` | Upload a media file (multipart, max 50 MB) |
| GET | `/media/{key}` | Serve media object from configured object store |

### Canvas

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/canvas/{threadId}/execute` | Start an asynchronous canvas run |
| GET | `/api/canvas/{threadId}/document` | Read canvas JSON document |
| GET | `/api/canvas/runs/{runId}` | Poll canvas run status |
| POST | `/api/canvas/runs/{runId}/cancel` | Cancel a running canvas execution |

### Apps

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/apps` | List apps in current scope |
| POST | `/api/apps` | Create a new app |
| GET | `/api/apps/{appId}` | Get app metadata with inputs/outputs |
| PUT | `/api/apps/{appId}` | Update app metadata |
| DELETE | `/api/apps/{appId}` | Delete an app |
| POST | `/api/apps/{appId}/publish` | Publish a new version from source canvas |
| GET | `/api/apps/{appId}/versions` | List published version summaries |
| POST | `/api/apps/{appId}/run` | Start an app run (supports Bearer API-key auth) |
| GET | `/api/apps/{appId}/runs/{runId}` | Poll app run status |
| POST | `/api/apps/{appId}/runs/{runId}/cancel` | Cancel an app run |
| PUT | `/api/apps/{appId}/published-version` | Set the published version pointer |

### App API Keys

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/apps/{appId}/keys` | List API keys (without secrets) |
| POST | `/api/apps/{appId}/keys` | Create a new API key (plaintext returned once) |
| DELETE | `/api/apps/{appId}/keys/{keyId}` | Revoke an API key |
| POST | `/api/apps/{appId}/keys/{keyId}/rotate` | Rotate an API key (new plaintext returned once) |

### App Share Tokens

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/apps/{appId}/share` | List share tokens (preview only) |
| POST | `/api/apps/{appId}/share` | Create a share token for anonymous access |
| DELETE | `/api/apps/{appId}/share/{token}` | Revoke a share token |
| GET | `/api/apps/public/{token}` | Read public app schema (no auth) |
| POST | `/api/apps/public/{token}/run` | Start an anonymous run via share token |
| GET | `/api/apps/public/{token}/runs/{runId}` | Poll anonymous run status |
| POST | `/api/apps/public/{token}/runs/{runId}/cancel` | Cancel an anonymous run |

### RPC over HTTP

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/rpc/{method}` | Generic JSON-RPC over HTTP adapter (non-streaming methods only) |

### Authentication

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/auth/login` | Login with username/password, returns session cookie |
| GET | `/api/auth/status` | Check whether auth is required and current session status |
| POST | `/api/auth/logout` | Clear session cookie and revoke token |
| GET | `/api/auth/providers` | List enabled auth providers (local, LDAP, OIDC) |
| GET | `/api/auth/oidc/login` | Initiate OIDC redirect flow |
| GET | `/api/auth/oidc/callback` | OIDC callback, exchanges code for session |

## Authentication

- Localhost requests are always allowed as admin (no auth required).
- Remote access requires a session cookie obtained via `/api/auth/login`.
- App runs support Bearer API-key auth (`Authorization: Bearer ak_...`).
- Public share-token endpoints (`/api/apps/public/...`) require no authentication.
- OpenAI gateway endpoints (`/v1/*`) use Bearer token auth (see below).

## Multi-Tenant Mode

When a project store is wired, REST endpoints prefix the project ID in the URL path. For example:

- `/api/canvas/{projectId}/{threadId}/execute`
- `/api/apps/{projectId}/{appId}/run`

In single-project (embedded library) mode, the project ID prefix is omitted.

---

## OpenAI-Compatible Gateway

Saker ships an OpenAI-compatible HTTP/SSE gateway under `/v1/*`. It implements the [Chat Completions](https://platform.openai.com/docs/api-reference/chat) protocol with saker-specific extensions via `extra_body`, plus a lightweight Runs API for reconnect, cancellation, and interactive tool-call flows.

**Enable the gateway:**

```bash
./bin/saker --server --openai-gw-enabled
```

All `/v1/*` routes require Bearer token auth and are subject to per-tenant rate limiting.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v1/models` | List available models |
| POST | `/v1/chat/completions` | Chat completions (streaming & synchronous) |
| GET | `/v1/runs/{id}/events` | SSE reconnect — resume an in-flight run |
| DELETE | `/v1/runs/{id}` | Cancel an in-flight run |
| POST | `/v1/runs/{id}/submit` | Submit tool outputs to a paused run |

---

### Authentication

All `/v1/*` requests require a Bearer token in the `Authorization` header:

```
Authorization: Bearer sk-proj-...
```

When a project store is configured, the token is validated against the API key table. Without a project store, requests are rejected with `401` unless dev bypass is explicitly enabled.

**Dev bypass**: set `--openai-gw-dev-bypass-auth` (or `OPENAI_GW_DEV_BYPASS=true`) to skip auth for local development. Never enable in production.

**Tenant isolation**: all run operations (reconnect, cancel, submit) enforce strict tenant ownership. A run is only accessible to the same Bearer key that created it. Cross-tenant probes receive `404` (not `403`) to prevent existence leaks.

---

### Rate Limiting

Per-tenant token-bucket rate limiting is applied to all `/v1/*` routes.

- Default: **10 requests/second** per Bearer key, burst of max(RPS, 5).
- Configurable via operator option `RPSPerTenant`. Set to 0 to disable.
- Idle visitors (>10 min since last request) are garbage-collected.

When the limit is hit, the gateway returns:

```json
{
  "error": {
    "message": "rate limit exceeded for this key (10 rps)",
    "type": "rate_limit_error"
  }
}
```

**Status code**: `429 Too Many Requests`

---

### GET /v1/models

Lists the model tiers the gateway accepts.

**Response:**

```json
{
  "object": "list",
  "data": [
    {"id": "saker-default", "object": "model", "created": 1735689600, "owned_by": "saker"},
    {"id": "saker-low",     "object": "model", "created": 1735689600, "owned_by": "saker"},
    {"id": "saker-mid",     "object": "model", "created": 1735689600, "owned_by": "saker"},
    {"id": "saker-high",    "object": "model", "created": 1735689600, "owned_by": "saker"}
  ]
}
```

| Model ID | Description |
|----------|-------------|
| `saker-default` | Resolves to the operator's configured default model |
| `saker-low` | Low-tier model (fastest, cheapest) |
| `saker-mid` | Mid-tier model (balanced) |
| `saker-high` | High-tier model (most capable) |

---

### POST /v1/chat/completions

Create a chat completion. Supports both streaming (SSE) and synchronous modes.

**Request body:**

```json
{
  "model": "saker-mid",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "Hello!"}
  ],
  "stream": true,
  "extra_body": {
    "session_id": "sess_abc123",
    "allowed_tools": ["bash", "file_read", "grep"]
  }
}
```

#### Standard OpenAI Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `model` | string | Yes | Model tier: `saker-default`, `saker-low`, `saker-mid`, `saker-high` |
| `messages` | array | Yes | Conversation messages (see [Message Format](#message-format)) |
| `stream` | boolean | No | `true` for SSE streaming, `false` for synchronous response. Default `false` |
| `stream_options` | object | No | `{"include_usage": true}` to receive a usage chunk at end of stream |
| `temperature` | number | No | Sampling temperature (0.0–2.0) |
| `top_p` | number | No | Nucleus sampling |
| `max_tokens` | integer | No | Max output tokens (fallback for `max_completion_tokens`) |
| `max_completion_tokens` | integer | No | Max output tokens (takes precedence over `max_tokens`) |
| `stop` | string or array | No | Stop sequence(s) |
| `seed` | integer | No | Deterministic sampling seed |
| `tool_choice` | string | No | `"auto"`, `"none"`, `"required"`, or a tool name |
| `parallel_tool_calls` | boolean | No | Allow model to call multiple tools in one turn |
| `presence_penalty` | number | No | Presence penalty |
| `frequency_penalty` | number | No | Frequency penalty |
| `user` | string | No | End-user identifier |
| `tools` | array | No | Reserved for compatibility; currently ignored by the gateway |

#### extra_body (Saker Extensions)

Pass saker-specific parameters via the `extra_body` field:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `session_id` | string | (new session) | Continue an existing saker session. Empty creates a new one |
| `allowed_tools` | string[] | (all tools) | Per-request tool whitelist. Only listed tools are sent to the LLM. Intersected with server-side preset and persona constraints |
| `human_input_mode` | enum | `"always"` | HITL umbrella switch: `always` / `terminate` / `never` |
| `ask_user_question_mode` | enum | `"fallback"` | AskUserQuestion behavior: `fallback` / `tool_call` / `disabled` |
| `interactive` | boolean | — | Deprecated alias. `true` → `always`, `false` → `never`. Use `human_input_mode` instead |
| `expose_tool_calls` | boolean | `false` | Emit saker's internal tool calls as OpenAI tool_calls chunks |
| `cancel_on_disconnect` | boolean | `false` | Cancel the run when the SSE client disconnects. Forced `true` when `human_input_mode` is `never` |
| `expires_after_seconds` | integer | 600 | Per-run idle timeout (60–86400 seconds) |
| `system_prompt_mode` | enum | `"prepend"` | How client system message merges with persona: `prepend` / `replace` |

**Tool whitelist example:**

```json
{
  "model": "saker-mid",
  "messages": [{"role": "user", "content": "List files in the project"}],
  "stream": true,
  "extra_body": {
    "allowed_tools": ["bash", "file_read", "glob", "grep"]
  }
}
```

When `allowed_tools` is set, only the listed tools are available to the agent for this request. The whitelist is intersected with:
1. The server-side mode preset (e.g., `server_api` excludes canvas/browser tools)
2. Any persona-level `EnabledTools` constraints

This means `allowed_tools` can only narrow the tool set, never expand it beyond what the server allows.

#### Message Format

Messages follow the OpenAI chat message schema:

```json
{"role": "system", "content": "You are a code assistant."}
```

```json
{"role": "user", "content": "Fix the bug in main.go"}
```

```json
{"role": "user", "content": [
  {"type": "text", "text": "What's in this image?"},
  {"type": "image_url", "image_url": {"url": "data:image/png;base64,..."}}
]}
```

| Role | Description |
|------|-------------|
| `system` / `developer` | System instructions (prepended to prompt by default) |
| `user` | User messages. Supports text and multipart (text + images) |
| `assistant` | Prior assistant responses. May include `tool_calls` |
| `tool` | Tool result for a prior `tool_call_id` |

**Image support**: user messages accept `image_url` content parts. Both `data:` URIs (base64) and `http(s)` URLs are supported. HTTP URLs are fetched server-side with the following protections:
- **SSRF protection**: DNS resolution is validated against private/internal IP ranges (loopback, RFC 1918, link-local, cloud metadata endpoints). Connections are pinned to the resolved IP to prevent DNS rebinding.
- **Redirect limit**: max 3 hops.
- **Timeout**: 15 seconds.
- **Size cap**: 20 MiB.
- **Content-Type validation**: response must be `image/*`; non-image responses are rejected.

#### Synchronous Response (stream=false)

```json
{
  "id": "run_abc123",
  "object": "chat.completion",
  "created": 1715680000,
  "model": "saker-mid",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Here are the files in your project..."
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 128,
    "completion_tokens": 256,
    "total_tokens": 384
  }
}
```

#### Streaming Response (stream=true)

The gateway sends SSE frames:

```
id: run_abc123:1
data: {"id":"run_abc123","object":"chat.completion.chunk","created":1715680000,"model":"saker-mid","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

id: run_abc123:2
data: {"id":"run_abc123","object":"chat.completion.chunk","created":1715680000,"model":"saker-mid","choices":[{"index":0,"delta":{"content":"Here "},"finish_reason":null}]}

id: run_abc123:3
data: {"id":"run_abc123","object":"chat.completion.chunk","created":1715680000,"model":"saker-mid","choices":[{"index":0,"delta":{"content":"are the files..."},"finish_reason":null}]}

id: run_abc123:4
data: {"id":"run_abc123","object":"chat.completion.chunk","created":1715680000,"model":"saker-mid","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

**SSE headers:**

```
Content-Type: text/event-stream; charset=utf-8
Cache-Control: no-cache, no-store, no-transform
Connection: keep-alive
X-Accel-Buffering: no
```

**Response headers:**

| Header | Description |
|--------|-------------|
| `X-Saker-Run-Id` | Run identifier for reconnect and cancellation |
| `X-Saker-Trace-Id` | OpenTelemetry trace ID |
| `X-Saker-Thread-Id` | Conversation thread ID (when persistence is enabled) |

**Finish reasons:**

| Value | Meaning |
|-------|---------|
| `stop` | Normal completion or stop sequence |
| `tool_calls` | Agent requests interactive tool response (ask_user_question in tool_call mode) |
| `length` | Output truncated by max_tokens |

**Keepalive**: the server sends SSE comment frames (`: keepalive`) every 15 seconds to keep the connection alive.

**Usage chunk**: when `stream_options.include_usage` is `true`, a final chunk with empty choices and populated `usage` is emitted before `[DONE]`.

---

### GET /v1/runs/{id}/events

Resume an in-flight SSE stream after a client disconnect.

**Query parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `last_event_id` | string | Cursor in `run_id:seq` format. Events after this sequence are replayed |

Also accepts the standard `Last-Event-ID` header as an alternative to the query parameter.

**Behavior:**
- Replays buffered events from the run's ring buffer starting after the given sequence.
- If the ring has aged out and the gap cannot be filled, returns `410 Gone`.
- Does not cancel the producer on client disconnect (reconnect is the entire purpose).
- Keepalive comments every 30 seconds.
- Cross-tenant access returns `404` (no existence leak).

**Response**: same SSE format as the original `/v1/chat/completions` stream.

---

### DELETE /v1/runs/{id}

Cancel an in-flight run.

**Response:** `204 No Content` on success.

- Idempotent: cancelling a terminal run still returns 204.
- Cross-tenant or unknown run: `404 Not Found`.
- The run row remains in the hub for the terminal retention window (default 60s) so in-flight consumers can observe the cancellation.

---

### POST /v1/runs/{id}/submit

Submit tool outputs to a run paused in `requires_action` state (when `ask_user_question_mode` is `tool_call`).

**Request body:**

```json
{
  "tool_outputs": [
    {
      "tool_call_id": "call_abc123",
      "output": "The user chose option A"
    }
  ],
  "stream": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tool_outputs` | array | Yes | Array of tool output objects |
| `tool_outputs[].tool_call_id` | string | Yes | Must match the `id` from the paused tool_call |
| `tool_outputs[].output` | string | Yes | The answer text, or a JSON object with `action` + `content` |
| `stream` | boolean | No | `true` to receive continued output as SSE, `false` for synchronous |

**Output formats:**

Plain text:
```json
{"tool_call_id": "call_abc123", "output": "Yes, proceed"}
```

Structured (action envelope):
```json
{"tool_call_id": "call_abc123", "output": "{\"action\": \"accept\", \"content\": \"Go ahead\"}"}
```

Supported actions: `accept`, `decline`, `cancel`.

**Errors:**
- `404`: unknown run or cross-tenant
- `400` (`session_awaiting_tool_response`): run is not in a paused state
- `400` (`tool_outputs_invalid`): tool_call_id mismatch or malformed output

---

### Interactive Tool Call Flow (ask_user_question)

When `extra_body.ask_user_question_mode` is `"tool_call"`, the gateway implements a two-phase flow for human-in-the-loop:

**Phase 1 — Agent asks a question:**

The agent calls `ask_user_question` internally. The gateway emits it as an OpenAI-style `tool_calls` chunk:

```
data: {"id":"run_abc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_xyz","type":"function","function":{"name":"ask_user_question","arguments":"{\"question\":\"Which option?\"}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]
```

The stream ends with `finish_reason: "tool_calls"`. The run is now paused.

**Phase 2 — Client submits the answer:**

```http
POST /v1/runs/run_abc/submit
Content-Type: application/json

{
  "tool_outputs": [{"tool_call_id": "call_xyz", "output": "Option A"}],
  "stream": true
}
```

The run resumes and the agent continues with the provided answer. If `stream: true`, the response is a new SSE stream of the continued output.

---

### Error Format

All errors follow the OpenAI error envelope:

```json
{
  "error": {
    "message": "Human-readable error description",
    "type": "invalid_request_error",
    "param": "messages",
    "code": "missing_field"
  }
}
```

| HTTP Status | Error Type | Description |
|-------------|-----------|-------------|
| 400 | `invalid_request_error` | Malformed request, missing fields, bad values |
| 401 | `authentication_error` | Missing or invalid Bearer token |
| 403 | `permission_error` | Valid token but insufficient permissions |
| 404 | `not_found_error` | Resource not found (run, thread) |
| 429 | `rate_limit_error` | Per-tenant rate limit exceeded |
| 500 | `server_error` | Internal error |
| 503 | `service_unavailable_error` | Service temporarily unavailable |

**Mid-stream terminal errors** are delivered as an `event: error` SSE frame before `[DONE]`:

```
event: error
data: {"error":{"message":"run was cancelled","type":"api_error","code":"run_cancelled"}}

data: [DONE]
```

| Code | Meaning |
|------|---------|
| `run_cancelled` | Run cancelled by client disconnect or explicit DELETE |
| `run_expired` | Run timed out (idle/await timeout elapsed) |
| `run_failed` | Internal error during execution |

---

### Client Examples

#### Python (OpenAI SDK)

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:10112/v1",
    api_key="sk-your-key",
)

# Streaming with tool whitelist
stream = client.chat.completions.create(
    model="saker-mid",
    messages=[{"role": "user", "content": "List all Go files"}],
    stream=True,
    extra_body={
        "allowed_tools": ["bash", "glob", "grep"],
        "cancel_on_disconnect": True,
    },
)
for chunk in stream:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")
```

#### curl (Streaming)

```bash
curl -N http://localhost:10112/v1/chat/completions \
  -H "Authorization: Bearer sk-your-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "saker-mid",
    "messages": [{"role": "user", "content": "Show disk usage"}],
    "stream": true,
    "extra_body": {
      "allowed_tools": ["bash"],
      "human_input_mode": "never"
    }
  }'
```

#### curl (Synchronous)

```bash
curl http://localhost:10112/v1/chat/completions \
  -H "Authorization: Bearer sk-your-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "saker-mid",
    "messages": [{"role": "user", "content": "What is 2+2?"}],
    "stream": false
  }'
```

#### curl (Reconnect)

```bash
# Save the run ID from the X-Saker-Run-Id header, then reconnect:
curl -N "http://localhost:10112/v1/runs/run_abc123/events?last_event_id=run_abc123:5" \
  -H "Authorization: Bearer sk-your-key"
```

#### curl (Cancel)

```bash
curl -X DELETE http://localhost:10112/v1/runs/run_abc123 \
  -H "Authorization: Bearer sk-your-key"
```

#### TypeScript (fetch)

```typescript
const response = await fetch("http://localhost:10112/v1/chat/completions", {
  method: "POST",
  headers: {
    "Authorization": "Bearer sk-your-key",
    "Content-Type": "application/json",
  },
  body: JSON.stringify({
    model: "saker-mid",
    messages: [{ role: "user", content: "Explain the codebase" }],
    stream: true,
    extra_body: {
      allowed_tools: ["file_read", "grep", "glob"],
      session_id: "my-session",
    },
  }),
});

const reader = response.body!.getReader();
const decoder = new TextDecoder();
while (true) {
  const { done, value } = await reader.read();
  if (done) break;
  const text = decoder.decode(value);
  // Parse SSE frames...
}
```

---

### Operator Configuration

The gateway is configured via CLI flags or environment variables at server startup:

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--openai-gw-enabled` | `OPENAI_GW_ENABLED` | `false` | Enable the gateway |
| `--openai-gw-dev-bypass-auth` | `OPENAI_GW_DEV_BYPASS` | `false` | Skip auth for local dev |
| `--openai-gw-rps` | `OPENAI_GW_RPS` | `10` | Per-tenant requests/second |
| `--openai-gw-max-runs` | `OPENAI_GW_MAX_RUNS` | `256` | Max concurrent runs |
| `--openai-gw-max-runs-per-tenant` | `OPENAI_GW_MAX_RUNS_PER_TENANT` | `32` | Max concurrent runs per key |
| `--openai-gw-expires-after` | `OPENAI_GW_EXPIRES_AFTER` | `600` | Default run timeout (seconds) |
| `--openai-gw-ring-size` | `OPENAI_GW_RING_SIZE` | `512` | Per-run event ring buffer size |
| `--openai-gw-max-body` | `OPENAI_GW_MAX_BODY` | `10485760` | Max request body (bytes) |
| `--openai-gw-error-detail` | `OPENAI_GW_ERROR_DETAIL` | `dev` | Error detail mode: `dev` / `prod` |
| `--openai-gw-runhub-dsn` | `OPENAI_GW_RUNHUB_DSN` | (in-memory) | RunHub backend: `sqlite://path` or `postgres://...` |
| `--api-only` | — | `false` | Use `server_api` tool preset (no canvas/browser) |

**Tool presets** determine which tools are available:

| Preset | Included Groups | Use Case |
|--------|----------------|----------|
| `server_web` (default) | core_io, bash_mgmt, task_mgmt, web, media, canvas, browser | Web workspace with UI |
| `server_api` (`--api-only`) | core_io, bash_mgmt, task_mgmt, web, media, interaction | Pure API backend |

Further filtering is available via `--allowed-tools` (operator-level whitelist) and `--disallowed-tools` (blacklist). Client-side `extra_body.allowed_tools` can only narrow the operator set, never expand it.