package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cinience/saker/pkg/project"
)

// TestHandleProjectList_AutoEnsuresPersonal verifies that listing projects
// for a fresh user transparently provisions the personal project so the
// dropdown is never empty.
func TestHandleProjectList_AutoEnsuresPersonal(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	seedProjectUser(t, h.projects, "alice")
	ctx := withUser(context.Background(), "alice", "user")

	resp := h.handleProjectList(ctx, rpcRequest("project/list", 1, nil))
	if resp.Error != nil {
		t.Fatalf("project/list: %+v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T", resp.Result)
	}
	projects, _ := result["projects"].([]map[string]any)
	if len(projects) != 1 {
		t.Fatalf("want 1 personal project, got %d: %+v", len(projects), projects)
	}
	if projects[0]["kind"] != string(project.ProjectKindPersonal) {
		t.Fatalf("want personal kind, got %v", projects[0]["kind"])
	}
	if projects[0]["role"] != string(project.RoleOwner) {
		t.Fatalf("want owner role, got %v", projects[0]["role"])
	}
}

func TestHandleProjectCreate_OwnerMembership(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	u := seedProjectUser(t, h.projects, "alice")
	ctx := withUser(context.Background(), "alice", "user")

	resp := h.handleProjectCreate(ctx, rpcRequest("project/create", 1, map[string]any{
		"name": "Acme Corp",
	}))
	if resp.Error != nil {
		t.Fatalf("project/create: %+v", resp.Error)
	}
	result, _ := resp.Result.(map[string]any)
	pid, _ := result["id"].(string)
	if pid == "" {
		t.Fatalf("result missing id: %+v", result)
	}
	if result["role"] != string(project.RoleOwner) {
		t.Fatalf("want owner role, got %v", result["role"])
	}
	// Owner membership exists in the store.
	pm, err := h.projects.GetMember(context.Background(), pid, u.ID)
	if err != nil {
		t.Fatalf("get owner membership: %v", err)
	}
	if pm.Role != project.RoleOwner {
		t.Fatalf("want owner, got %s", pm.Role)
	}
}

func TestHandleProjectCreate_RequiresName(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	seedProjectUser(t, h.projects, "alice")
	ctx := withUser(context.Background(), "alice", "user")

	resp := h.handleProjectCreate(ctx, rpcRequest("project/create", 1, map[string]any{
		"name": "   ",
	}))
	if resp.Error == nil {
		t.Fatalf("expected error for blank name")
	}
	if resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want %d, got %d", ErrCodeInvalidParams, resp.Error.Code)
	}
}

func TestHandleProjectGet_NonMemberDenied(t *testing.T) {
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

	// bob asks for alice's project — should be denied.
	ctx := withUser(context.Background(), "bob", "user")
	resp := h.handleProjectGet(ctx, rpcRequest("project/get", 1, map[string]any{
		"projectId": p.ID,
	}))
	if resp.Error == nil {
		t.Fatalf("expected error for non-member access")
	}
	if resp.Error.Code != ErrCodeProjectAccess {
		t.Fatalf("want %d, got %d (%s)", ErrCodeProjectAccess, resp.Error.Code, resp.Error.Message)
	}
}

func TestHandleProjectDelete_PersonalImmutable(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	u := seedProjectUser(t, h.projects, "alice")
	personal, err := h.projects.EnsurePersonalProject(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ensure personal: %v", err)
	}

	// Build a scoped context the way resolveScope would have done.
	scope := project.Scope{
		UserID: u.ID, Username: "alice",
		ProjectID: personal.ID, Role: project.RoleOwner,
		Paths: project.BuildPaths(h.dataDir, personal.ID),
	}
	ctx := project.WithScope(withUser(context.Background(), "alice", "user"), scope)

	resp := h.handleProjectDelete(ctx, rpcRequest("project/delete", 1, map[string]any{
		"projectId": personal.ID,
	}))
	if resp.Error == nil {
		t.Fatalf("expected error deleting personal project")
	}
	if resp.Error.Code != ErrCodeProjectAccess {
		t.Fatalf("want %d, got %d (%s)", ErrCodeProjectAccess, resp.Error.Code, resp.Error.Message)
	}
}

func TestHandleProjectDelete_OwnerOnly(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	alice := seedProjectUser(t, h.projects, "alice")
	bob := seedProjectUser(t, h.projects, "bob")
	p, err := h.projects.CreateProject(context.Background(), project.CreateProjectOptions{
		Name: "Acme", OwnerUserID: alice.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	// Promote bob to admin (still not owner).
	inv, err := h.projects.InviteByUsername(context.Background(), project.InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: "bob", Role: project.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if _, err := h.projects.AcceptInvite(context.Background(), inv.ID, bob.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}

	// Bob (admin) tries to delete — must fail.
	scopeBob := project.Scope{
		UserID: bob.ID, Username: "bob",
		ProjectID: p.ID, Role: project.RoleAdmin,
		Paths: project.BuildPaths(h.dataDir, p.ID),
	}
	ctxBob := project.WithScope(withUser(context.Background(), "bob", "user"), scopeBob)
	resp := h.handleProjectDelete(ctxBob, rpcRequest("project/delete", 1, map[string]any{
		"projectId": p.ID,
	}))
	if resp.Error == nil || resp.Error.Code != ErrCodeProjectAccess {
		t.Fatalf("admin should not delete: %+v", resp.Error)
	}

	// Alice (owner) deletes — must succeed.
	scopeAlice := project.Scope{
		UserID: alice.ID, Username: "alice",
		ProjectID: p.ID, Role: project.RoleOwner,
		Paths: project.BuildPaths(h.dataDir, p.ID),
	}
	ctxAlice := project.WithScope(withUser(context.Background(), "alice", "user"), scopeAlice)
	resp = h.handleProjectDelete(ctxAlice, rpcRequest("project/delete", 1, map[string]any{
		"projectId": p.ID,
	}))
	if resp.Error != nil {
		t.Fatalf("owner delete: %+v", resp.Error)
	}
}

func TestHandleProjectMe_ReturnsUserAndProjects(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	seedProjectUser(t, h.projects, "alice")
	ctx := withUser(context.Background(), "alice", "user")

	resp := h.handleProjectMe(ctx, rpcRequest("project/me", 1, nil))
	if resp.Error != nil {
		t.Fatalf("project/me: %+v", resp.Error)
	}
	result, _ := resp.Result.(map[string]any)
	user, _ := result["user"].(map[string]any)
	if user["username"] != "alice" {
		t.Fatalf("want username=alice, got %v", user["username"])
	}
	projs, _ := result["projects"].([]map[string]any)
	if len(projs) != 1 {
		t.Fatalf("want 1 project, got %d", len(projs))
	}
}

func TestHandleProjectList_NoStore(t *testing.T) {
	t.Parallel()
	h := &Handler{} // no projects store
	ctx := withUser(context.Background(), "alice", "user")
	resp := h.handleProjectList(ctx, rpcRequest("project/list", 1, nil))
	if resp.Error == nil || resp.Error.Code != ErrCodeProjectStore {
		t.Fatalf("want ErrCodeProjectStore, got %+v", resp.Error)
	}
}

// TestHandleProjectDelete_RemovesOnDiskDirectory exercises the GC path that
// runs after the DB tombstone: the project's data directory under
// <dataDir>/projects/<projectID> must be removed so disk usage stays bounded.
// Best-effort by design — the test asserts the happy path; failure modes are
// logged but do not fail the RPC (covered separately by code review).
func TestHandleProjectDelete_RemovesOnDiskDirectory(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	alice := seedProjectUser(t, h.projects, "alice")
	p, err := h.projects.CreateProject(context.Background(), project.CreateProjectOptions{
		Name: "Acme", OwnerUserID: alice.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Prepopulate the project directory with a representative file so we can
	// observe the cleanup. Using the same BuildPaths helper keeps the test
	// honest about the layout the handler relies on.
	paths := project.BuildPaths(h.dataDir, p.ID)
	if err := os.MkdirAll(paths.SessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.SessionsDir, "t.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	scope := project.Scope{
		UserID: alice.ID, Username: "alice",
		ProjectID: p.ID, Role: project.RoleOwner,
		Paths: paths,
	}
	ctx := project.WithScope(withUser(context.Background(), "alice", "user"), scope)
	resp := h.handleProjectDelete(ctx, rpcRequest("project/delete", 1, map[string]any{
		"projectId": p.ID,
	}))
	if resp.Error != nil {
		t.Fatalf("project/delete: %+v", resp.Error)
	}
	if _, err := os.Stat(paths.Root); !os.IsNotExist(err) {
		t.Fatalf("project root still exists after delete: stat err=%v", err)
	}
}

// TestHandleProjectDelete_EvictsSessionRegistry primes the handler's
// per-project SessionStore registry via sessionsFor (the same code path a
// scoped RPC would hit), then verifies that project/delete drops the cached
// entry. Without this, an in-flight subscription could keep writing to the
// deleted project's directory after the GC had removed it.
func TestHandleProjectDelete_EvictsSessionRegistry(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	alice := seedProjectUser(t, h.projects, "alice")
	p, err := h.projects.CreateProject(context.Background(), project.CreateProjectOptions{
		Name: "Acme", OwnerUserID: alice.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	paths := project.BuildPaths(h.dataDir, p.ID)
	scope := project.Scope{
		UserID: alice.ID, Username: "alice",
		ProjectID: p.ID, Role: project.RoleOwner,
		Paths: paths,
	}
	ctx := project.WithScope(withUser(context.Background(), "alice", "user"), scope)

	// Prime the registry through the production lookup helper. This is
	// what a scoped RPC (turn/send, thread/list, …) would do internally.
	if store := h.sessionsFor(ctx); store == nil {
		t.Fatalf("sessionsFor returned nil")
	}
	h.mu.RLock()
	reg := h.sessionRegistry
	h.mu.RUnlock()
	if reg == nil || reg.Len() != 1 {
		t.Fatalf("registry not primed: reg=%v len=%d", reg, regLen(reg))
	}

	resp := h.handleProjectDelete(ctx, rpcRequest("project/delete", 1, map[string]any{
		"projectId": p.ID,
	}))
	if resp.Error != nil {
		t.Fatalf("project/delete: %+v", resp.Error)
	}
	if reg.Len() != 0 {
		t.Fatalf("session registry not evicted after delete: len=%d", reg.Len())
	}
}

// regLen returns the registry length tolerantly for log messages.
func regLen[T any](r *project.ComponentRegistry[T]) int {
	if r == nil {
		return -1
	}
	return r.Len()
}

// TestHandleProjectTransfer_OwnerToMember covers the happy path: alice (owner)
// promotes bob (member) to owner, alice is demoted to admin atomically.
func TestHandleProjectTransfer_OwnerToMember(t *testing.T) {
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
	resp := h.handleProjectTransfer(ctx, rpcRequest("project/transfer", 1, map[string]any{
		"targetUserId": bob.ID,
	}))
	if resp.Error != nil {
		t.Fatalf("project/transfer: %+v", resp.Error)
	}

	bobMember, err := h.projects.GetMember(context.Background(), p.ID, bob.ID)
	if err != nil || bobMember.Role != project.RoleOwner {
		t.Fatalf("bob should now be owner; role=%v err=%v", bobMember.Role, err)
	}
	aliceMember, err := h.projects.GetMember(context.Background(), p.ID, alice.ID)
	if err != nil || aliceMember.Role != project.RoleAdmin {
		t.Fatalf("alice should be demoted to admin; role=%v err=%v", aliceMember.Role, err)
	}
}

// TestHandleProjectTransfer_MissingTarget rejects a transfer with no
// targetUserId so the frontend gets a clear validation error rather than
// generic store failure.
func TestHandleProjectTransfer_MissingTarget(t *testing.T) {
	t.Parallel()
	h, _, _, scope := setupProjectWithOwner(t)
	ctx := scopeCtx(h, context.Background(), scope, "alice")

	resp := h.handleProjectTransfer(ctx, rpcRequest("project/transfer", 1, map[string]any{}))
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Fatalf("want ErrCodeInvalidParams, got %+v", resp.Error)
	}
}

// TestHandleProjectTransfer_TargetNotMember surfaces ErrNotMember as a
// permission-style error so the dialog can show "user is not a member".
func TestHandleProjectTransfer_TargetNotMember(t *testing.T) {
	t.Parallel()
	h, _, _, scope := setupProjectWithOwner(t)
	bob := seedProjectUser(t, h.projects, "bob") // user exists but is NOT a member

	ctx := scopeCtx(h, context.Background(), scope, "alice")
	resp := h.handleProjectTransfer(ctx, rpcRequest("project/transfer", 1, map[string]any{
		"targetUserId": bob.ID,
	}))
	if resp.Error == nil || resp.Error.Code != ErrCodeProjectAccess {
		t.Fatalf("want ErrCodeProjectAccess for non-member target, got %+v", resp.Error)
	}
}

// TestHandleProjectTransfer_PersonalImmutable rejects a transfer attempt on
// the personal project. The personal project is the user's identity-bound
// workspace; transferring it would orphan their data.
func TestHandleProjectTransfer_PersonalImmutable(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	alice := seedProjectUser(t, h.projects, "alice")
	bob := seedProjectUser(t, h.projects, "bob")
	personal, err := h.projects.EnsurePersonalProject(context.Background(), alice.ID)
	if err != nil {
		t.Fatalf("ensure personal: %v", err)
	}
	scope := project.Scope{
		UserID: alice.ID, Username: "alice",
		ProjectID: personal.ID, Role: project.RoleOwner,
		Paths: project.BuildPaths(h.dataDir, personal.ID),
	}
	ctx := scopeCtx(h, context.Background(), scope, "alice")

	resp := h.handleProjectTransfer(ctx, rpcRequest("project/transfer", 1, map[string]any{
		"targetUserId": bob.ID,
	}))
	if resp.Error == nil || resp.Error.Code != ErrCodeProjectAccess {
		t.Fatalf("want ErrCodeProjectAccess for personal project, got %+v", resp.Error)
	}
}

// TestHandleProjectTransfer_SelfNoOp accepts a self-transfer silently — the
// API stays idempotent so a stale dialog click doesn't error.
func TestHandleProjectTransfer_SelfNoOp(t *testing.T) {
	t.Parallel()
	h, alice, p, scope := setupProjectWithOwner(t)
	ctx := scopeCtx(h, context.Background(), scope, "alice")

	resp := h.handleProjectTransfer(ctx, rpcRequest("project/transfer", 1, map[string]any{
		"targetUserId": alice.ID,
	}))
	if resp.Error != nil {
		t.Fatalf("self-transfer should be no-op; got %+v", resp.Error)
	}
	// Alice is still owner.
	m, err := h.projects.GetMember(context.Background(), p.ID, alice.ID)
	if err != nil || m.Role != project.RoleOwner {
		t.Fatalf("alice should still be owner; role=%v err=%v", m.Role, err)
	}
}
