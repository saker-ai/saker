package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/conversation"
	"github.com/cinience/saker/pkg/media/embedding"
	"github.com/cinience/saker/pkg/project"
)

func (h *Handler) handleConfigGet(req Request) Response {
	cfg := h.runtime.Config()
	return h.success(req.ID, map[string]any{"config": cfg})
}

func (h *Handler) handleSettingsGet(req Request) Response {
	settings := h.runtime.Settings()
	// Strip password hashes from response — expose username + user list only.
	if settings != nil && settings.WebAuth != nil {
		safe := *settings
		safeAuth := &config.WebAuthConfig{Username: settings.WebAuth.Username}
		// Preserve user list without passwords.
		for _, u := range settings.WebAuth.Users {
			safeAuth.Users = append(safeAuth.Users, config.UserAuth{
				Username: u.Username,
				Disabled: u.Disabled,
			})
		}
		safe.WebAuth = safeAuth
		settings = &safe
	}
	// Redact API keys in aigo providers to prevent credential leaks.
	if settings != nil && settings.Aigo != nil && len(settings.Aigo.Providers) > 0 {
		// Ensure we have our own copy of settings if not already cloned above.
		if settings.WebAuth == nil {
			safe := *settings
			settings = &safe
		}
		aigoCopy := *settings.Aigo
		safeProviders := make(map[string]config.AigoProvider, len(aigoCopy.Providers))
		for k, p := range aigoCopy.Providers {
			if p.APIKey != "" && !strings.Contains(p.APIKey, "${") {
				if len(p.APIKey) > 4 {
					p.APIKey = p.APIKey[:4] + "****"
				} else {
					p.APIKey = "****"
				}
			}
			safeProviders[k] = p
		}
		aigoCopy.Providers = safeProviders
		settings.Aigo = &aigoCopy
	}
	return h.success(req.ID, map[string]any{
		"settings":      settings,
		"tools":         h.runtime.ToolInfos(),
		"embedBackends": embedding.AllBackends(),
	})
}

func (h *Handler) handleSettingsUpdate(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}
	raw, err := json.Marshal(req.Params)
	if err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}

	var patch config.Settings
	if err := json.Unmarshal(raw, &patch); err != nil {
		return h.invalidParams(req.ID, "invalid settings: "+err.Error())
	}

	// Load existing local settings to preserve non-aigo fields.
	projectRoot := h.runtime.ProjectRoot()
	existing, err := config.LoadSettingsLocal(projectRoot)
	if err != nil {
		return h.internalError(req.ID, "load local settings: "+err.Error())
	}

	// Merge patch into existing local settings.
	var merged *config.Settings
	if existing != nil {
		merged = config.MergeSettings(existing, &patch)
	} else {
		merged = &patch
	}

	if err := config.SaveSettingsLocal(projectRoot, merged); err != nil {
		return h.internalError(req.ID, "save settings: "+err.Error())
	}

	// Hot-reload settings into the runtime.
	if err := h.runtime.ReloadSettings(); err != nil {
		return h.internalError(req.ID, "reload settings: "+err.Error())
	}

	// Hot-swap the object store when storage config changed. Failure here
	// keeps the persisted settings (already saved above) but surfaces the
	// error so the admin sees they need to fix the new backend rather than
	// silently running on the previous one.
	if patch.Storage != nil && h.storageReloader != nil {
		if err := h.storageReloader(ctx); err != nil {
			return h.internalError(req.ID, "reload storage: "+err.Error())
		}
	}

	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleStatsSession(req Request) Response {
	var params struct {
		SessionID string `json:"session_id"`
	}
	raw, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(raw, &params); err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	if params.SessionID == "" {
		return h.invalidParams(req.ID, "session_id is required")
	}

	stats := h.runtime.GetSessionStats(params.SessionID)
	if stats == nil {
		return h.success(req.ID, map[string]any{"session_id": params.SessionID, "found": false})
	}
	return h.success(req.ID, stats)
}

func (h *Handler) handleStatsTotal(req Request) Response {
	stats := h.runtime.GetTotalStats()
	if stats == nil {
		return h.success(req.ID, map[string]any{"total_tokens": 0})
	}
	return h.success(req.ID, stats)
}

func (h *Handler) handleSessionsSearch(ctx context.Context, req Request) Response {
	store := h.runtime.ConversationStore()
	if store == nil {
		return h.internalError(req.ID, "conversation store not available")
	}
	projectID := "default"
	if scope, ok := project.FromContext(ctx); ok {
		projectID = scope.ProjectID
	}
	query, _ := req.Params["query"].(string)
	limit := 20
	if v, ok := req.Params["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	hits, err := store.Search(ctx, projectID, query, conversation.SearchOpts{Limit: limit})
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	if hits == nil {
		return h.success(req.ID, []struct{}{})
	}
	return h.success(req.ID, hits)
}

func (h *Handler) handleSessionsList(ctx context.Context, req Request) Response {
	store := h.runtime.ConversationStore()
	if store == nil {
		return h.internalError(req.ID, "conversation store not available")
	}
	projectID := "default"
	if scope, ok := project.FromContext(ctx); ok {
		projectID = scope.ProjectID
	}
	limit := 50
	offset := 0
	if v, ok := req.Params["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	if v, ok := req.Params["offset"].(float64); ok && v >= 0 {
		offset = int(v)
	}
	threads, err := store.ListThreads(ctx, projectID, conversation.ListThreadsOpts{Limit: limit, Offset: offset})
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	if threads == nil {
		return h.success(req.ID, []struct{}{})
	}
	return h.success(req.ID, threads)
}

func (h *Handler) handleModelSwitch(ctx context.Context, req Request) Response {
	modelName, _ := req.Params["model"].(string)
	if strings.TrimSpace(modelName) == "" {
		return h.invalidParams(req.ID, "model name is required")
	}
	if err := h.runtime.SetModel(ctx, modelName); err != nil {
		return h.internalError(req.ID, err.Error())
	}
	return h.success(req.ID, map[string]string{"model": modelName, "status": "switched"})
}

// --- Monitor endpoints ---

func (h *Handler) handleMonitorList(req Request) Response {
	monitors := h.runtime.ListMonitors()
	return h.success(req.ID, map[string]any{"monitors": monitors})
}

func (h *Handler) handleMonitorStart(ctx context.Context, req Request) Response {
	sm := h.runtime.StreamMonitorTool()
	if sm == nil {
		return h.internalError(req.ID, "stream_monitor tool not available")
	}

	// Validate required field
	urlStr, _ := req.Params["url"].(string)
	if strings.TrimSpace(urlStr) == "" {
		return h.invalidParams(req.ID, "url is required")
	}

	// Validate sample_rate range if provided
	if sr, ok := req.Params["sample_rate"]; ok {
		if srFloat, ok := sr.(float64); ok {
			if srFloat < 1 || srFloat > 100 {
				return h.invalidParams(req.ID, "sample_rate must be between 1 and 100")
			}
		}
	}

	result, err := sm.Execute(ctx, map[string]any{
		"action":      "start",
		"url":         req.Params["url"],
		"events":      req.Params["events"],
		"sample_rate": req.Params["sample_rate"],
		"webhook_url": req.Params["webhook_url"],
		"subject":     req.Params["subject"],
	})
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		return h.internalError(req.ID, "failed to parse tool output")
	}
	return h.success(req.ID, out)
}

func (h *Handler) handleMonitorStop(ctx context.Context, req Request) Response {
	sm := h.runtime.StreamMonitorTool()
	if sm == nil {
		return h.internalError(req.ID, "stream_monitor tool not available")
	}

	taskID, _ := req.Params["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return h.invalidParams(req.ID, "task_id is required")
	}

	result, err := sm.Execute(ctx, map[string]any{
		"action":  "stop",
		"task_id": taskID,
	})
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result.Output), &out); err != nil {
		return h.internalError(req.ID, "failed to parse tool output")
	}
	return h.success(req.ID, out)
}

// parseRequest decodes raw JSON into a Request.
func parseRequest(data []byte) (Request, error) {
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return req, err
	}
	if req.JSONRPC != "2.0" {
		return req, fmt.Errorf("invalid jsonrpc version: %s", req.JSONRPC)
	}
	return req, nil
}
