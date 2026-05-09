# ADR 1: Migrate HTTP Framework from net/http to Gin

## Status: Accepted

## Context

The Saker server currently uses bare `net/http.ServeMux` + `gorilla/websocket` for all HTTP routing and middleware. This approach has several limitations:

1. **No composable middleware**: Auth is a monolithic `AuthManager.Middleware(next http.Handler)` wrapper — the entire auth decision tree is in one function. Adding CORS, rate limiting, or request-ID middleware requires wrapping the entire mux.
2. **No path parameters**: REST sub-dispatchers (canvas, apps, rpc) manually parse URL segments via `strings.TrimPrefix` + `strings.Split`. This is error-prone and makes route structure opaque.
3. **No route grouping**: All routes are registered flat on a ServeMux with no per-route middleware, no auth-required vs public grouping.
4. **Missing production hardening**: No server timeouts, no CORS, no rate limiting, no security headers, no request-ID middleware.

## Decision

Migrate the HTTP layer to [Gin](https://github.com/gin-gonic/gin), a Go HTTP framework that provides:

- Composable middleware chains (`gin.HandlerFunc`)
- Route groups with per-group middleware (public vs authenticated)
- Path parameters via `c.Param("id")` replacing manual URL parsing
- Built-in recovery and logging middleware
- `gin.WrapH()` for wrapping existing `http.Handler` implementations (pprof, static files)

The agent middleware pipeline (`pkg/middleware/`) and JSON-RPC dispatch (`Handler.HandleRequest`) remain unchanged — they are not HTTP middleware.

## Consequences

### Positive
- Auth decomposed into 3 composable Gin middleware functions (PublicPath, LocalhostIdentity, CookieSession)
- REST sub-dispatchers replaced by Gin path parameters — cleaner, less error-prone
- Route groups enable per-endpoint middleware (rate limiting on auth, body size limits on upload)
- Gin's built-in recovery middleware catches handler panics (safety net)
- New HTTP middleware easily added: CORS, rate limiting, request-ID, security headers, Prometheus metrics

### Negative
- New dependency (`github.com/gin-gonic/gin`) adds ~30KB to binary
- All REST handler signatures need Gin wrapper functions (`handleHealthGin` etc.) during transition
- WebSocket upgrade needs careful handling (Gin context vs gorilla/websocket)
- Migration must be incremental to avoid breaking existing functionality

### Migration Strategy
Phase-by-phase migration: introduce Gin engine first (keeping auth wrapper), decompose auth into Gin middleware, port routes one group at a time, remove compat shims at end.