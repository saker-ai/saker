package server

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/project"
)

// inviteJSON is the wire shape for an Invite. ExpiresAt is omitted when nil so
// the frontend can render "no expiry" cleanly without a magic-zero timestamp.
func inviteJSON(inv *project.Invite) map[string]any {
	out := map[string]any{
		"id":        inv.ID,
		"projectId": inv.ProjectID,
		"username":  inv.Username,
		"userId":    inv.UserID,
		"role":      string(inv.Role),
		"invitedBy": inv.InvitedBy,
		"status":    string(inv.Status),
		"createdAt": inv.CreatedAt,
	}
	if inv.ExpiresAt != nil {
		out["expiresAt"] = *inv.ExpiresAt
	}
	if inv.AcceptedAt != nil {
		out["acceptedAt"] = *inv.AcceptedAt
	}
	return out
}

// memberJSON enriches the wire shape with the user's display name when
// available so the UI doesn't need a second round-trip per row.
func memberJSON(pm *project.ProjectMember, user *project.User) map[string]any {
	out := map[string]any{
		"projectId": pm.ProjectID,
		"userId":    pm.UserID,
		"role":      string(pm.Role),
		"invitedBy": pm.InvitedBy,
		"joinedAt":  pm.JoinedAt,
	}
	if user != nil {
		out["username"] = user.Username
		out["displayName"] = user.DisplayName
		out["email"] = user.Email
	}
	return out
}

// handleProjectInvite issues a username-targeted invite. The dispatcher has
// already verified that the caller is admin or owner via methodMinRole.
func (h *Handler) handleProjectInvite(ctx context.Context, req Request) Response {
	scope, ok := project.FromContext(ctx)
	if !ok {
		return h.errorResp(req.ID, ErrCodeProjectMissing, "projectId required")
	}
	var params struct {
		Username  string `json:"username"`
		Role      string `json:"role"`
		ExpiresIn int64  `json:"expiresInSeconds"`
	}
	raw, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(raw, &params); err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	if strings.TrimSpace(params.Username) == "" {
		return h.invalidParams(req.ID, "username is required")
	}
	role := project.Role(params.Role)
	if !role.Valid() {
		role = project.RoleMember
	}
	opts := project.InviteOptions{
		ProjectID: scope.ProjectID,
		InviterID: scope.UserID,
		Username:  params.Username,
		Role:      role,
	}
	if params.ExpiresIn > 0 {
		opts.ExpiresIn = time.Duration(params.ExpiresIn) * time.Second
	}
	inv, err := h.projects.InviteByUsername(ctx, opts)
	if err != nil {
		return mapProjectError(h, req.ID, err)
	}
	return h.success(req.ID, inviteJSON(inv))
}

// handleProjectInviteList lists all invites for the current project. Status
// filter is optional; empty returns every status.
func (h *Handler) handleProjectInviteList(ctx context.Context, req Request) Response {
	scope, ok := project.FromContext(ctx)
	if !ok {
		return h.errorResp(req.ID, ErrCodeProjectMissing, "projectId required")
	}
	status, _ := req.Params["status"].(string)
	invites, err := h.projects.ListInvites(ctx, scope.ProjectID, project.InviteStatus(status))
	if err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	out := make([]map[string]any, 0, len(invites))
	for i := range invites {
		out = append(out, inviteJSON(&invites[i]))
	}
	return h.success(req.ID, map[string]any{"invites": out})
}

// handleProjectInviteCancel revokes a pending invite. Admin or owner only.
func (h *Handler) handleProjectInviteCancel(ctx context.Context, req Request) Response {
	scope, ok := project.FromContext(ctx)
	if !ok {
		return h.errorResp(req.ID, ErrCodeProjectMissing, "projectId required")
	}
	inviteID, _ := req.Params["inviteId"].(string)
	if strings.TrimSpace(inviteID) == "" {
		return h.invalidParams(req.ID, "inviteId is required")
	}
	if err := h.projects.CancelInvite(ctx, inviteID, scope.UserID); err != nil {
		return mapProjectError(h, req.ID, err)
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

// handleProjectInviteListForMe returns pending invites addressed to the caller
// across all projects. Used by the notification badge / accept dialog. Each
// invite is enriched with the project's display name and the inviter's
// username so the inbox can render a useful summary without N+1 fetches.
// Lookup failures fall back gracefully: a missing project is just skipped
// from enrichment rather than causing the whole inbox to fail (the row may
// still be useful as "invite to a project you can't yet see").
func (h *Handler) handleProjectInviteListForMe(ctx context.Context, req Request) Response {
	u, deny := h.resolveCurrentUser(ctx, req.ID)
	if deny != nil {
		return *deny
	}
	invites, err := h.projects.ListInvitesForUser(ctx, u.ID)
	if err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	// Cache lookups across rows — typically a user is invited to a small
	// number of distinct projects so the win is measurable on the rare
	// busy-inbox case without complicating the happy path.
	projectCache := map[string]*project.Project{}
	userCache := map[string]*project.User{}
	out := make([]map[string]any, 0, len(invites))
	for i := range invites {
		row := inviteJSON(&invites[i])
		pid := invites[i].ProjectID
		if _, hit := projectCache[pid]; !hit {
			if p, perr := h.projects.GetProject(ctx, pid); perr == nil {
				projectCache[pid] = p
			} else {
				projectCache[pid] = nil
			}
		}
		if p := projectCache[pid]; p != nil {
			row["projectName"] = p.Name
			row["projectKind"] = string(p.Kind)
		}
		iby := invites[i].InvitedBy
		if _, hit := userCache[iby]; !hit {
			if iu, uerr := h.projects.GetUser(ctx, iby); uerr == nil {
				userCache[iby] = iu
			} else {
				userCache[iby] = nil
			}
		}
		if iu := userCache[iby]; iu != nil {
			row["inviterUsername"] = iu.Username
			if iu.DisplayName != "" {
				row["inviterDisplayName"] = iu.DisplayName
			}
		}
		out = append(out, row)
	}
	return h.success(req.ID, map[string]any{"invites": out})
}

// handleProjectInviteAccept accepts an invite addressed to the caller.
func (h *Handler) handleProjectInviteAccept(ctx context.Context, req Request) Response {
	u, deny := h.resolveCurrentUser(ctx, req.ID)
	if deny != nil {
		return *deny
	}
	inviteID, _ := req.Params["inviteId"].(string)
	if strings.TrimSpace(inviteID) == "" {
		return h.invalidParams(req.ID, "inviteId is required")
	}
	pm, err := h.projects.AcceptInvite(ctx, inviteID, u.ID)
	if err != nil {
		return mapProjectError(h, req.ID, err)
	}
	return h.success(req.ID, memberJSON(pm, u))
}

// handleProjectInviteDecline lets the invitee refuse a pending invite. Like
// accept, this RPC bypasses scope (the invitee isn't a member yet so they
// can't satisfy a membership check).
func (h *Handler) handleProjectInviteDecline(ctx context.Context, req Request) Response {
	u, deny := h.resolveCurrentUser(ctx, req.ID)
	if deny != nil {
		return *deny
	}
	inviteID, _ := req.Params["inviteId"].(string)
	if strings.TrimSpace(inviteID) == "" {
		return h.invalidParams(req.ID, "inviteId is required")
	}
	if err := h.projects.DeclineInvite(ctx, inviteID, u.ID); err != nil {
		return mapProjectError(h, req.ID, err)
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

// handleProjectMemberList returns every member of the current project,
// enriched with the user record so the UI can render usernames directly.
func (h *Handler) handleProjectMemberList(ctx context.Context, req Request) Response {
	scope, ok := project.FromContext(ctx)
	if !ok {
		return h.errorResp(req.ID, ErrCodeProjectMissing, "projectId required")
	}
	members, err := h.projects.ListMembers(ctx, scope.ProjectID)
	if err != nil {
		return h.errorResp(req.ID, ErrCodeProjectStore, err.Error())
	}
	out := make([]map[string]any, 0, len(members))
	for i := range members {
		u, _ := h.projects.GetUser(ctx, members[i].UserID)
		out = append(out, memberJSON(&members[i], u))
	}
	return h.success(req.ID, map[string]any{"members": out})
}

// handleProjectMemberUpdateRole changes a member's role. Admin or owner only.
func (h *Handler) handleProjectMemberUpdateRole(ctx context.Context, req Request) Response {
	scope, ok := project.FromContext(ctx)
	if !ok {
		return h.errorResp(req.ID, ErrCodeProjectMissing, "projectId required")
	}
	var params struct {
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	raw, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(raw, &params); err != nil {
		return h.invalidParams(req.ID, "invalid params: "+err.Error())
	}
	if strings.TrimSpace(params.UserID) == "" {
		return h.invalidParams(req.ID, "userId is required")
	}
	role := project.Role(params.Role)
	if !role.Valid() {
		return h.invalidParams(req.ID, "role must be owner|admin|member|viewer")
	}
	err := h.projects.UpdateRole(ctx, project.UpdateRoleOptions{
		ProjectID:    scope.ProjectID,
		ActorUserID:  scope.UserID,
		TargetUserID: params.UserID,
		NewRole:      role,
	})
	if err != nil {
		return mapProjectError(h, req.ID, err)
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

// handleProjectMemberRemove evicts a member. Admin or owner; members may
// remove themselves (self-removal is permitted by the service layer).
func (h *Handler) handleProjectMemberRemove(ctx context.Context, req Request) Response {
	scope, ok := project.FromContext(ctx)
	if !ok {
		return h.errorResp(req.ID, ErrCodeProjectMissing, "projectId required")
	}
	target, _ := req.Params["userId"].(string)
	if strings.TrimSpace(target) == "" {
		return h.invalidParams(req.ID, "userId is required")
	}
	if err := h.projects.RemoveMember(ctx, scope.ProjectID, scope.UserID, target); err != nil {
		return mapProjectError(h, req.ID, err)
	}
	return h.success(req.ID, map[string]any{"ok": true})
}
