package server

import (
	"strings"
)

// handleMemoryList returns all memory entries.
func (h *Handler) handleMemoryList(req Request) Response {
	store := h.runtime.MemoryStore()
	if store == nil {
		return h.success(req.ID, map[string]any{"entries": []any{}})
	}
	entries, err := store.List()
	if err != nil {
		return h.internalError(req.ID, "failed to list memory: "+err.Error())
	}
	result := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		result = append(result, map[string]any{
			"name":        e.Name,
			"description": e.Description,
			"type":        string(e.Type),
			"content":     e.Content,
			"filepath":    e.FilePath,
			"mod_time":    e.ModTime.Format("2006-01-02T15:04:05Z"),
		})
	}
	return h.success(req.ID, map[string]any{"entries": result})
}

// handleMemoryRead returns a single memory entry by name.
func (h *Handler) handleMemoryRead(req Request) Response {
	store := h.runtime.MemoryStore()
	if store == nil {
		return h.internalError(req.ID, "memory store not configured")
	}
	name, _ := req.Params["name"].(string)
	if strings.TrimSpace(name) == "" {
		return h.invalidParams(req.ID, "name is required")
	}
	entry, err := store.Load(name)
	if err != nil {
		return h.internalError(req.ID, "failed to read memory: "+err.Error())
	}
	return h.success(req.ID, map[string]any{
		"name":        entry.Name,
		"description": entry.Description,
		"type":        string(entry.Type),
		"content":     entry.Content,
		"filepath":    entry.FilePath,
		"mod_time":    entry.ModTime.Format("2006-01-02T15:04:05Z"),
	})
}

// handleMemoryDelete removes a memory entry by name.
func (h *Handler) handleMemoryDelete(req Request) Response {
	store := h.runtime.MemoryStore()
	if store == nil {
		return h.internalError(req.ID, "memory store not configured")
	}
	name, _ := req.Params["name"].(string)
	if strings.TrimSpace(name) == "" {
		return h.invalidParams(req.ID, "name is required")
	}
	if err := store.Delete(name); err != nil {
		return h.internalError(req.ID, "failed to delete memory: "+err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}
