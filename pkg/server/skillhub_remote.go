package server

import (
	"context"
	"time"

	"github.com/saker-ai/saker/pkg/runtime/skills"
	"github.com/saker-ai/saker/pkg/skillhub"
)

// handleSkillhubRemoteEnable enables remote skill loading from a SkillHub
// registry. Skills are fetched into memory without writing to disk.
//
// Params: { "registry": "http://localhost:8080", "slugs": ["a","b"] (optional) }
func (h *Handler) handleSkillhubRemoteEnable(ctx context.Context, req Request) Response {
	registry, _ := req.Params["registry"].(string)
	if registry == "" {
		return h.invalidParams(req.ID, "registry is required")
	}

	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}

	cfg.RemoteMode = true
	cfg.RemoteRegistry = registry
	cfg.RemoteSlugs = extractStringSlice(req.Params, "slugs")
	if err := h.saveSkillhubConfig(cfg); err != nil {
		return h.internalError(req.ID, err.Error())
	}

	h.applyRemoteSkillSources(cfg)

	errs := h.runtime.ReloadSkills()
	errStrs := errorsToStrings(errs)

	return h.success(req.ID, map[string]any{
		"enabled":  true,
		"registry": registry,
		"slugs":    cfg.RemoteSlugs,
		"errors":   errStrs,
	})
}

// handleSkillhubRemoteDisable disables remote skill loading.
func (h *Handler) handleSkillhubRemoteDisable(_ context.Context, req Request) Response {
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}

	cfg.RemoteMode = false
	cfg.RemoteRegistry = ""
	cfg.RemoteSlugs = nil
	if err := h.saveSkillhubConfig(cfg); err != nil {
		return h.internalError(req.ID, err.Error())
	}

	h.clearRemoteSkillSources()
	errs := h.runtime.ReloadSkills()

	return h.success(req.ID, map[string]any{
		"enabled": false,
		"errors":  errorsToStrings(errs),
	})
}

// handleSkillhubRemoteRefresh triggers an immediate re-fetch of remote skills.
func (h *Handler) handleSkillhubRemoteRefresh(ctx context.Context, req Request) Response {
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	resolved := cfg.Resolved()
	if !resolved.RemoteMode {
		return h.invalidParams(req.ID, "remote mode is not enabled")
	}

	h.applyRemoteSkillSources(cfg)
	errs := h.runtime.ReloadSkills()

	available := h.runtime.AvailableSkills()
	remoteCount := 0
	for _, s := range available {
		if s.Scope == string(skills.SkillScopeRemote) {
			remoteCount++
		}
	}

	return h.success(req.ID, map[string]any{
		"refreshed":   true,
		"remoteCount": remoteCount,
		"totalSkills": len(available),
		"errors":      errorsToStrings(errs),
		"refreshedAt": time.Now().Format(time.RFC3339),
	})
}

// handleSkillhubRemoteStatus returns the current remote loading status.
func (h *Handler) handleSkillhubRemoteStatus(_ context.Context, req Request) Response {
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	resolved := cfg.Resolved()

	available := h.runtime.AvailableSkills()
	var remoteNames []string
	for _, s := range available {
		if s.Scope == string(skills.SkillScopeRemote) {
			remoteNames = append(remoteNames, s.Name)
		}
	}

	return h.success(req.ID, map[string]any{
		"enabled":      resolved.RemoteMode,
		"registry":     resolved.RemoteRegistry,
		"slugs":        resolved.RemoteSlugs,
		"remoteCount":  len(remoteNames),
		"remoteSkills": remoteNames,
		"totalSkills":  len(available),
	})
}

// --- helpers ----------------------------------------------------------------

func (h *Handler) applyRemoteSkillSources(cfg skillhub.Config) {
	resolved := cfg.Resolved()
	if !resolved.RemoteMode || resolved.RemoteRegistry == "" {
		h.clearRemoteSkillSources()
		return
	}
	sources := []skills.RemoteSkillSource{{
		Registry: resolved.RemoteRegistry,
		Token:    resolved.Token,
		Slugs:    append([]string(nil), resolved.RemoteSlugs...),
	}}
	h.runtime.SetRemoteSkillSources(sources)
}

func (h *Handler) clearRemoteSkillSources() {
	h.runtime.SetRemoteSkillSources(nil)
}

func extractStringSlice(params map[string]any, key string) []string {
	raw, ok := params[key]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func errorsToStrings(errs []error) []string {
	if len(errs) == 0 {
		return nil
	}
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Error()
	}
	return out
}
