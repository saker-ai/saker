package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/apps"
	"github.com/gin-gonic/gin"
)

// ── Share-token management ────────────────────────────────────────────────────

// apiShareToken is the wire shape for listing: exposes a tokenPreview instead
// of the full token.
type apiShareToken struct {
	TokenPreview string     `json:"tokenPreview"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	RateLimit    int        `json:"rateLimit,omitempty"`
}

// handleAppsShareCollection handles GET and POST on /api/apps/{appId}/share.
//
// @Summary List share tokens
// @Description Returns every share token registered for an app, with the token value masked as a tokenPreview.
// @Tags apps-share
// @Produce json
// @Param appId path string true "App ID"
// @Success 200 {array} apiShareToken "Share tokens (preview only)"
// @Failure 404 {string} string "app not found"
// @Router /api/apps/{appId}/share [get]
//
// @Summary Create share token
// @Description Generates a new share token for anonymous access to the app's run endpoint. Returns the token value ONCE.
// @Tags apps-share
// @Accept json
// @Produce json
// @Param appId path string true "App ID"
// @Param body body object false "Optional creation payload: {expiresInDays?, rateLimit?}"
// @Success 201 {object} map[string]any "Created token: {token, createdAt, expiresAt, rateLimit}"
// @Failure 400 {string} string "invalid body"
// @Failure 500 {string} string "token generation failed"
// @Router /api/apps/{appId}/share [post]
func (s *Server) handleAppsShareCollection(c *gin.Context) {
	r := c.Request
	w := c.Writer
	appID := c.Param("appId")
	switch r.Method {
	case http.MethodGet:
		store := s.handler.appsStoreFor(r.Context())
		kf, err := store.LoadKeys(r.Context(), appID)
		if err != nil {
			writeAppsError(w, err)
			return
		}
		out := make([]apiShareToken, len(kf.ShareTokens))
		for i, st := range kf.ShareTokens {
			preview := st.Token
			if len(preview) > 6 {
				preview = preview[:6] + "…"
			}
			out[i] = apiShareToken{
				TokenPreview: preview,
				CreatedAt:    st.CreatedAt,
				ExpiresAt:    st.ExpiresAt,
				RateLimit:    st.RateLimit,
			}
		}
		writeJSON(w, http.StatusOK, out)

	case http.MethodPost:
		body := struct {
			ExpiresInDays int `json:"expiresInDays,omitempty"`
			RateLimit     int `json:"rateLimit,omitempty"`
		}{}
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		tok, err := apps.GenerateShareToken()
		if err != nil {
			http.Error(w, "generate token: "+err.Error(), http.StatusInternalServerError)
			return
		}
		now := time.Now().UTC()
		st := apps.ShareToken{
			Token:     tok,
			CreatedAt: now,
			RateLimit: body.RateLimit,
		}
		if body.ExpiresInDays > 0 {
			exp := now.AddDate(0, 0, body.ExpiresInDays)
			st.ExpiresAt = &exp
		}
		store := s.handler.appsStoreFor(r.Context())
		kf, err := store.LoadKeys(r.Context(), appID)
		if err != nil {
			writeAppsError(w, err)
			return
		}
		kf.ShareTokens = append(kf.ShareTokens, st)
		if err := store.SaveKeys(r.Context(), appID, kf); err != nil {
			http.Error(w, "save keys: "+err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"token":     tok,
			"createdAt": st.CreatedAt,
			"expiresAt": st.ExpiresAt,
			"rateLimit": st.RateLimit,
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAppsShareItem handles DELETE on /api/apps/{appId}/share/{token}.
//
// @Summary Revoke share token
// @Description Permanently removes the share token. Anonymous callers using this token stop authenticating immediately.
// @Tags apps-share
// @Produce json
// @Param appId path string true "App ID"
// @Param token path string true "Share token value"
// @Success 200 {object} map[string]bool "{ok: true}"
// @Failure 404 {string} string "share token not found"
// @Router /api/apps/{appId}/share/{token} [delete]
func (s *Server) handleAppsShareItem(c *gin.Context) {
	r := c.Request
	w := c.Writer
	appID := c.Param("appId")
	token := c.Param("token")
	store := s.handler.appsStoreFor(r.Context())
	kf, err := store.LoadKeys(r.Context(), appID)
	if err != nil {
		writeAppsError(w, err)
		return
	}
	found := false
	filtered := kf.ShareTokens[:0]
	for _, st := range kf.ShareTokens {
		if st.Token == token {
			found = true
			continue
		}
		filtered = append(filtered, st)
	}
	if !found {
		http.Error(w, "share token not found", http.StatusNotFound)
		return
	}
	kf.ShareTokens = filtered
	if err := store.SaveKeys(r.Context(), appID, kf); err != nil {
		http.Error(w, "save keys: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── Public share-token endpoints ──────────────────────────────────────────────

// handleAppsPublic sub-dispatches the anonymous share-token API. parts[0] is
// always "public"; parts[1] is the token; parts[2] (if present) is the sub-
// action ("run" or "runs"). Called from the gin adapters in
// gin_routes_apps.go which split the catch-all wildcard into parts.
//
//	GET  …/public/{token}             → app schema
//	POST …/public/{token}/run         → start run
//	GET  …/public/{token}/runs/{runId}→ run status
func (s *Server) handleAppsPublic(w http.ResponseWriter, r *http.Request, parts []string) {
	// parts[0] == "public"
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		http.Error(w, "missing share token", http.StatusBadRequest)
		return
	}
	token := parts[1]

	// Resolve which app owns this share token by scanning every app.
	appID, kf, st := s.resolveShareToken(r.Context(), token)
	if appID == "" {
		http.Error(w, "share token not found", http.StatusNotFound)
		return
	}
	// ValidateShareToken also checks expiry and rate-limits. We need to
	// call it a second time here so the rate-limiter counts public calls.
	if _, ok := apps.ValidateShareToken(kf, token); !ok {
		// Distinguish expired from rate-limited by checking ExpiresAt.
		if st.ExpiresAt != nil && time.Now().After(*st.ExpiresAt) {
			http.Error(w, "share token expired", http.StatusNotFound)
			return
		}
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	// Route by sub-action.
	sub := ""
	if len(parts) >= 3 {
		sub = parts[2]
	}
	switch {
	case sub == "" && r.Method == http.MethodGet:
		s.handleAppsPublicSchema(w, r, appID)
	case sub == "run" && r.Method == http.MethodPost:
		s.handleAppsPublicRun(w, r, appID)
	case sub == "runs" && len(parts) >= 4:
		// /public/{token}/runs/{runId}        → GET status
		// /public/{token}/runs/{runId}/cancel → POST cancel
		if len(parts) >= 5 && parts[4] == "cancel" {
			s.handleAppsPublicRunCancel(w, r, parts[3])
			return
		}
		s.handleAppsPublicRunStatus(w, r, parts[3])
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// resolveShareToken scans all apps in the scope to find which app owns the
// given token. Returns ("", nil, nil) when not found.
func (s *Server) resolveShareToken(ctx context.Context, token string) (appID string, kf *apps.KeysFile, st *apps.ShareToken) {
	store := s.handler.appsStoreFor(ctx)
	metas, err := store.List(ctx)
	if err != nil {
		return "", nil, nil
	}
	for _, meta := range metas {
		k, err := store.LoadKeys(ctx, meta.ID)
		if err != nil {
			continue
		}
		for i := range k.ShareTokens {
			if k.ShareTokens[i].Token == token {
				return meta.ID, k, &k.ShareTokens[i]
			}
		}
	}
	return "", nil, nil
}

// publicSchemaResponse is the stripped-down shape returned to anonymous callers.
type publicSchemaResponse struct {
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Icon        string                `json:"icon,omitempty"`
	Inputs      []apps.AppInputField  `json:"inputs"`
	Outputs     []apps.AppOutputField `json:"outputs"`
}

// handleAppsPublicSchema returns a stripped meta + inputs/outputs schema for
// the published version, accessible only via a valid share token.
//
// @Summary Get public app schema
// @Description Returns the published app's name, description, icon, and inputs/outputs schema for anonymous callers using a share token. No authentication required.
// @Tags apps-public
// @Produce json
// @Param token path string true "Share token"
// @Success 200 {object} publicSchemaResponse "Public app schema"
// @Failure 404 {string} string "share token not found, app not found, or app not published"
// @Failure 429 {string} string "rate limit exceeded"
// @Router /api/apps/public/{token} [get]
func (s *Server) handleAppsPublicSchema(w http.ResponseWriter, r *http.Request, appID string) {
	store := s.handler.appsStoreFor(r.Context())
	meta, err := store.Get(r.Context(), appID)
	if err != nil {
		// Anonymous endpoint — keep "app not found" generic so we don't
		// leak whether the underlying error is permission/path/parse.
		writeAppsErrorPublic(w, err, s.logger, "public/schema/get")
		return
	}
	if meta.PublishedVersion == "" {
		http.Error(w, "app not published", http.StatusNotFound)
		return
	}
	v, err := store.LoadVersion(r.Context(), appID, meta.PublishedVersion)
	if err != nil {
		writeAppsErrorPublic(w, err, s.logger, "public/schema/load-version")
		return
	}
	writeJSON(w, http.StatusOK, publicSchemaResponse{
		Name:        meta.Name,
		Description: meta.Description,
		Icon:        meta.Icon,
		Inputs:      v.Inputs,
		Outputs:     v.Outputs,
	})
}
