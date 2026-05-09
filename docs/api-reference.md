# Saker API Reference

Saker exposes a REST and WebSocket API for interacting with the runtime, managing canvas workflows, apps, files, and authentication.

## OpenAPI Specification

The full OpenAPI (Swagger) specification is auto-generated from handler annotations in the `pkg/server/` package. To regenerate the spec:

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

## Multi-Tenant Mode

When a project store is wired, REST endpoints prefix the project ID in the URL path. For example:

- `/api/canvas/{projectId}/{threadId}/execute`
- `/api/apps/{projectId}/{appId}/run`

In single-project (embedded library) mode, the project ID prefix is omitted.