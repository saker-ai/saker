package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cinience/saker/pkg/canvas"
)

// canvasRESTPath is the URL prefix mounted onto the HTTP mux. Anything
// under it routes through handleCanvasREST which sub-dispatches by method
// + path. This keeps server.go's route table small.
const canvasRESTPath = "/api/canvas/"

// handleCanvasREST is the entry point for the canvas REST API.
//
// When no project store is wired in (embedded library mode) the URL shape is:
//
//	POST /api/canvas/{threadId}/execute       → start a run
//	GET  /api/canvas/{threadId}/document      → read canvas JSON
//	GET  /api/canvas/runs/{runId}             → poll status
//	POST /api/canvas/runs/{runId}/cancel      → cancel mid-run
//
// In multi-tenant mode every URL is prefixed by the project id so the scope
// can be resolved without a separate handshake:
//
//	POST /api/canvas/{projectId}/{threadId}/execute
//	GET  /api/canvas/{projectId}/{threadId}/document
//	GET  /api/canvas/{projectId}/runs/{runId}
//	POST /api/canvas/{projectId}/runs/{runId}/cancel
//
// Auth is enforced upstream by AuthManager.Middleware; we resolve the project
// scope here and inject it into the request context for downstream callers.
//
// @Summary Canvas REST API dispatcher
// @Description Entry point for the canvas REST API. Sub-dispatches by method and path to execute, document, and run-status/cancel operations.
// @Tags canvas
// @Accept json
// @Produce json
// @Router /api/canvas/ [get]
func (s *Server) handleCanvasREST(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, canvasRESTPath)
	rest = strings.Trim(rest, "/")
	if rest == "" {
		http.Error(w, "missing canvas resource", http.StatusBadRequest)
		return
	}
	parts := strings.Split(rest, "/")

	// In multi-tenant mode the first segment is {projectId}; resolve scope
	// and strip it before falling through to the legacy router.
	if s.handler.projects != nil {
		if len(parts) < 2 {
			http.Error(w, "missing projectId or canvas resource", http.StatusBadRequest)
			return
		}
		projectID := parts[0]
		ctx, err := s.handler.resolveRESTScope(r.Context(), UserFromContext(r.Context()), projectID)
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
		r = r.WithContext(ctx)
		parts = parts[1:]
	}

	// /runs/{id}[/cancel]
	if parts[0] == "runs" {
		s.handleCanvasRunREST(w, r, parts[1:])
		return
	}

	if len(parts) < 2 {
		http.Error(w, "missing canvas action", http.StatusBadRequest)
		return
	}
	threadID := parts[0]
	action := parts[1]
	switch action {
	case "execute":
		s.handleCanvasExecuteREST(w, r, threadID)
	case "document":
		s.handleCanvasDocumentREST(w, r, threadID)
	default:
		http.Error(w, "unknown canvas action: "+action, http.StatusNotFound)
	}
}

// @Summary Execute canvas run
// @Description Starts an asynchronous canvas execution run for a given thread. Optionally specify nodeIds and skipDone in the body. Returns the runId and initial status.
// @Tags canvas
// @Accept json
// @Produce json
// @Param threadId path string true "Thread ID"
// @Param body body object false "Optional: {nodeIds: [...], skipDone: bool}"
// @Success 202 {object} map[string]string "runId and status"
// @Failure 400 {string} string "invalid JSON body or thread ID"
// @Failure 405 {string} string "POST required"
// @Router /api/canvas/{threadId}/execute [post]
func (s *Server) handleCanvasExecuteREST(w http.ResponseWriter, r *http.Request, threadID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	body := struct {
		NodeIDs  []string `json:"nodeIds,omitempty"`
		SkipDone bool     `json:"skipDone,omitempty"`
	}{}
	// Empty body is fine — defaults run all nodes.
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	exec := s.handler.canvasExecutorFor(r.Context())
	runID, err := exec.RunAsync(r.Context(), canvas.RunOptions{
		ThreadID: threadID,
		NodeIDs:  body.NodeIDs,
		SkipDone: body.SkipDone,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"runId":  runID,
		"status": canvas.RunStatusRunning,
	})
}

// @Summary Canvas run status and cancel
// @Description GET polls the status of a canvas run by runId. POST cancels a running canvas execution.
// @Tags canvas
// @Accept json
// @Produce json
// @Param runId path string true "Run ID"
// @Success 200 {object} map[string]string "Run summary or cancel confirmation"
// @Failure 404 {string} string "run not found or already finished"
// @Failure 405 {string} string "method not allowed"
// @Router /api/canvas/runs/{runId} [get]
// @Router /api/canvas/runs/{runId}/cancel [post]
func (s *Server) handleCanvasRunREST(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		http.Error(w, "missing runId", http.StatusBadRequest)
		return
	}
	runID := parts[0]
	exec := s.handler.canvasExecutorFor(r.Context())

	// /runs/{id}/cancel
	if len(parts) >= 2 && parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if !exec.Tracker.Cancel(runID) {
			http.Error(w, "run not found or already finished", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	// /runs/{id}
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	summary, ok := exec.Tracker.Get(runID)
	if !ok {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// @Summary Get canvas document
// @Description Loads and returns the canvas JSON document for a given thread ID.
// @Tags canvas
// @Produce json
// @Param threadId path string true "Thread ID"
// @Success 200 {object} map[string]any "Canvas document JSON"
// @Failure 400 {string} string "invalid thread ID or load error"
// @Failure 405 {string} string "GET required"
// @Router /api/canvas/{threadId}/document [get]
func (s *Server) handleCanvasDocumentREST(w http.ResponseWriter, r *http.Request, threadID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	// pathsFor returns CanvasDir as <root>/canvas, so we hand canvas.Load
	// the root and let it append the "canvas" segment itself. In legacy
	// mode the root is s.opts.DataDir; in multi-tenant mode it is
	// <dataDir>/projects/<projectId>/.
	root := s.handler.pathsFor(r.Context()).Root
	doc, err := canvas.Load(root, threadID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
