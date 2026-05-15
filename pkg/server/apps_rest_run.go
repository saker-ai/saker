package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/saker-ai/saker/pkg/canvas"
	"github.com/gin-gonic/gin"
)

// handleAppsRun validates inputs and dispatches a canvas run. Returns the
// runID with 202 Accepted, mirroring the canvas execute REST contract.
//
// Authentication: cookie auth (set by auth.Middleware) takes priority. When
// no cookie session is present the handler falls back to Bearer API-key auth
// — isPublicPath already allowed the request through the cookie gate when the
// header matched the ak_ pattern.
//
// @Summary Run app
// @Description Validates inputs and dispatches a canvas run. Supports cookie auth and Bearer API-key auth. Returns runId with 202 Accepted.
// @Tags apps
// @Accept json
// @Produce json
// @Param appId path string true "App ID"
// @Param body body object false "Optional: {inputs: {key: value}}"
// @Param Authorization header string false "Bearer API key (ak_...)"
// @Success 202 {object} map[string]string "runId and status"
// @Failure 400 {string} string "invalid JSON body or inputs"
// @Failure 401 {string} string "authentication required"
// @Failure 405 {string} string "POST required"
// @Router /api/apps/{appId}/run [post]
func (s *Server) handleAppsRun(c *gin.Context) {
	r := c.Request
	w := c.Writer
	appID := c.Param("appId")
	// Bearer API-key auth fallback: only runs when the cookie middleware did
	// NOT establish a user (no session cookie) AND the caller supplied an
	// Authorization header. Requests with neither (e.g. no-auth deployments
	// or test servers) pass through so the legacy behaviour is preserved.
	if UserFromContext(r.Context()) == "" && r.Header.Get("Authorization") != "" {
		if !s.appsAuthBearer(c, appID) {
			return
		}
	}
	body := struct {
		Inputs map[string]any `json:"inputs"`
	}{}
	// Empty body is fine — apps with no required inputs can run with no
	// payload. Tolerate zero-length requests by skipping decode.
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if body.Inputs == nil {
		body.Inputs = map[string]any{}
	}
	runner := s.handler.appsRunnerFor(r.Context())
	runID, err := runner.Run(r.Context(), appID, body.Inputs)
	if err != nil {
		writeAppsError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"runId":  runID,
		"status": canvas.RunStatusRunning,
	})
}

// handleAppsRunStatus proxies the canvas RunTracker for a previously
// dispatched run. The runID is opaque to apps — the canvas executor owns
// the lifecycle. Path shape: /api/apps/{appId}/runs/{runId}.
//
// @Summary Get app run status
// @Description Proxies the canvas RunTracker for a previously dispatched run. Supports Bearer API-key auth.
// @Tags apps
// @Produce json
// @Param appId path string true "App ID"
// @Param runId path string true "Run ID"
// @Param Authorization header string false "Bearer API key (ak_...)"
// @Success 200 {object} map[string]any "Run status summary"
// @Failure 400 {string} string "missing runId"
// @Failure 401 {string} string "authentication required"
// @Failure 404 {string} string "run not found"
// @Failure 405 {string} string "GET required"
// @Router /api/apps/{appId}/runs/{runId} [get]
func (s *Server) handleAppsRunStatus(c *gin.Context) {
	r := c.Request
	w := c.Writer
	appID := c.Param("appId")
	runID := c.Param("runId")
	if strings.TrimSpace(runID) == "" {
		http.Error(w, "missing runId", http.StatusBadRequest)
		return
	}
	// Bearer API-key auth fallback (mirrors handleAppsRun).
	if UserFromContext(r.Context()) == "" && r.Header.Get("Authorization") != "" {
		if !s.appsAuthBearer(c, appID) {
			return
		}
	}
	exec := s.handler.canvasExecutorFor(r.Context())
	summary, ok := exec.Tracker.Get(runID)
	if !ok {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// handleAppsRunCancel interrupts an in-flight run via the canvas tracker.
// Returns 200 on success, 404 when the run is unknown, 409 when it has
// already reached a terminal state (cancelling a finished run is a no-op
// the caller should treat as success — but we surface it for telemetry).
//
// @Summary Cancel app run
// @Description Interrupts an in-flight canvas run. Supports Bearer API-key auth. Returns 200 on success, 404 when run is unknown.
// @Tags apps
// @Produce json
// @Param appId path string true "App ID"
// @Param runId path string true "Run ID"
// @Param Authorization header string false "Bearer API key (ak_...)"
// @Success 200 {object} map[string]bool "ok: true"
// @Failure 400 {string} string "missing runId"
// @Failure 401 {string} string "authentication required"
// @Failure 404 {string} string "run not found"
// @Failure 405 {string} string "POST required"
// @Router /api/apps/{appId}/runs/{runId}/cancel [post]
func (s *Server) handleAppsRunCancel(c *gin.Context) {
	r := c.Request
	w := c.Writer
	appID := c.Param("appId")
	runID := c.Param("runId")
	// Bearer API-key auth fallback (mirrors handleAppsRun).
	if UserFromContext(r.Context()) == "" && r.Header.Get("Authorization") != "" {
		if !s.appsAuthBearer(c, appID) {
			return
		}
	}
	if strings.TrimSpace(runID) == "" {
		http.Error(w, "missing runId", http.StatusBadRequest)
		return
	}
	exec := s.handler.canvasExecutorFor(r.Context())
	if _, ok := exec.Tracker.Get(runID); !ok {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if !exec.Tracker.Cancel(runID) {
		// Already terminal — treat as idempotent success so the UI can
		// stop polling without surfacing a confusing error.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "alreadyTerminal": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAppsPublicRun is the share-token variant of handleAppsRun; called
// from handleAppsPublic after the token has been validated.
func (s *Server) handleAppsPublicRun(w http.ResponseWriter, r *http.Request, appID string) {
	body := struct {
		Inputs map[string]any `json:"inputs"`
	}{}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
	}
	if body.Inputs == nil {
		body.Inputs = map[string]any{}
	}
	runner := s.handler.appsRunnerFor(r.Context())
	runID, err := runner.Run(r.Context(), appID, body.Inputs)
	if err != nil {
		writeAppsErrorPublic(w, err, s.logger, "public/run")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"runId":  runID,
		"status": canvas.RunStatusRunning,
	})
}

// handleAppsPublicRunStatus is the share-token variant of handleAppsRunStatus;
// called from handleAppsPublic after the token has been validated.
func (s *Server) handleAppsPublicRunStatus(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	exec := s.handler.canvasExecutorFor(r.Context())
	summary, ok := exec.Tracker.Get(runID)
	if !ok {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// handleAppsPublicRunCancel mirrors handleAppsRunCancel for share-token
// callers. The token has already been validated by handleAppsPublic, so we
// only need to interrupt the tracker entry. Anonymous callers can cancel
// runs they started; the token holder is the de-facto owner of the run.
func (s *Server) handleAppsPublicRunCancel(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if strings.TrimSpace(runID) == "" {
		http.Error(w, "missing runId", http.StatusBadRequest)
		return
	}
	exec := s.handler.canvasExecutorFor(r.Context())
	if _, ok := exec.Tracker.Get(runID); !ok {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if !exec.Tracker.Cancel(runID) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "alreadyTerminal": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
