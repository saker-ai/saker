package server

import (
	"context"
	"testing"

	"github.com/cinience/saker/pkg/project"
)

// scopeCtx builds a request ctx with both the auth user and the project scope
// — used to drive handlers that read project.FromContext directly.
func scopeCtx(h *Handler, ctx context.Context, scope project.Scope, username string) context.Context {
	return project.WithScope(withUser(ctx, username, "user"), scope)
}

func setupProjectWithOwner(t *testing.T) (*Handler, *project.User, *project.Project, project.Scope) {
	t.Helper()
	h := newProjectTestHandler(t)
	alice := seedProjectUser(t, h.projects, "alice")
	p, err := h.projects.CreateProject(context.Background(), project.CreateProjectOptions{
		Name: "Acme", OwnerUserID: alice.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	scope := project.Scope{
		UserID: alice.ID, Username: "alice",
		ProjectID: p.ID, Role: project.RoleOwner,
		Paths: project.BuildPaths(h.dataDir, p.ID),
	}
	return h, alice, p, scope
}

func TestHandleProjectInvite_UnknownUser(t *testing.T) {
	t.Parallel()
	h, _, _, scope := setupProjectWithOwner(t)
	ctx := scopeCtx(h, context.Background(), scope, "alice")

	resp := h.handleProjectInvite(ctx, rpcRequest("project/invite", 1, map[string]any{
		"username": "ghost",
		"role":     string(project.RoleMember),
	}))
	if resp.Error == nil {
		t.Fatal("expected error inviting unknown user")
	}
	// mapProjectError sends ErrUserNotFound through ErrCodeProjectStore.
	if resp.Error.Code != ErrCodeProjectStore {
		t.Fatalf("want ErrCodeProjectStore, got %d (%s)", resp.Error.Code, resp.Error.Message)
	}
}

func TestHandleProjectInvite_UsernameRequired(t *testing.T) {
	t.Parallel()
	h, _, _, scope := setupProjectWithOwner(t)
	ctx := scopeCtx(h, context.Background(), scope, "alice")

	resp := h.handleProjectInvite(ctx, rpcRequest("project/invite", 1, map[string]any{
		"username": "  ",
		"role":     string(project.RoleMember),
	}))
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got %+v", resp.Error)
	}
}

func TestHandleProjectInvite_DefaultsToMember(t *testing.T) {
	t.Parallel()
	h, _, _, scope := setupProjectWithOwner(t)
	seedProjectUser(t, h.projects, "bob")
	ctx := scopeCtx(h, context.Background(), scope, "alice")

	resp := h.handleProjectInvite(ctx, rpcRequest("project/invite", 1, map[string]any{
		"username": "bob",
		// role intentionally absent — handler should fall back to member.
	}))
	if resp.Error != nil {
		t.Fatalf("invite: %+v", resp.Error)
	}
	out, _ := resp.Result.(map[string]any)
	if out["role"] != string(project.RoleMember) {
		t.Fatalf("want default role=member, got %v", out["role"])
	}
}

func TestHandleProjectInviteAccept_WrongUser(t *testing.T) {
	t.Parallel()
	h, _, _, scope := setupProjectWithOwner(t)
	seedProjectUser(t, h.projects, "bob")
	carol := seedProjectUser(t, h.projects, "carol")

	// Owner invites bob.
	ctxOwner := scopeCtx(h, context.Background(), scope, "alice")
	resp := h.handleProjectInvite(ctxOwner, rpcRequest("project/invite", 1, map[string]any{
		"username": "bob",
		"role":     string(project.RoleMember),
	}))
	if resp.Error != nil {
		t.Fatalf("invite: %+v", resp.Error)
	}
	out, _ := resp.Result.(map[string]any)
	inviteID, _ := out["id"].(string)

	// Carol tries to accept bob's invite — should fail.
	ctxCarol := withUser(context.Background(), "carol", "user")
	resp = h.handleProjectInviteAccept(ctxCarol, rpcRequest("project/invite/accept", 1, map[string]any{
		"inviteId": inviteID,
	}))
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("carol accepting bob's invite: want ErrCodeInvalidParams, got %+v", resp.Error)
	}
	_ = carol
}

func TestHandleProjectInviteCancel_RoundTrip(t *testing.T) {
	t.Parallel()
	h, _, _, scope := setupProjectWithOwner(t)
	seedProjectUser(t, h.projects, "bob")

	ctxOwner := scopeCtx(h, context.Background(), scope, "alice")
	inv := h.handleProjectInvite(ctxOwner, rpcRequest("project/invite", 1, map[string]any{
		"username": "bob",
		"role":     string(project.RoleMember),
	}))
	if inv.Error != nil {
		t.Fatalf("invite: %+v", inv.Error)
	}
	out, _ := inv.Result.(map[string]any)
	inviteID, _ := out["id"].(string)

	resp := h.handleProjectInviteCancel(ctxOwner, rpcRequest("project/invite/cancel", 1, map[string]any{
		"inviteId": inviteID,
	}))
	if resp.Error != nil {
		t.Fatalf("cancel: %+v", resp.Error)
	}
	// Cancelled invite should no longer be acceptable.
	bob, _ := h.projects.LookupUserByUsername(context.Background(), "bob", "")
	if _, err := h.projects.AcceptInvite(context.Background(), inviteID, bob.ID); err == nil {
		t.Fatal("cancelled invite should not be acceptable")
	}
}

func TestHandleProjectMemberList_EnrichesUsername(t *testing.T) {
	t.Parallel()
	h, alice, p, scope := setupProjectWithOwner(t)
	bob := seedProjectUser(t, h.projects, "bob")
	inv, err := h.projects.InviteByUsername(context.Background(), project.InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: "bob", Role: project.RoleMember,
	})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if _, err := h.projects.AcceptInvite(context.Background(), inv.ID, bob.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}

	ctx := scopeCtx(h, context.Background(), scope, "alice")
	resp := h.handleProjectMemberList(ctx, rpcRequest("project/member/list", 1, nil))
	if resp.Error != nil {
		t.Fatalf("member list: %+v", resp.Error)
	}
	out, _ := resp.Result.(map[string]any)
	members, _ := out["members"].([]map[string]any)
	if len(members) != 2 {
		t.Fatalf("want 2 members, got %d", len(members))
	}
	usernames := map[string]bool{}
	for _, m := range members {
		usernames[m["username"].(string)] = true
	}
	if !usernames["alice"] || !usernames["bob"] {
		t.Fatalf("missing enriched usernames: %v", usernames)
	}
}

func TestHandleProjectMemberUpdateRole_InvalidRole(t *testing.T) {
	t.Parallel()
	h, _, _, scope := setupProjectWithOwner(t)
	bob := seedProjectUser(t, h.projects, "bob")
	ctx := scopeCtx(h, context.Background(), scope, "alice")

	resp := h.handleProjectMemberUpdateRole(ctx, rpcRequest("project/member/update-role", 1, map[string]any{
		"userId": bob.ID,
		"role":   "superadmin",
	}))
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams for bogus role, got %+v", resp.Error)
	}
}

func TestHandleProjectInviteDecline_RoundTrip(t *testing.T) {
	t.Parallel()
	h, alice, p, _ := setupProjectWithOwner(t)
	bob := seedProjectUser(t, h.projects, "bob")
	inv, err := h.projects.InviteByUsername(context.Background(), project.InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: "bob", Role: project.RoleMember,
	})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}

	ctxBob := withUser(context.Background(), "bob", "user")
	resp := h.handleProjectInviteDecline(ctxBob, rpcRequest("project/invite/decline", 1, map[string]any{
		"inviteId": inv.ID,
	}))
	if resp.Error != nil {
		t.Fatalf("decline: %+v", resp.Error)
	}
	// Bob should NOT be a member after declining.
	if _, err := h.projects.GetMember(context.Background(), p.ID, bob.ID); err == nil {
		t.Fatal("bob should not be a member after declining")
	}
	// Re-decline now reports not-pending so a stale UI click surfaces an
	// error rather than appearing to succeed.
	resp = h.handleProjectInviteDecline(ctxBob, rpcRequest("project/invite/decline", 1, map[string]any{
		"inviteId": inv.ID,
	}))
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("re-decline should fail; got %+v", resp.Error)
	}
}

func TestHandleProjectInviteDecline_WrongUser(t *testing.T) {
	t.Parallel()
	h, alice, p, _ := setupProjectWithOwner(t)
	seedProjectUser(t, h.projects, "bob")
	seedProjectUser(t, h.projects, "carol")
	inv, err := h.projects.InviteByUsername(context.Background(), project.InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: "bob", Role: project.RoleMember,
	})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}

	ctxCarol := withUser(context.Background(), "carol", "user")
	resp := h.handleProjectInviteDecline(ctxCarol, rpcRequest("project/invite/decline", 1, map[string]any{
		"inviteId": inv.ID,
	}))
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("carol declining bob's invite: want ErrCodeInvalidParams, got %+v", resp.Error)
	}
}

func TestHandleProjectInviteListForMe(t *testing.T) {
	t.Parallel()
	h, alice, p, _ := setupProjectWithOwner(t)
	bob := seedProjectUser(t, h.projects, "bob")
	if _, err := h.projects.InviteByUsername(context.Background(), project.InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: "bob", Role: project.RoleMember,
	}); err != nil {
		t.Fatalf("seed invite: %v", err)
	}

	ctx := withUser(context.Background(), "bob", "user")
	resp := h.handleProjectInviteListForMe(ctx, rpcRequest("project/invite/list-for-me", 1, nil))
	if resp.Error != nil {
		t.Fatalf("list-for-me: %+v", resp.Error)
	}
	out, _ := resp.Result.(map[string]any)
	invites, _ := out["invites"].([]map[string]any)
	if len(invites) != 1 || invites[0]["userId"] != bob.ID {
		t.Fatalf("want 1 invite for bob, got %+v", invites)
	}
}
