package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/cinience/saker/pkg/project"
)

// resolveCurrentUser returns the project.User row for the authenticated
// caller. The Handler.handleXxx layer has already passed through the auth
// middleware, so UserFromContext is non-empty. When the project store is
// disabled (legacy single-project mode), all project/* RPCs are unsupported.
func (h *Handler) resolveCurrentUser(ctx context.Context, reqID any) (*project.User, *Response) {
	if h.projects == nil {
		resp := h.errorResp(reqID, ErrCodeProjectStore, "project store not configured")
		return nil, &resp
	}
	username := UserFromContext(ctx)
	if username == "" {
		resp := h.errorResp(reqID, ErrCodeUnauthorized, "authentication required")
		return nil, &resp
	}
	u, err := h.projects.LookupUserByUsername(ctx, username, "")
	if err != nil {
		// Auto-provision so first-time login from existing settings.local.json
		// users gets a personal project without manual sign-up step.
		created, cerr := h.projects.EnsureUserFromAuth(ctx, project.UserSourceLocal, username, "", username, "")
		if cerr != nil {
			resp := h.errorResp(reqID, ErrCodeProjectStore, "lookup user: "+err.Error())
			return nil, &resp
		}
		u = created
	}
	return u, nil
}

// projectSummaryJSON returns a frontend-friendly view of a ProjectSummary.
func projectSummaryJSON(s project.ProjectSummary) map[string]any {
	return map[string]any{
		"id":        s.Project.ID,
		"name":      s.Project.Name,
		"slug":      s.Project.Slug,
		"kind":      string(s.Project.Kind),
		"ownerId":   s.Project.OwnerUserID,
		"teamId":    s.Project.TeamID,
		"role":      string(s.Role),
		"createdAt": s.Project.CreatedAt,
	}
}

func projectJSON(p *project.Project, role project.Role) map[string]any {
	if p == nil {
		return nil
	}
	return map[string]any{
		"id":        p.ID,
		"name":      p.Name,
		"slug":      p.Slug,
		"kind":      string(p.Kind),
		"ownerId":   p.OwnerUserID,
		"teamId":    p.TeamID,
		"role":      string(role),
		"createdAt": p.CreatedAt,
	}
}

// handleProjectList returns every project the caller is a member of. The
// caller's personal project is auto-created on first call so a fresh user
// always sees at least one entry.
func (h *Handler) handleProjectList(ctx context.Context, req Request) Response {
	u, deny := h.resolveCurrentUser(ctx, req.ID)
	if deny != nil {
		return *deny
	}
	// Ensure personal project exists so the dropdown is never empty.
	if _, err := h.projects.EnsurePersonalProject(ctx, u.ID); err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, "ensure personal: "+err.Error())
	}
	summaries, err := h.projects.ListProjects(ctx, u.ID)
	if err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	out := make([]map[string]any, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, projectSummaryJSON(s))
	}
	return h.success(req.ID, map[string]any{"projects": out})
}

// handleProjectCreate creates a new team project owned by the caller.
func (h *Handler) handleProjectCreate(ctx context.Context, req Request) Response {
	u, deny := h.resolveCurrentUser(ctx, req.ID)
	if deny != nil {
		return *deny
	}
	var params struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	raw, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(raw, &params); err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	if strings.TrimSpace(params.Name) == "" {
		return h.invalidParams(req.ID, "name is required")
	}
	p, err := h.projects.CreateProject(ctx, project.CreateProjectOptions{
		Name:        params.Name,
		Slug:        params.Slug,
		OwnerUserID: u.ID,
		Kind:        project.ProjectKindTeam,
	})
	if err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	return h.success(req.ID, projectJSON(p, project.RoleOwner))
}

// handleProjectGet returns the full project record. The dispatcher already
// verified membership when projectId was supplied, but this handler also
// supports lookup by id without the scope wrapper for convenience.
func (h *Handler) handleProjectGet(ctx context.Context, req Request) Response {
	u, deny := h.resolveCurrentUser(ctx, req.ID)
	if deny != nil {
		return *deny
	}
	projectID, _ := req.Params["projectId"].(string)
	if strings.TrimSpace(projectID) == "" {
		return h.invalidParams(req.ID, "projectId is required")
	}
	pm, err := h.projects.GetMember(ctx, projectID, u.ID)
	if err != nil {
		return h.errorResp(req.ID, ErrCodeProjectAccess, "not a member")
	}
	p, err := h.projects.GetProject(ctx, projectID)
	if err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	return h.success(req.ID, projectJSON(p, pm.Role))
}

// handleProjectUpdate updates name/slug. Requires admin or owner.
func (h *Handler) handleProjectUpdate(ctx context.Context, req Request) Response {
	scope, ok := project.FromContext(ctx)
	if !ok {
		return h.errorResp(req.ID, ErrCodeProjectMissing, "projectId required")
	}
	var params struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	raw, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(raw, &params); err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	if err := h.projects.UpdateProjectMeta(ctx, scope.ProjectID, scope.UserID, params.Name, params.Slug); err != nil {
		return mapProjectError(h, req.ID, err)
	}
	p, err := h.projects.GetProject(ctx, scope.ProjectID)
	if err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	return h.success(req.ID, projectJSON(p, scope.Role))
}

// handleProjectDelete soft-deletes a non-personal project. Owner only.
//
// After the database row is marked deleted we also (a) evict any cached
// per-project components so a subsequent request would have to rebuild from
// scratch (which fails fast against the now-tombstoned project), and (b)
// best-effort remove the on-disk project directory so disk usage doesn't
// grow with every delete. Both are idempotent — if the registries were
// never populated for this project, Evict is a no-op; if the directory
// never existed (project was created but never written to), RemoveAll
// returns nil.
func (h *Handler) handleProjectDelete(ctx context.Context, req Request) Response {
	scope, ok := project.FromContext(ctx)
	if !ok {
		return h.errorResp(req.ID, ErrCodeProjectMissing, "projectId required")
	}
	if err := h.projects.SoftDeleteProject(ctx, scope.ProjectID, scope.UserID); err != nil {
		return mapProjectError(h, req.ID, err)
	}
	// Evict cached components so an in-flight subscription stops touching the
	// deleted project's files. Both registries can be nil if the multi-tenant
	// path was never primed for this server instance.
	h.mu.RLock()
	sessReg := h.sessionRegistry
	canvReg := h.canvasExecRegistry
	h.mu.RUnlock()
	if sessReg != nil {
		sessReg.Evict(scope.ProjectID)
	}
	if canvReg != nil {
		canvReg.Evict(scope.ProjectID)
	}
	// GC the project directory. Logged as a warning rather than failing the
	// RPC — the DB tombstone is the source of truth, and a stuck directory
	// can be cleaned up later by an operator without resurrecting the row.
	if scope.Paths.Root != "" {
		if err := os.RemoveAll(scope.Paths.Root); err != nil {
			h.logger.Warn("project delete: remove on-disk directory failed",
				"project_id", scope.ProjectID,
				"path", scope.Paths.Root,
				"error", err)
		}
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

// handleProjectTransfer hands ownership of a team project from the caller (the
// current owner) to a target member. The target must already be a member; use
// project/invite first if they aren't. The previous owner is demoted to admin
// in the same transaction so the project always has exactly one owner.
func (h *Handler) handleProjectTransfer(ctx context.Context, req Request) Response {
	scope, ok := project.FromContext(ctx)
	if !ok {
		return h.errorResp(req.ID, ErrCodeProjectMissing, "projectId required")
	}
	var params struct {
		TargetUserID string `json:"targetUserId"`
	}
	raw, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(raw, &params); err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	if strings.TrimSpace(params.TargetUserID) == "" {
		return h.invalidParams(req.ID, "targetUserId is required")
	}
	if err := h.projects.TransferOwnership(ctx, scope.ProjectID, scope.UserID, params.TargetUserID); err != nil {
		return mapProjectError(h, req.ID, err)
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

// handleProjectMe returns the caller's "current" view: user info + project
// list. Used by the frontend on boot to populate the TopBar.
func (h *Handler) handleProjectMe(ctx context.Context, req Request) Response {
	u, deny := h.resolveCurrentUser(ctx, req.ID)
	if deny != nil {
		return *deny
	}
	if _, err := h.projects.EnsurePersonalProject(ctx, u.ID); err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, "ensure personal: "+err.Error())
	}
	summaries, err := h.projects.ListProjects(ctx, u.ID)
	if err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	projs := make([]map[string]any, 0, len(summaries))
	for _, s := range summaries {
		projs = append(projs, projectSummaryJSON(s))
	}
	return h.success(req.ID, map[string]any{
		"user": map[string]any{
			"id":          u.ID,
			"username":    u.Username,
			"displayName": u.DisplayName,
			"email":       u.Email,
			"source":      string(u.Source),
			"globalRole":  u.GlobalRole,
		},
		"projects": projs,
	})
}

// mapProjectError translates a service-layer error to a JSON-RPC response with
// the closest matching code. Keeps the call sites short.
func mapProjectError(h *Handler, reqID any, err error) Response {
	switch {
	case errors.Is(err, project.ErrUserNotFound):
		return h.errorResp(reqID, ErrCodeProjectStore, err.Error())
	case errors.Is(err, project.ErrProjectNotFound):
		return h.errorResp(reqID, ErrCodeProjectStore, err.Error())
	case errors.Is(err, project.ErrNotMember),
		errors.Is(err, project.ErrInsufficientRole),
		errors.Is(err, project.ErrSoleOwner),
		errors.Is(err, project.ErrPersonalImmutable):
		return h.errorResp(reqID, ErrCodeProjectAccess, err.Error())
	case errors.Is(err, project.ErrAlreadyMember),
		errors.Is(err, project.ErrSelfInvite),
		errors.Is(err, project.ErrInvalidRole),
		errors.Is(err, project.ErrInviteNotFound),
		errors.Is(err, project.ErrInviteWrongUser),
		errors.Is(err, project.ErrInviteNotPending):
		return h.invalidParams(reqID, err.Error())
	default:
		return h.errorResp(reqID, ErrCodeProjectStore, err.Error())
	}
}
