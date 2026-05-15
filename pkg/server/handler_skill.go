package server

import (
	"github.com/saker-ai/saker/pkg/api"
)

func (h *Handler) handleSkillList(req Request) Response {
	skills := h.runtime.AvailableSkills()
	if skills == nil {
		skills = []api.AvailableSkill{}
	}
	return h.success(req.ID, map[string]any{"skills": skills})
}

func (h *Handler) handleSkillRemove(req Request) Response {
	name, _ := req.Params["name"].(string)
	if name == "" {
		return h.invalidParams(req.ID, "name is required")
	}
	if err := h.runtime.RemoveLearnedSkill(name); err != nil {
		return h.invalidParams(req.ID, err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleSkillPromote(req Request) Response {
	name, _ := req.Params["name"].(string)
	if name == "" {
		return h.invalidParams(req.ID, "name is required")
	}
	if err := h.runtime.PromoteLearnedSkill(name); err != nil {
		return h.invalidParams(req.ID, err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleSkillContent(req Request) Response {
	name, _ := req.Params["name"].(string)
	if name == "" {
		return h.invalidParams(req.ID, "name is required")
	}
	result, err := h.runtime.SkillContent(name)
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}
	return h.success(req.ID, result)
}

func (h *Handler) handleSkillPatch(req Request) Response {
	name, _ := req.Params["name"].(string)
	if name == "" {
		return h.invalidParams(req.ID, "name is required")
	}
	oldText, _ := req.Params["old_text"].(string)
	newText, _ := req.Params["new_text"].(string)
	if oldText == "" {
		return h.invalidParams(req.ID, "old_text is required")
	}
	replaceAll, _ := req.Params["replace_all"].(bool)
	if err := h.runtime.PatchLearnedSkill(name, oldText, newText, replaceAll); err != nil {
		return h.invalidParams(req.ID, err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleSkillAnalytics(req Request) Response {
	stats := h.runtime.SkillAnalyticsData()
	return h.success(req.ID, stats)
}

func (h *Handler) handleSkillAnalyticsHistory(req Request) Response {
	name, _ := req.Params["name"].(string)
	limit := 50
	if l, ok := req.Params["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	history := h.runtime.SkillActivationHistory(name, limit)
	return h.success(req.ID, map[string]any{"history": history})
}
