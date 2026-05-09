package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// --- Cron RPC handlers ---

func (h *Handler) handleCronList(req Request) Response {
	if h.cronStore == nil {
		return h.internalError(req.ID, "cron not initialized")
	}
	jobs := h.cronStore.List()
	return h.success(req.ID, map[string]any{"jobs": jobs})
}

func (h *Handler) handleCronAdd(req Request) Response {
	if h.cronStore == nil {
		return h.internalError(req.ID, "cron not initialized")
	}
	raw, err := json.Marshal(req.Params)
	if err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	var job CronJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return h.invalidParams(req.ID, "invalid job: "+err.Error())
	}

	created, err := h.cronStore.Add(&job)
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}

	// Compute initial next run if enabled.
	if created.Enabled && h.scheduler != nil {
		h.scheduler.refreshNextRuns()
	}

	return h.success(req.ID, created)
}

func (h *Handler) handleCronUpdate(req Request) Response {
	if h.cronStore == nil {
		return h.internalError(req.ID, "cron not initialized")
	}
	id, _ := req.Params["id"].(string)
	if id == "" {
		return h.invalidParams(req.ID, "id is required")
	}

	raw, err := json.Marshal(req.Params)
	if err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	var patch CronJobPatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		return h.invalidParams(req.ID, "invalid patch: "+err.Error())
	}

	updated, err := h.cronStore.Update(id, patch)
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}

	// Refresh next runs if schedule or enabled state changed.
	if h.scheduler != nil {
		h.scheduler.refreshNextRuns()
	}

	return h.success(req.ID, updated)
}

func (h *Handler) handleCronRemove(req Request) Response {
	if h.cronStore == nil {
		return h.internalError(req.ID, "cron not initialized")
	}
	id, _ := req.Params["id"].(string)
	if id == "" {
		return h.invalidParams(req.ID, "id is required")
	}
	if !h.cronStore.Remove(id) {
		return h.invalidParams(req.ID, "job not found")
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleCronToggle(req Request) Response {
	if h.cronStore == nil {
		return h.internalError(req.ID, "cron not initialized")
	}
	id, _ := req.Params["id"].(string)
	if id == "" {
		return h.invalidParams(req.ID, "id is required")
	}
	enabled, ok := req.Params["enabled"].(bool)
	if !ok {
		return h.invalidParams(req.ID, "enabled (bool) is required")
	}

	patch := CronJobPatch{Enabled: &enabled}
	updated, err := h.cronStore.Update(id, patch)
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}

	if h.scheduler != nil {
		h.scheduler.refreshNextRuns()
	}

	return h.success(req.ID, updated)
}

func (h *Handler) handleCronRun(req Request) Response {
	if h.scheduler == nil {
		return h.internalError(req.ID, "scheduler not initialized")
	}
	id, _ := req.Params["id"].(string)
	if id == "" {
		return h.invalidParams(req.ID, "id is required")
	}
	if err := h.scheduler.RunJobNow(id); err != nil {
		return h.invalidParams(req.ID, err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleCronRuns(req Request) Response {
	if h.cronStore == nil {
		return h.internalError(req.ID, "cron not initialized")
	}
	jobID, _ := req.Params["jobId"].(string)
	limit := 20
	if l, ok := req.Params["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	var runs []*CronRun
	var err error
	if jobID != "" {
		runs, err = h.cronStore.ListRuns(jobID, limit)
	} else {
		runs, err = h.cronStore.ListAllRuns(limit)
	}
	if err != nil {
		return h.internalError(req.ID, "list runs: "+err.Error())
	}
	if runs == nil {
		runs = []*CronRun{}
	}
	return h.success(req.ID, map[string]any{"runs": runs})
}

func (h *Handler) handleCronStatus(req Request) Response {
	if h.scheduler == nil {
		return h.internalError(req.ID, "scheduler not initialized")
	}
	return h.success(req.ID, h.scheduler.Status())
}

// --- Active turns handler ---

func (h *Handler) handleTurnsActive(req Request) Response {
	if h.tracker == nil {
		return h.success(req.ID, map[string]any{"turns": []any{}})
	}
	turns := h.tracker.List()
	return h.success(req.ID, map[string]any{"turns": turns})
}

// --- Tool schema handler ---

// handleToolSchema returns the JSON schema and available engines for a registered tool.
func (h *Handler) handleToolSchema(req Request) Response {
	toolName, _ := req.Params["toolName"].(string)
	if toolName == "" {
		return h.invalidParams(req.ID, "toolName is required")
	}
	engineName, _ := req.Params["engine"].(string)
	result, err := h.runtime.ToolSchema(toolName, engineName)
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	return h.success(req.ID, result)
}

// --- Direct tool execution handler ---

// handleToolRun directly executes a registered tool by name, bypassing the agent loop.
// Used by canvas nodes for direct media generation (generate_image, generate_video, etc.).
func (h *Handler) handleToolRun(reqCtx context.Context, req Request) Response {
	toolName, _ := req.Params["toolName"].(string)
	if toolName == "" {
		return h.invalidParams(req.ID, "toolName is required")
	}
	params, _ := req.Params["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	nodeID, _ := req.Params["nodeId"].(string)

	taskID := h.taskTracker.Create(toolName, nodeID)
	slog.Info("[tool/run] async start", "tool", toolName, "taskId", taskID)

	// Run in background goroutine with independent context so page refresh
	// doesn't cancel the execution. detachWithScope copies the project Scope
	// from the request ctx so per-project tool paths (media cache, session
	// dirs) keep routing correctly instead of falling back to the legacy root.
	go func() {
		start := time.Now()
		bgCtx, cancel := context.WithTimeout(detachWithScope(reqCtx, context.Background()), 10*time.Minute)
		defer cancel()

		result, err := h.runtime.ExecuteTool(bgCtx, toolName, params)
		elapsed := time.Since(start)

		if err != nil {
			slog.Error("[tool/run] failed", "tool", toolName, "taskId", taskID, "elapsed", elapsed, "error", err)
			h.taskTracker.Fail(taskID, err.Error())
			return
		}

		slog.Info("[tool/run] completed", "tool", toolName, "taskId", taskID, "success", result.Success, "elapsed", elapsed, "output_len", len(result.Output), "structured", result.Structured != nil)

		resp := map[string]any{
			"success": result.Success,
			"output":  result.Output,
		}
		if result.Structured != nil {
			resp["structured"] = result.Structured
		}
		if !result.Success {
			h.taskTracker.Fail(taskID, result.Output)
		} else {
			h.taskTracker.Complete(taskID, resp)
		}
	}()

	return h.success(req.ID, map[string]any{"taskId": taskID})
}

func (h *Handler) handleToolTaskStatus(req Request) Response {
	taskID, _ := req.Params["taskId"].(string)
	if taskID == "" {
		return h.invalidParams(req.ID, "taskId is required")
	}
	task, ok := h.taskTracker.Get(taskID)
	if !ok {
		return h.invalidParams(req.ID, "task not found")
	}
	return h.success(req.ID, task)
}

func (h *Handler) handleToolActiveTasks(req Request) Response {
	tasks := h.taskTracker.ActiveTasks()
	return h.success(req.ID, map[string]any{"tasks": tasks})
}
