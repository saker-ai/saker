package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cinience/saker/pkg/project"
)

// JSON-RPC error codes specific to project access.
const (
	// ErrCodeUnauthorized — caller is not authenticated.
	ErrCodeUnauthorized = -32002
	// ErrCodeProjectMissing — request omits projectId for a method that needs one.
	ErrCodeProjectMissing = -32003
	// ErrCodeProjectAccess — caller is not a member with sufficient role.
	ErrCodeProjectAccess = -32004
	// ErrCodeProjectStore — internal error talking to the project store.
	ErrCodeProjectStore = -32005
)

// methodSkipProject lists RPCs that bypass the projectId check. These either
// run before a project is selected (auth/login, project/list, project/create)
// or operate on global state (user/me, aigo/*).
var methodSkipProject = map[string]bool{
	// Boot / connection.
	"initialize": true,

	// Auth lifecycle: caller may not even be logged in yet.
	"auth/update": true,
	"auth/delete": true,

	// User self-service & site-admin user CRUD.
	"user/me":              true,
	"user/list":            true,
	"user/create":          true,
	"user/delete":          true,
	"user/update-password": true,

	// Project discovery & creation; project/get is allowed without a
	// projectId param because the user may be browsing memberships.
	"project/list":   true,
	"project/create": true,
	"project/get":    true,
	"project/me":     true,

	// Invite acceptance / refusal is keyed by inviteId, not projectId — the
	// invitee is not yet a member, so the scope/role check would always fail.
	"project/invite/accept":      true,
	"project/invite/decline":     true,
	"project/invite/list-for-me": true,

	// Teams are top-level entities — they don't belong to a project.
	"team/list":        true,
	"team/create":      true,
	"team/delete":      true,
	"team/member/list": true,

	// Cross-project search & global metadata.
	"sessions/search": true,
	"aigo/models":     true,
	"aigo/providers":  true,
	"aigo/status":     true,
}

// methodMinRole maps RPC method names to the minimum project role required.
// Methods absent from the table default to RoleViewer (read-only). Methods
// in methodSkipProject bypass this table entirely.
var methodMinRole = map[string]project.Role{
	// Edit-tier methods (member or higher).
	"thread/create":     project.RoleMember,
	"thread/update":     project.RoleMember,
	"thread/delete":     project.RoleMember,
	"turn/send":         project.RoleMember,
	"turn/cancel":       project.RoleMember,
	"thread/interrupt":  project.RoleMember,
	"approval/respond":  project.RoleMember,
	"question/respond":  project.RoleMember,
	"canvas/save":       project.RoleMember,
	"canvas/text-gen":   project.RoleMember,
	"canvas/execute":    project.RoleMember,
	"canvas/run-cancel": project.RoleMember,
	"tool/run":          project.RoleMember,
	"skill/remove":      project.RoleMember,
	"skill/promote":     project.RoleMember,
	"skill/patch":       project.RoleMember,
	"skill/import":      project.RoleMember,
	"model/switch":      project.RoleMember,
	"media/cache":       project.RoleMember,
	"persona/save":      project.RoleMember,
	"persona/delete":    project.RoleMember,
	"memory/delete":     project.RoleMember,

	// Manage-tier (admin or owner).
	"settings/update":            project.RoleAdmin,
	"project/update":             project.RoleAdmin,
	"project/invite":             project.RoleAdmin,
	"project/invite/cancel":      project.RoleAdmin,
	"project/invite/list":        project.RoleAdmin,
	"project/member/update-role": project.RoleAdmin,
	"project/member/remove":      project.RoleAdmin,
	"channels/save":              project.RoleAdmin,
	"channels/delete":            project.RoleAdmin,
	"channels/toggle":            project.RoleAdmin,
	"channels/route-set":         project.RoleAdmin,

	// Owner-only.
	"project/delete":   project.RoleOwner,
	"project/transfer": project.RoleOwner,
}

// resolveScope inspects the request and returns either the request context
// enriched with a project.Scope, or a JSON-RPC Response describing the
// access denial. When no project store is wired in (embedded library mode
// or tests) it returns the original ctx unchanged so legacy code paths see
// no behavioural change.
func (h *Handler) resolveScope(ctx context.Context, req Request) (context.Context, *Response) {
	if h.projects == nil {
		return ctx, nil
	}
	if methodSkipProject[req.Method] {
		return ctx, nil
	}
	// Identify the caller. UserFromContext is populated by AuthMiddleware
	// (pkg/server/auth.go).
	user := UserFromContext(ctx)
	if user == "" {
		resp := h.errorResp(req.ID, ErrCodeUnauthorized, "authentication required")
		return ctx, &resp
	}

	projectID, _ := req.Params["projectId"].(string)
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		resp := h.errorResp(req.ID, ErrCodeProjectMissing, "projectId required for "+req.Method)
		return ctx, &resp
	}

	u, err := h.projects.LookupUserByUsername(ctx, user, "")
	if err != nil {
		resp := h.errorResp(req.ID, ErrCodeProjectStore, "lookup user: "+err.Error())
		return ctx, &resp
	}
	pm, err := h.projects.GetMember(ctx, projectID, u.ID)
	if err != nil {
		resp := h.errorResp(req.ID, ErrCodeProjectAccess, "not a member of project "+projectID)
		return ctx, &resp
	}
	min := methodMinRole[req.Method]
	if min == "" {
		min = project.RoleViewer
	}
	if !pm.Role.AtLeast(min) {
		resp := h.errorResp(req.ID, ErrCodeProjectAccess, "role "+string(pm.Role)+" insufficient for "+req.Method+" (need "+string(min)+")")
		return ctx, &resp
	}

	scope := project.Scope{
		UserID:    u.ID,
		Username:  u.Username,
		ProjectID: projectID,
		Role:      pm.Role,
		Paths:     project.BuildPaths(h.dataDir, projectID),
	}
	return project.WithScope(ctx, scope), nil
}

// resolveRESTScope is the HTTP-handler counterpart to resolveScope: it takes
// a projectID extracted from the URL and returns the request context enriched
// with a project.Scope. Returns (ctx, nil) when no project store is wired in
// (embedded library mode), or (ctx, error) when the caller fails the
// membership check. Each error string is suitable for HTTP body text; the
// caller decides which status code to map it to.
func (h *Handler) resolveRESTScope(ctx context.Context, user, projectID string) (context.Context, error) {
	if h.projects == nil {
		return ctx, nil
	}
	if user == "" {
		return ctx, errRESTAuthRequired
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ctx, errRESTProjectMissing
	}
	u, err := h.projects.LookupUserByUsername(ctx, user, "")
	if err != nil {
		return ctx, fmt.Errorf("lookup user: %w", err)
	}
	pm, err := h.projects.GetMember(ctx, projectID, u.ID)
	if err != nil {
		return ctx, fmt.Errorf("not a member of project %s", projectID)
	}
	scope := project.Scope{
		UserID:    u.ID,
		Username:  u.Username,
		ProjectID: projectID,
		Role:      pm.Role,
		Paths:     project.BuildPaths(h.dataDir, projectID),
	}
	return project.WithScope(ctx, scope), nil
}

// REST-side sentinel errors so canvas_rest.go can map them to specific HTTP
// status codes (401/400) instead of leaking generic 500s.
var (
	errRESTAuthRequired   = errors.New("authentication required")
	errRESTProjectMissing = errors.New("projectId required in URL")
)

// bearerProjectScope synthesizes a project.Scope for Bearer-API-key requests
// in multi-tenant mode. The handler downstream validates the actual key
// against the app under this project; we only need ProjectID + Paths set so
// pathsFor returns the correct per-project root. UserID/Username/Role stay
// zero — the caller is anonymous (key-authenticated, not user-authenticated).
func (h *Handler) bearerProjectScope(ctx context.Context, projectID string) context.Context {
	if h.projects == nil {
		return ctx
	}
	scope := project.Scope{
		ProjectID: strings.TrimSpace(projectID),
		Paths:     project.BuildPaths(h.dataDir, projectID),
	}
	return project.WithScope(ctx, scope)
}

// errorResp builds a JSON-RPC error response with the given code & message.
func (h *Handler) errorResp(id any, code int, msg string) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: code, Message: msg},
	}
}
