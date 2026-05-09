package server

import (
	"context"
	"testing"

	"github.com/cinience/saker/pkg/project"
)

// TestResolveScope_Disabled verifies that the scope middleware is a transparent
// no-op when no project store is wired in (embedded library mode).
func TestResolveScope_Disabled(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	ctx := context.Background()
	newCtx, deny := h.resolveScope(ctx, rpcRequest("thread/list", 1, nil))
	if deny != nil {
		t.Fatalf("expected nil deny in legacy mode, got %+v", deny)
	}
	if newCtx != ctx {
		t.Fatalf("expected unchanged ctx in legacy mode")
	}
	// Even when method has no projectId, no error should fire.
	if _, ok := project.FromContext(newCtx); ok {
		t.Fatal("expected no scope in ctx when middleware disabled")
	}
}

// TestResolveScope_SkipMethod confirms that whitelisted methods bypass the
// projectId check.
func TestResolveScope_SkipMethod(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	for _, method := range []string{"initialize", "user/me", "project/list", "project/create", "auth/update"} {
		ctx := withUser(context.Background(), "alice", "user")
		_, deny := h.resolveScope(ctx, rpcRequest(method, 1, nil))
		if deny != nil {
			t.Fatalf("%s should bypass scope, got %+v", method, deny.Error)
		}
	}
}

// TestResolveScope_Unauthenticated rejects requests with no user binding.
func TestResolveScope_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	_, deny := h.resolveScope(context.Background(), rpcRequest("thread/create", 1, map[string]any{
		"projectId": "p1",
	}))
	if deny == nil || deny.Error == nil || deny.Error.Code != ErrCodeUnauthorized {
		t.Fatalf("want ErrCodeUnauthorized, got %+v", deny)
	}
}

// TestResolveScope_MissingProjectID fails when a non-skip method omits projectId.
func TestResolveScope_MissingProjectID(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	seedProjectUser(t, h.projects, "alice")
	ctx := withUser(context.Background(), "alice", "user")
	_, deny := h.resolveScope(ctx, rpcRequest("thread/create", 1, nil))
	if deny == nil || deny.Error == nil || deny.Error.Code != ErrCodeProjectMissing {
		t.Fatalf("want ErrCodeProjectMissing, got %+v", deny)
	}
}

// TestResolveScope_NonMember rejects callers who are not in the project.
func TestResolveScope_NonMember(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	alice := seedProjectUser(t, h.projects, "alice")
	seedProjectUser(t, h.projects, "bob")
	p, err := h.projects.CreateProject(context.Background(), project.CreateProjectOptions{
		Name: "Acme", OwnerUserID: alice.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	ctx := withUser(context.Background(), "bob", "user")
	_, deny := h.resolveScope(ctx, rpcRequest("thread/list", 1, map[string]any{
		"projectId": p.ID,
	}))
	if deny == nil || deny.Error == nil || deny.Error.Code != ErrCodeProjectAccess {
		t.Fatalf("want ErrCodeProjectAccess for non-member, got %+v", deny)
	}
}

// TestResolveScope_InsufficientRole denies a viewer trying to invoke a
// member-tier method.
func TestResolveScope_InsufficientRole(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	alice := seedProjectUser(t, h.projects, "alice")
	bob := seedProjectUser(t, h.projects, "bob")
	p, err := h.projects.CreateProject(context.Background(), project.CreateProjectOptions{
		Name: "P", OwnerUserID: alice.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	inv, err := h.projects.InviteByUsername(context.Background(), project.InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: "bob", Role: project.RoleViewer,
	})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if _, err := h.projects.AcceptInvite(context.Background(), inv.ID, bob.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}

	ctx := withUser(context.Background(), "bob", "user")
	// thread/create is in methodMinRole as RoleMember; viewer must fail.
	_, deny := h.resolveScope(ctx, rpcRequest("thread/create", 1, map[string]any{
		"projectId": p.ID,
	}))
	if deny == nil || deny.Error == nil || deny.Error.Code != ErrCodeProjectAccess {
		t.Fatalf("want ErrCodeProjectAccess for viewer→member, got %+v", deny)
	}

	// A read method (default RoleViewer) must succeed.
	newCtx, denyRead := h.resolveScope(ctx, rpcRequest("thread/list", 1, map[string]any{
		"projectId": p.ID,
	}))
	if denyRead != nil {
		t.Fatalf("viewer reading should succeed, got %+v", denyRead.Error)
	}
	scope, ok := project.FromContext(newCtx)
	if !ok {
		t.Fatal("expected scope in ctx after success")
	}
	if scope.Role != project.RoleViewer {
		t.Fatalf("scope role: want viewer, got %s", scope.Role)
	}
	if scope.UserID != bob.ID {
		t.Fatalf("scope user: want bob, got %s", scope.UserID)
	}
}

// TestResolveScope_OwnerSucceeds end-to-end happy path: owner gets a fully
// populated scope in ctx.
func TestResolveScope_OwnerSucceeds(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	alice := seedProjectUser(t, h.projects, "alice")
	p, err := h.projects.CreateProject(context.Background(), project.CreateProjectOptions{
		Name: "P", OwnerUserID: alice.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	ctx := withUser(context.Background(), "alice", "user")
	newCtx, deny := h.resolveScope(ctx, rpcRequest("project/delete", 1, map[string]any{
		"projectId": p.ID,
	}))
	if deny != nil {
		t.Fatalf("owner should pass project/delete, got %+v", deny.Error)
	}
	scope, ok := project.FromContext(newCtx)
	if !ok {
		t.Fatal("expected scope in ctx after success")
	}
	if scope.Role != project.RoleOwner || scope.ProjectID != p.ID {
		t.Fatalf("unexpected scope: %+v", scope)
	}
	if scope.Paths.Root == "" {
		t.Fatal("scope.Paths.Root must be populated")
	}
}

// TestResolveRESTScope_LegacyPassthrough returns no error and unchanged ctx
// when no project store is wired in.
func TestResolveRESTScope_LegacyPassthrough(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	ctx, err := h.resolveRESTScope(context.Background(), "alice", "p1")
	if err != nil {
		t.Fatalf("legacy mode should not error: %v", err)
	}
	if _, ok := project.FromContext(ctx); ok {
		t.Fatal("legacy mode must not inject scope")
	}
}

// TestResolveRESTScope_Errors covers the three sentinel error paths.
func TestResolveRESTScope_Errors(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	alice := seedProjectUser(t, h.projects, "alice")

	// Missing user → errRESTAuthRequired.
	if _, err := h.resolveRESTScope(context.Background(), "", "p1"); err != errRESTAuthRequired {
		t.Fatalf("want errRESTAuthRequired, got %v", err)
	}
	// Missing projectID → errRESTProjectMissing.
	if _, err := h.resolveRESTScope(context.Background(), "alice", "  "); err != errRESTProjectMissing {
		t.Fatalf("want errRESTProjectMissing, got %v", err)
	}
	// Unknown projectID → wrapped lookup error.
	if _, err := h.resolveRESTScope(context.Background(), "alice", "no-such-project"); err == nil {
		t.Fatal("expected error for non-member project")
	}

	// Happy path: alice as owner of her own project.
	p, err := h.projects.CreateProject(context.Background(), project.CreateProjectOptions{
		Name: "P", OwnerUserID: alice.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	ctx, err := h.resolveRESTScope(context.Background(), "alice", p.ID)
	if err != nil {
		t.Fatalf("rest scope happy path: %v", err)
	}
	scope, ok := project.FromContext(ctx)
	if !ok || scope.ProjectID != p.ID || scope.Role != project.RoleOwner {
		t.Fatalf("unexpected scope: %+v ok=%v", scope, ok)
	}
}
