package server

import (
	"context"
	"errors"
	"strings"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/canvas"
	"github.com/cinience/saker/pkg/tool"
)

// canvasRuntimeAdapter satisfies canvas.Runtime by forwarding to the
// existing api.Runtime and exposing the package-private cacheMediaForProject
// helper. Keeping the adapter here means pkg/canvas stays free of any
// pkg/server import — important because pkg/server already imports pkg/api
// and we don't want a cycle.
type canvasRuntimeAdapter struct {
	rt *api.Runtime
}

func (a canvasRuntimeAdapter) ExecuteTool(ctx context.Context, name string, params map[string]any) (*tool.ToolResult, error) {
	return a.rt.ExecuteTool(ctx, name, params)
}

func (a canvasRuntimeAdapter) Run(ctx context.Context, req api.Request) (*api.Response, error) {
	return a.rt.Run(ctx, req)
}

func (a canvasRuntimeAdapter) ProjectRoot() string {
	return a.rt.ProjectRoot()
}

func (a canvasRuntimeAdapter) CacheMedia(_ context.Context, rawURL, mediaType string) (string, string, error) {
	cached, err := cacheMediaForProject(a.rt.ProjectRoot(), rawURL, mediaType)
	if err != nil {
		return "", "", err
	}
	if cached == nil {
		return "", "", errors.New("cache returned nil")
	}
	return cached.Path, cached.URL, nil
}

// ensureCanvasExecutor lazily initialises the executor on first use. We
// build it on demand instead of in newHandler so non-canvas servers don't
// pay the tracker-goroutine cost.
func (h *Handler) ensureCanvasExecutor() *canvas.Executor {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.canvasExecutor != nil {
		return h.canvasExecutor
	}
	h.canvasExecutor = &canvas.Executor{
		Runtime: canvasRuntimeAdapter{rt: h.runtime},
		DataDir: h.dataDir,
		Tracker: canvas.NewRunTracker(),
		Notify: func(threadID, method string, params map[string]any) {
			h.notifySubscribers(threadID, method, params)
		},
		Logger: h.logger,
	}
	return h.canvasExecutor
}

// handleCanvasExecute starts a canvas run. Returns a runId that callers
// poll via canvas/run-status or cancel via canvas/run-cancel. Mirrors the
// shape of tool/run, which also returns a taskId.
func (h *Handler) handleCanvasExecute(ctx context.Context, req Request) Response {
	threadID, _ := req.Params["threadId"].(string)
	if strings.TrimSpace(threadID) == "" {
		return h.invalidParams(req.ID, "threadId is required")
	}
	skipDone, _ := req.Params["skipDone"].(bool)

	var nodeIDs []string
	if raw, ok := req.Params["nodeIds"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				nodeIDs = append(nodeIDs, s)
			}
		}
	}

	// Acquire pins the per-project executor in the registry so the sweep
	// cannot evict it (and tear down the Tracker) while the async run is
	// still in flight. The pin is released by the canvas/run-finished
	// notification handler in per_project_components.go.
	exec, release := h.canvasExecutorAcquireFor(ctx)
	runID, err := exec.RunAsync(detachWithScope(ctx, context.Background()), canvas.RunOptions{
		ThreadID: threadID,
		NodeIDs:  nodeIDs,
		SkipDone: skipDone,
	})
	if err != nil {
		release()
		return h.invalidParams(req.ID, err.Error())
	}
	h.registerCanvasRunPin(runID, release)
	return h.success(req.ID, map[string]any{
		"runId":  runID,
		"status": canvas.RunStatusRunning,
	})
}

// handleCanvasRunStatus returns the current RunSummary for a runId.
func (h *Handler) handleCanvasRunStatus(ctx context.Context, req Request) Response {
	runID, _ := req.Params["runId"].(string)
	if strings.TrimSpace(runID) == "" {
		return h.invalidParams(req.ID, "runId is required")
	}
	exec := h.canvasExecutorFor(ctx)
	summary, ok := exec.Tracker.Get(runID)
	if !ok {
		return h.invalidParams(req.ID, "run not found")
	}
	return h.success(req.ID, summary)
}

// handleCanvasRunCancel triggers the cancellation func registered when the
// run started. Returns false when the run is unknown or already terminal.
func (h *Handler) handleCanvasRunCancel(ctx context.Context, req Request) Response {
	runID, _ := req.Params["runId"].(string)
	if strings.TrimSpace(runID) == "" {
		return h.invalidParams(req.ID, "runId is required")
	}
	exec := h.canvasExecutorFor(ctx)
	if !exec.Tracker.Cancel(runID) {
		return h.invalidParams(req.ID, "run not found or already finished")
	}
	return h.success(req.ID, map[string]any{"ok": true})
}
