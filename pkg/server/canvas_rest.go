package server

import (
	"encoding/json"
	"net/http"

	"github.com/cinience/saker/pkg/canvas"
	"github.com/gin-gonic/gin"
)

// Routes for the canvas REST API are mounted at /api/canvas/ and registered
// onto the gin engine in registerCanvasRoutes (gin_routes_canvas.go).
// Per-handler bodies live in this file as gin.HandlerFunc.

// handleCanvasExecuteREST starts an asynchronous canvas execution run for the
// thread identified by the :threadId path parameter.
//
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
func (s *Server) handleCanvasExecuteREST(c *gin.Context) {
	r := c.Request
	w := c.Writer
	threadID := c.Param("threadId")
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

// handleCanvasRunStatusREST returns the run summary for the :runId path
// parameter. GET only — Cancel is handled by handleCanvasRunCancelREST.
//
// @Summary Canvas run status
// @Description GET polls the status of a canvas run by runId.
// @Tags canvas
// @Accept json
// @Produce json
// @Param runId path string true "Run ID"
// @Success 200 {object} map[string]string "Run summary"
// @Failure 404 {string} string "run not found"
// @Failure 405 {string} string "method not allowed"
// @Router /api/canvas/runs/{runId} [get]
func (s *Server) handleCanvasRunStatusREST(c *gin.Context) {
	r := c.Request
	w := c.Writer
	runID := c.Param("runId")
	if runID == "" {
		http.Error(w, "missing runId", http.StatusBadRequest)
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

// handleCanvasRunCancelREST cancels a running canvas execution.
//
// @Summary Cancel canvas run
// @Description POST cancels a running canvas execution.
// @Tags canvas
// @Accept json
// @Produce json
// @Param runId path string true "Run ID"
// @Success 200 {object} map[string]string "Cancel confirmation"
// @Failure 404 {string} string "run not found or already finished"
// @Router /api/canvas/runs/{runId}/cancel [post]
func (s *Server) handleCanvasRunCancelREST(c *gin.Context) {
	r := c.Request
	w := c.Writer
	runID := c.Param("runId")
	if runID == "" {
		http.Error(w, "missing runId", http.StatusBadRequest)
		return
	}
	exec := s.handler.canvasExecutorFor(r.Context())
	if !exec.Tracker.Cancel(runID) {
		http.Error(w, "run not found or already finished", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCanvasDocumentREST loads and returns the canvas JSON document for a
// given thread.
//
// @Summary Get canvas document
// @Description Loads and returns the canvas JSON document for a given thread ID.
// @Tags canvas
// @Produce json
// @Param threadId path string true "Thread ID"
// @Success 200 {object} map[string]any "Canvas document JSON"
// @Failure 400 {string} string "invalid thread ID or load error"
// @Failure 405 {string} string "GET required"
// @Router /api/canvas/{threadId}/document [get]
func (s *Server) handleCanvasDocumentREST(c *gin.Context) {
	r := c.Request
	w := c.Writer
	threadID := c.Param("threadId")
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
