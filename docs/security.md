# Security Model

For vulnerability reporting and security policy, see [SECURITY.md](../SECURITY.md).

Saker can execute tools, read files, call external model providers, and run
generated workflows. Treat it as a powerful local automation tool.

## Defaults

- Project runtime state is stored under `.saker/` and ignored by git.
- Web credentials should be configured with `--auth-user` and `--auth-pass`
  before exposing the server beyond localhost.
- API keys should stay in environment variables or local settings files.

## Authentication

When `--auth-user` / `--auth-pass` (or LDAP/OIDC) is configured, the server
requires a valid session for all authenticated endpoints. Sessions are
HMAC-signed tokens stored in cookies; they survive server restarts because
the signing key is generated once at startup and kept in memory.

- **Local auth**: Username/password stored as bcrypt hash in settings.
- **LDAP**: Bind DN + search filter; optional group-based role mapping.
- **OIDC**: OAuth2 redirect flow; supports Google, GitHub, Keycloak, etc.
- **Metrics endpoint** (`/metrics`): Requires authentication when any auth
  mechanism is enabled. In single-user / localhost dev mode (no auth
  configured), metrics are accessible without credentials.
- **Health endpoint** (`/health`): Always unauthenticated (for load balancers).

### Session tokens

- Format: `base64(username:role:expires:nonce).hex(HMAC-SHA256(payload))`
- TTL: 7 days (configurable via `sessionTTL` constant).
- Logout revokes tokens in memory; revocations do not survive a restart
  (re-login is required, which is the intended behavior).
- Signing key: 32 random bytes from `crypto/rand`. If `crypto/rand` fails,
  the server refuses to start rather than falling back to a predictable key.

## WebSocket Security

WebSocket connections (`/ws`) require authentication via the same middleware
chain as HTTP endpoints. The upgrade handler validates the `Origin` header
against the CORS allowed origins configuration:

- **With explicit CORS origins**: only those origins can open WebSocket
  connections.
- **Without explicit CORS origins (default)**: only localhost origins are
  permitted.
- **Non-browser clients** (CLI, scripts) that send no `Origin` header are
  always allowed.

## Debug Mode

The `--debug` flag (`SAKER_DEBUG=true`) enables `/debug/pprof/` endpoints
that expose goroutine stacks, heap profiles, and CPU profiles. **These
endpoints have no authentication.** Never enable debug mode in production or
any environment reachable by untrusted users.

When debug is enabled, the server logs a prominent warning at startup.

## CORS

Cross-Origin Resource Sharing is configurable via settings:

```json
{
  "cors": {
    "allowedOrigins": ["https://my-app.example.com"]
  }
}
```

When no explicit origins are configured, only localhost origins
(`http://localhost:*`, `http://127.0.0.1:*`, HTTPS variants, IPv6 `[::1]`)
are permitted. The WebSocket upgrade handler uses the same origin list.

## Sandboxing

The CLI supports multiple sandbox backends:

```bash
--sandbox-backend host
--sandbox-backend landlock
--sandbox-backend gvisor
--sandbox-backend docker
--sandbox-backend govm
```

Backend availability depends on the host OS, kernel features, and installed
runtime components. Use `landlock` or a virtualized backend for untrusted
projects when available.

## Upload Security

File uploads are limited to 50 MB and require authentication. MIME type
detection uses content-based analysis (`http.DetectContentType`) as the
primary method, falling back to extension-based detection only when the
content check returns `application/octet-stream`. Client-provided
`Content-Type` is the last resort and considered unreliable.

Uploaded filenames are sanitized: path separators and null bytes are removed,
and each file is prefixed with a UUID to prevent collision and path-based
attacks.

## Rate Limiting

Authentication endpoints (`/api/auth/login`) are rate-limited to 5 requests
per second per IP to prevent brute-force attacks. The rate limiter runs a
background cleanup goroutine that is properly stopped on server shutdown.

## Open Source Hygiene

Do not commit:

- `.saker/`
- `.env`
- provider API keys
- generated media outputs
- frontend build outputs
- local database or log files

Before publishing a release, run the production build and the relevant tests:

```bash
make build
make test-short
make oss-check
```