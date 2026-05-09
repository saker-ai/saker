package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cinience/saker/pkg/config"
)

// handlePersonaList returns all configured persona profiles.
// SoulFile and InstructFile paths are scrubbed from the response.
func (h *Handler) handlePersonaList(req Request) Response {
	s := h.runtime.Settings()
	if s == nil || s.Personas == nil {
		return h.success(req.ID, map[string]any{
			"default":  "",
			"profiles": map[string]any{},
			"routes":   []any{},
		})
	}
	// Scrub file paths from profiles to avoid leaking server paths.
	scrubbed := make(map[string]config.PersonaProfile, len(s.Personas.Profiles))
	for id, p := range s.Personas.Profiles {
		p.SoulFile = ""
		p.InstructFile = ""
		scrubbed[id] = p
	}
	return h.success(req.ID, map[string]any{
		"default":  s.Personas.Default,
		"profiles": scrubbed,
		"routes":   s.Personas.Routes,
	})
}

// handlePersonaSave creates or updates a persona profile in settings.json.
func (h *Handler) handlePersonaSave(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}

	id, _ := req.Params["id"].(string)
	if strings.TrimSpace(id) == "" {
		return h.invalidParams(req.ID, "id is required")
	}

	profileData, _ := req.Params["profile"]
	raw, err := json.Marshal(profileData)
	if err != nil {
		return h.invalidParams(req.ID, "invalid profile data")
	}
	var profile config.PersonaProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return h.invalidParams(req.ID, "invalid profile format")
	}

	if err := h.patchPersonas(func(p *config.PersonasConfig) {
		if p.Profiles == nil {
			p.Profiles = map[string]config.PersonaProfile{}
		}
		p.Profiles[id] = profile
	}); err != nil {
		return h.internalError(req.ID, "save persona: "+err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

// handlePersonaDelete removes a persona profile from settings.json.
func (h *Handler) handlePersonaDelete(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}

	id, _ := req.Params["id"].(string)
	if strings.TrimSpace(id) == "" {
		return h.invalidParams(req.ID, "id is required")
	}

	if err := h.patchPersonas(func(p *config.PersonasConfig) {
		delete(p.Profiles, id)
		if p.Default == id {
			p.Default = ""
		}
	}); err != nil {
		return h.internalError(req.ID, "delete persona: "+err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

// handlePersonaSetDefault sets the default persona ID.
func (h *Handler) handlePersonaSetDefault(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}

	id, _ := req.Params["id"].(string)

	if err := h.patchPersonas(func(p *config.PersonasConfig) {
		p.Default = id
	}); err != nil {
		return h.internalError(req.ID, "set default persona: "+err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

// patchPersonas loads existing settings, applies a mutation to the Personas
// section, saves back, and reloads the runtime.
func (h *Handler) patchPersonas(mutate func(p *config.PersonasConfig)) error {
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	projectRoot := h.runtime.ProjectRoot()
	existing, err := config.LoadSettingsLocal(projectRoot)
	if err != nil || existing == nil {
		existing = &config.Settings{}
	}
	if existing.Personas == nil {
		existing.Personas = &config.PersonasConfig{
			Profiles: map[string]config.PersonaProfile{},
		}
	}
	mutate(existing.Personas)

	if err := config.SaveSettingsLocal(projectRoot, existing); err != nil {
		return err
	}
	return h.runtime.ReloadSettings()
}

// handleUserPersonaList returns the current user's persona config (global profiles + user's own).
func (h *Handler) handleUserPersonaList(ctx context.Context, req Request) Response {
	username := UserFromContext(ctx)
	s := h.runtime.Settings()

	// Global profiles (read-only for user).
	globalProfiles := map[string]config.PersonaProfile{}
	globalDefault := ""
	if s != nil && s.Personas != nil {
		globalProfiles = s.Personas.Profiles
		globalDefault = s.Personas.Default
	}

	// User's own profiles and active selection.
	var userActive string
	userProfiles := map[string]config.PersonaProfile{}
	if s != nil && s.UserPersonas != nil {
		if uc, ok := s.UserPersonas[username]; ok && uc != nil {
			userActive = uc.Active
			if uc.Profiles != nil {
				userProfiles = uc.Profiles
			}
		}
	}

	return h.success(req.ID, map[string]any{
		"globalProfiles": globalProfiles,
		"globalDefault":  globalDefault,
		"userProfiles":   userProfiles,
		"active":         userActive,
	})
}

// handleUserPersonaSave saves a persona profile for the current user.
func (h *Handler) handleUserPersonaSave(ctx context.Context, req Request) Response {
	username := UserFromContext(ctx)
	if username == "" {
		return h.invalidParams(req.ID, "not authenticated")
	}

	id, _ := req.Params["id"].(string)
	if strings.TrimSpace(id) == "" {
		return h.invalidParams(req.ID, "id is required")
	}

	profileData, _ := req.Params["profile"]
	raw, err := json.Marshal(profileData)
	if err != nil {
		return h.invalidParams(req.ID, "invalid profile data")
	}
	var profile config.PersonaProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return h.invalidParams(req.ID, "invalid profile format")
	}

	if err := h.patchUserPersonas(username, func(uc *config.UserPersonasConfig) {
		if uc.Profiles == nil {
			uc.Profiles = map[string]config.PersonaProfile{}
		}
		uc.Profiles[id] = profile
	}); err != nil {
		return h.internalError(req.ID, "save user persona: "+err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

// handleUserPersonaDelete deletes a persona profile for the current user.
func (h *Handler) handleUserPersonaDelete(ctx context.Context, req Request) Response {
	username := UserFromContext(ctx)
	if username == "" {
		return h.invalidParams(req.ID, "not authenticated")
	}

	id, _ := req.Params["id"].(string)
	if strings.TrimSpace(id) == "" {
		return h.invalidParams(req.ID, "id is required")
	}

	if err := h.patchUserPersonas(username, func(uc *config.UserPersonasConfig) {
		delete(uc.Profiles, id)
		if uc.Active == id {
			uc.Active = ""
		}
	}); err != nil {
		return h.internalError(req.ID, "delete user persona: "+err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

// handleUserPersonaSetActive sets the active persona for the current user.
func (h *Handler) handleUserPersonaSetActive(ctx context.Context, req Request) Response {
	username := UserFromContext(ctx)
	if username == "" {
		return h.invalidParams(req.ID, "not authenticated")
	}

	id, _ := req.Params["id"].(string)

	// Empty id means deactivate — always allowed.
	if id != "" {
		// Validate the persona exists in global or user profiles.
		s := h.runtime.Settings()
		found := false
		if s != nil && s.Personas != nil {
			if _, ok := s.Personas.Profiles[id]; ok {
				found = true
			}
		}
		if !found && s != nil && s.UserPersonas != nil {
			if uc, ok := s.UserPersonas[username]; ok && uc != nil {
				if _, ok := uc.Profiles[id]; ok {
					found = true
				}
			}
		}
		if !found {
			return h.invalidParams(req.ID, "persona not found: "+id)
		}
	}

	if err := h.patchUserPersonas(username, func(uc *config.UserPersonasConfig) {
		uc.Active = id
	}); err != nil {
		return h.internalError(req.ID, "set active persona: "+err.Error())
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

// patchUserPersonas loads settings.local.json, applies a mutation to the
// specified user's persona config, saves back, and reloads the runtime.
func (h *Handler) patchUserPersonas(username string, mutate func(uc *config.UserPersonasConfig)) error {
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	projectRoot := h.runtime.ProjectRoot()
	existing, err := config.LoadSettingsLocal(projectRoot)
	if err != nil || existing == nil {
		existing = &config.Settings{}
	}
	if existing.UserPersonas == nil {
		existing.UserPersonas = map[string]*config.UserPersonasConfig{}
	}
	uc := existing.UserPersonas[username]
	if uc == nil {
		uc = &config.UserPersonasConfig{
			Profiles: map[string]config.PersonaProfile{},
		}
	}
	mutate(uc)
	existing.UserPersonas[username] = uc

	if err := config.SaveSettingsLocal(projectRoot, existing); err != nil {
		return err
	}
	return h.runtime.ReloadSettings()
}
