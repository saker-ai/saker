package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/apps"
)

// appsRESTPath is the URL prefix mounted onto the HTTP mux. Anything under
// it routes through handleAppsREST which sub-dispatches by method + path.
// Mirrors the pattern used by canvasRESTPath / handleCanvasREST.
const appsRESTPath = "/api/apps/"

// handleAppsREST is the entry point for the apps REST API.
//
// When no project store is wired in (embedded library mode) the URL shape is:
//
//	GET    /api/apps                       → list current scope's apps
//	POST   /api/apps                       → create app
//	GET    /api/apps/{appId}               → meta + (if published) inputs/outputs
//	PUT    /api/apps/{appId}               → patch meta
//	DELETE /api/apps/{appId}               → delete app
//	POST   /api/apps/{appId}/publish       → snapshot from sourceThread
//	GET    /api/apps/{appId}/versions      → list versions
//	POST   /api/apps/{appId}/run           → start a run, returns runId
//	GET    /api/apps/{appId}/runs/{runId}  → poll run status (proxies canvas tracker)
//
// In multi-tenant mode every URL is prefixed by the project id so the scope
// can be resolved without a separate handshake:
//
//	GET    /api/apps/{projectId}                       → list
//	POST   /api/apps/{projectId}                       → create
//	GET    /api/apps/{projectId}/{appId}               → meta
//	... and so on, with every {appId} segment shifted right by one.
//
// Auth is enforced upstream by AuthManager.Middleware; we resolve the project
// scope here and inject it into the request context for downstream callers.
//
// @Summary Apps REST API dispatcher
// @Description Entry point for the apps REST API. Sub-dispatches by method and path to list, create, get, update, delete, publish, run, and manage API keys and share tokens.
// @Tags apps
// @Accept json
// @Produce json
// @Router /api/apps/ [get]
func (s *Server) handleAppsREST(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, appsRESTPath)
	rest = strings.Trim(rest, "/")
	var parts []string
	if rest != "" {
		parts = strings.Split(rest, "/")
	}

	// In multi-tenant mode the first segment is {projectId}; resolve scope
	// and strip it before falling through to the legacy router.
	if s.handler.projects != nil {
		if len(parts) == 0 {
			http.Error(w, "missing projectId", http.StatusBadRequest)
			return
		}
		// Peek at the first segment: if it is "public" this is an
		// unauthenticated share-token call with no project prefix.
		if parts[0] == "public" {
			s.handleAppsPublic(w, r, parts)
			return
		}
		projectID := parts[0]
		// Anonymous paths under the multi-tenant prefix:
		//   /api/apps/{projectId}/public/{token}/...   (share-token)
		//   /api/apps/{projectId}/{appId}/run|runs/... (Bearer API key)
		// Both must skip the cookie-user membership check or the dispatcher
		// returns 401 before the per-handler auth ever runs. We synthesize a
		// project.Scope rooted at the URL projectId so pathsFor downstream
		// resolves the correct per-project data root; the handler validates
		// the actual credential.
		anonShare := len(parts) > 1 && parts[1] == "public"
		anonBearer := UserFromContext(r.Context()) == "" && hasBearerAPIKey(r)
		var ctx context.Context
		if anonShare || anonBearer {
			ctx = s.handler.bearerProjectScope(r.Context(), projectID)
		} else {
			c, err := s.handler.resolveRESTScope(r.Context(), UserFromContext(r.Context()), projectID)
			if err != nil {
				status := http.StatusForbidden
				switch {
				case errors.Is(err, errRESTAuthRequired):
					status = http.StatusUnauthorized
				case errors.Is(err, errRESTProjectMissing):
					status = http.StatusBadRequest
				}
				http.Error(w, err.Error(), status)
				return
			}
			ctx = c
		}
		r = r.WithContext(ctx)
		parts = parts[1:]
		// After consuming the projectId, check for the multi-tenant public path:
		// /api/apps/{projectId}/public/{token}/...
		if len(parts) > 0 && parts[0] == "public" {
			s.handleAppsPublic(w, r, parts)
			return
		}
	} else {
		// Single-project mode: /api/apps/public/{token}/...
		if len(parts) > 0 && parts[0] == "public" {
			s.handleAppsPublic(w, r, parts)
			return
		}
	}

	// Collection-level: GET (list) or POST (create) /api/apps[/]
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			s.handleAppsList(w, r)
		case http.MethodPost:
			s.handleAppsCreate(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	appID := parts[0]
	// /api/apps/{appId} — bare meta CRUD
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.handleAppsGet(w, r, appID)
		case http.MethodPut:
			s.handleAppsUpdate(w, r, appID)
		case http.MethodDelete:
			s.handleAppsDelete(w, r, appID)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// /api/apps/{appId}/{action}[/...]
	action := parts[1]
	switch action {
	case "publish":
		s.handleAppsPublish(w, r, appID)
	case "versions":
		s.handleAppsVersions(w, r, appID)
	case "run":
		s.handleAppsRun(w, r, appID)
	case "runs":
		// /api/apps/{appId}/runs/{runId}            → GET status
		// /api/apps/{appId}/runs/{runId}/cancel     → POST cancel
		if len(parts) == 4 && parts[3] == "cancel" {
			s.handleAppsRunCancel(w, r, appID, parts[2])
			return
		}
		s.handleAppsRunStatus(w, r, appID, parts[2:])
	case "published-version":
		s.handleAppsSetPublishedVersion(w, r, appID)
	case "keys":
		switch len(parts) {
		case 2:
			s.handleAppsKeysCollection(w, r, appID)
		case 3:
			s.handleAppsKeysItem(w, r, appID, parts[2])
		case 4:
			if parts[3] == "rotate" {
				s.handleAppsKeysRotate(w, r, appID, parts[2])
				return
			}
			http.Error(w, "unknown keys subaction: "+parts[3], http.StatusNotFound)
		default:
			http.Error(w, "unknown keys path", http.StatusNotFound)
		}
	case "share":
		if len(parts) == 2 {
			s.handleAppsShareCollection(w, r, appID)
		} else {
			s.handleAppsShareItem(w, r, appID, parts[2])
		}
	default:
		http.Error(w, "unknown apps action: "+action, http.StatusNotFound)
	}
}

// classifyAppsError maps an apps package error to (status, public message).
// 4xx covers sentinel errors and validation failures whose strings are safe
// to surface verbatim. Anything else is 500 with the original error returned
// as the second result so callers can decide whether to log or echo it.
func classifyAppsError(err error) (status int, msg string) {
	switch {
	case errors.Is(err, apps.ErrAppNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, apps.ErrNotPublished):
		return http.StatusConflict, err.Error()
	case errors.Is(err, apps.ErrInvalidAppID):
		return http.StatusBadRequest, err.Error()
	}
	// ValidateInputs returns a plain fmt.Errorf wrapping "app inputs
	// invalid: ..." (see pkg/apps/extract.go). Match by substring because
	// it is not a sentinel; same logic for the publish-time validation
	// strings emitted by Store.PublishVersion.
	m := err.Error()
	if strings.Contains(m, "app inputs invalid") ||
		strings.Contains(m, "canvas has no appInput nodes") ||
		strings.Contains(m, "canvas has no appOutput nodes") ||
		strings.Contains(m, "version not found") {
		return http.StatusUnprocessableEntity, m
	}
	return http.StatusInternalServerError, m
}

// writeAppsError maps apps package sentinel errors and validation failures
// to appropriate HTTP status codes. All other errors bubble up as 500 with
// the underlying error message — fine for cookie-authenticated admins.
func writeAppsError(w http.ResponseWriter, err error) {
	status, msg := classifyAppsError(err)
	http.Error(w, msg, status)
}

// writeAppsErrorPublic is the anonymous-share variant: 4xx responses keep
// their public-safe message, 5xx responses are redacted to a stable
// "internal error" body with an 8-char ref tag that is also logged with the
// original error so operators can correlate the report. Avoids leaking
// internal paths, model IDs, or stack fragments to the public internet.
func writeAppsErrorPublic(w http.ResponseWriter, err error, logger *slog.Logger, where string) {
	status, msg := classifyAppsError(err)
	if status < 500 {
		http.Error(w, msg, status)
		return
	}
	ref := publicErrorRef()
	if logger != nil {
		logger.Error("apps public 5xx", "where", where, "ref", ref, "error", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "internal error",
		"ref":   ref,
	})
}

// publicErrorRef returns an 8-char hex tag identifying a single 5xx so the
// client report ("got internal error ref ab12cd34") can be grep'd against
// the server log line that recorded the underlying cause.
func publicErrorRef() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read on Linux's getrandom never fails in practice; if it
		// somehow does, fall back to a clock-derived tag — still unique
		// enough to correlate within a 1ns window.
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}