package server

import (
	"context"
	"encoding/json"

	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/profile"
	"github.com/cinience/saker/pkg/project"
	"golang.org/x/crypto/bcrypt"
)

func (h *Handler) handleAuthUpdate(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}
	var params struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	raw, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(raw, &params); err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	if params.Password == "" {
		return h.invalidParams(req.ID, "password is required")
	}

	username := params.Username
	if username == "" {
		username = "admin"
	}

	hash, err := HashPassword(params.Password)
	if err != nil {
		return h.internalError(req.ID, "hash password: "+err.Error())
	}

	// Save to settings.local.json, preserving existing users list.
	projectRoot := h.runtime.ProjectRoot()
	existing, _ := config.LoadSettingsLocal(projectRoot)
	if existing == nil {
		existing = &config.Settings{}
	}
	if existing.WebAuth == nil {
		existing.WebAuth = &config.WebAuthConfig{}
	}
	// Update only admin credentials, preserve Users slice.
	existing.WebAuth.Username = username
	existing.WebAuth.Password = hash
	if err := config.SaveSettingsLocal(projectRoot, existing); err != nil {
		return h.internalError(req.ID, "save settings: "+err.Error())
	}

	// Update live auth manager.
	if h.auth != nil {
		h.auth.UpdateConfig(existing.WebAuth)
	}

	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleAuthDelete(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}
	// Clear from settings.local.json.
	projectRoot := h.runtime.ProjectRoot()
	existing, _ := config.LoadSettingsLocal(projectRoot)
	if existing != nil {
		existing.WebAuth = nil
		if err := config.SaveSettingsLocal(projectRoot, existing); err != nil {
			return h.internalError(req.ID, "save settings: "+err.Error())
		}
	}

	// Clear live auth manager.
	if h.auth != nil {
		h.auth.UpdateConfig(nil)
	}

	return h.success(req.ID, map[string]any{"ok": true})
}

// --- User & Profile management ---

func (h *Handler) requireAdmin(ctx context.Context, reqID any) (Response, bool) {
	if RoleFromContext(ctx) != "admin" {
		return Response{
			JSONRPC: "2.0",
			ID:      reqID,
			Error:   &Error{Code: ErrCodeInvalidParams, Message: "admin access required"},
		}, false
	}
	return Response{}, true
}

func (h *Handler) handleUserMe(ctx context.Context, req Request) Response {
	username := UserFromContext(ctx)
	role := RoleFromContext(ctx)
	resp := map[string]any{
		"username": username,
		"role":     role,
	}

	// Enrich with cached external user info (LDAP/OIDC).
	if h.auth != nil {
		if info := h.auth.GetUserInfo(username); info != nil {
			if info.DisplayName != "" {
				resp["displayName"] = info.DisplayName
			}
			if info.Email != "" {
				resp["email"] = info.Email
			}
			if info.AvatarURL != "" {
				resp["avatarUrl"] = info.AvatarURL
			}
			resp["provider"] = info.Provider
		}
	}

	return h.success(req.ID, resp)
}

func (h *Handler) handleUserList(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}

	settings := h.runtime.Settings()
	var users []map[string]any

	// Admin user.
	adminUser := "admin"
	if settings != nil && settings.WebAuth != nil && settings.WebAuth.Username != "" {
		adminUser = settings.WebAuth.Username
	}
	users = append(users, map[string]any{
		"username": adminUser,
		"role":     "admin",
		"disabled": false,
	})

	// Regular users.
	if settings != nil && settings.WebAuth != nil {
		for _, u := range settings.WebAuth.Users {
			users = append(users, map[string]any{
				"username": u.Username,
				"role":     "user",
				"disabled": u.Disabled,
			})
		}
	}

	return h.success(req.ID, map[string]any{"users": users})
}

func (h *Handler) handleUserCreate(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}

	var params struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	raw, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(raw, &params); err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	if params.Username == "" || params.Password == "" {
		return h.invalidParams(req.ID, "username and password are required")
	}

	// Validate username as a valid profile name.
	if err := profile.Validate(params.Username); err != nil {
		return h.invalidParams(req.ID, err.Error())
	}

	projectRoot := h.runtime.ProjectRoot()
	existing, _ := config.LoadSettingsLocal(projectRoot)
	if existing == nil {
		existing = &config.Settings{}
	}
	if existing.WebAuth == nil {
		return h.invalidParams(req.ID, "web auth not configured — set admin password first")
	}

	// Check admin username collision.
	adminUser := existing.WebAuth.Username
	if adminUser == "" {
		adminUser = "admin"
	}
	if params.Username == adminUser {
		return h.invalidParams(req.ID, "cannot create user with admin username")
	}

	// Check duplicate.
	for _, u := range existing.WebAuth.Users {
		if u.Username == params.Username {
			return h.invalidParams(req.ID, "user already exists: "+params.Username)
		}
	}

	hash, err := HashPassword(params.Password)
	if err != nil {
		return h.internalError(req.ID, "hash password: "+err.Error())
	}

	existing.WebAuth.Users = append(existing.WebAuth.Users, config.UserAuth{
		Username: params.Username,
		Password: hash,
	})

	if err := config.SaveSettingsLocal(projectRoot, existing); err != nil {
		return h.internalError(req.ID, "save settings: "+err.Error())
	}

	// Update live auth manager.
	if h.auth != nil {
		h.auth.UpdateConfig(existing.WebAuth)
	}

	// Pre-create profile directory.
	_ = profile.EnsureExists(projectRoot, params.Username)

	// Mirror into the multi-tenant store so the new user can log in and
	// land in their personal project on first request. Best-effort.
	if h.projects != nil {
		u, err := h.projects.EnsureUserFromAuth(ctx, project.UserSourceLocal, params.Username, "", params.Username, "")
		if err != nil {
			h.logger.Warn("project store: ensure user failed for new local user", "username", params.Username, "error", err)
		} else if _, err := h.projects.EnsurePersonalProject(ctx, u.ID); err != nil {
			h.logger.Warn("project store: ensure personal project failed", "username", params.Username, "error", err)
		}
	}

	return h.success(req.ID, map[string]any{"ok": true, "username": params.Username})
}

func (h *Handler) handleUserDelete(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}

	username, _ := req.Params["username"].(string)
	if username == "" {
		return h.invalidParams(req.ID, "username is required")
	}

	projectRoot := h.runtime.ProjectRoot()
	existing, _ := config.LoadSettingsLocal(projectRoot)
	if existing == nil || existing.WebAuth == nil {
		return h.invalidParams(req.ID, "web auth not configured")
	}

	// Cannot delete admin.
	adminUser := existing.WebAuth.Username
	if adminUser == "" {
		adminUser = "admin"
	}
	if username == adminUser {
		return h.invalidParams(req.ID, "cannot delete admin user")
	}

	// Find and remove the user.
	found := false
	filtered := existing.WebAuth.Users[:0]
	for _, u := range existing.WebAuth.Users {
		if u.Username == username {
			found = true
			continue
		}
		filtered = append(filtered, u)
	}
	if !found {
		return h.invalidParams(req.ID, "user not found: "+username)
	}
	existing.WebAuth.Users = filtered

	if err := config.SaveSettingsLocal(projectRoot, existing); err != nil {
		return h.internalError(req.ID, "save settings: "+err.Error())
	}

	// Update live auth manager.
	if h.auth != nil {
		h.auth.UpdateConfig(existing.WebAuth)
	}

	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleUserUpdatePassword(ctx context.Context, req Request) Response {
	username := UserFromContext(ctx)
	if username == "" {
		return h.invalidParams(req.ID, "not authenticated")
	}

	var params struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	raw, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(raw, &params); err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	if params.OldPassword == "" || params.NewPassword == "" {
		return h.invalidParams(req.ID, "oldPassword and newPassword are required")
	}

	projectRoot := h.runtime.ProjectRoot()
	existing, _ := config.LoadSettingsLocal(projectRoot)
	if existing == nil || existing.WebAuth == nil {
		return h.invalidParams(req.ID, "auth not configured")
	}

	role := RoleFromContext(ctx)
	if role == "admin" {
		// Admin changing own password.
		if err := bcrypt.CompareHashAndPassword([]byte(existing.WebAuth.Password), []byte(params.OldPassword)); err != nil {
			return h.invalidParams(req.ID, "incorrect old password")
		}
		hash, err := HashPassword(params.NewPassword)
		if err != nil {
			return h.internalError(req.ID, "hash password: "+err.Error())
		}
		existing.WebAuth.Password = hash
	} else {
		// Regular user changing own password.
		found := false
		for i, u := range existing.WebAuth.Users {
			if u.Username == username {
				if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(params.OldPassword)); err != nil {
					return h.invalidParams(req.ID, "incorrect old password")
				}
				hash, err := HashPassword(params.NewPassword)
				if err != nil {
					return h.internalError(req.ID, "hash password: "+err.Error())
				}
				existing.WebAuth.Users[i].Password = hash
				found = true
				break
			}
		}
		if !found {
			return h.invalidParams(req.ID, "user not found")
		}
	}

	if err := config.SaveSettingsLocal(projectRoot, existing); err != nil {
		return h.internalError(req.ID, "save settings: "+err.Error())
	}
	if h.auth != nil {
		h.auth.UpdateConfig(existing.WebAuth)
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleProfileList(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}
	projectRoot := h.runtime.ProjectRoot()
	profiles, err := profile.List(projectRoot)
	if err != nil {
		return h.internalError(req.ID, "list profiles: "+err.Error())
	}
	return h.success(req.ID, map[string]any{"profiles": profiles})
}

// safeWebAuthForResponse strips sensitive data from the settings response.
// Now also includes the user list (without password hashes).
func (h *Handler) safeWebAuthForResponse(auth *config.WebAuthConfig) map[string]any {
	if auth == nil {
		return nil
	}
	result := map[string]any{
		"username": auth.Username,
	}
	if len(auth.Users) > 0 {
		var users []map[string]any
		for _, u := range auth.Users {
			users = append(users, map[string]any{
				"username": u.Username,
				"disabled": u.Disabled,
			})
		}
		result["users"] = users
	}
	return result
}
