package server

import (
	"context"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/project"
)

// TestPermissionMatrix_AllRoles drives the methodMinRole table against every
// (method, role) pair using the real resolveScope path. It is the
// authoritative gate that the role tiers stay aligned with the plan; if a new
// method is added to the dispatcher, this test forces it into either
// methodSkipProject or methodMinRole.
//
// Goals:
//   - viewer can ALWAYS read but NEVER mutate
//   - member can edit but never manage members or delete the project
//   - admin can manage members but never delete the project
//   - owner can do everything
func TestPermissionMatrix_AllRoles(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)

	owner := seedProjectUser(t, h.projects, "owner-user")
	admin := seedProjectUser(t, h.projects, "admin-user")
	member := seedProjectUser(t, h.projects, "member-user")
	viewer := seedProjectUser(t, h.projects, "viewer-user")

	p, err := h.projects.CreateProject(context.Background(), project.CreateProjectOptions{
		Name: "matrix", OwnerUserID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	for _, spec := range []struct {
		username string
		role     project.Role
	}{
		{"admin-user", project.RoleAdmin},
		{"member-user", project.RoleMember},
		{"viewer-user", project.RoleViewer},
	} {
		inv, err := h.projects.InviteByUsername(context.Background(), project.InviteOptions{
			ProjectID: p.ID, InviterID: owner.ID,
			Username: spec.username, Role: spec.role,
		})
		if err != nil {
			t.Fatalf("invite %s: %v", spec.username, err)
		}
		var uid string
		switch spec.username {
		case "admin-user":
			uid = admin.ID
		case "member-user":
			uid = member.ID
		case "viewer-user":
			uid = viewer.ID
		}
		if _, err := h.projects.AcceptInvite(context.Background(), inv.ID, uid); err != nil {
			t.Fatalf("accept %s: %v", spec.username, err)
		}
	}

	// callers maps a friendly role name to (username, expected scope role).
	callers := []struct {
		username string
		role     project.Role
	}{
		{"owner-user", project.RoleOwner},
		{"admin-user", project.RoleAdmin},
		{"member-user", project.RoleMember},
		{"viewer-user", project.RoleViewer},
	}

	// matrix entries live in permissionMatrixExpected (declared at package scope
	// so the drift-detection tests can share one source of truth). The map
	// covers read-tier (RoleViewer), member-tier, admin-tier, and owner-only
	// methods. TestPermissionMatrix_TableEntriesExercised enforces that every
	// methodMinRole entry has a matching expectation here, so a new method
	// added to the dispatcher without role-tier coverage fails CI.
	for method, minRole := range permissionMatrixExpected {
		method := method
		minRole := minRole
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			for _, c := range callers {
				ctx := withUser(context.Background(), c.username, "user")
				req := rpcRequest(method, 1, map[string]any{"projectId": p.ID})
				_, deny := h.resolveScope(ctx, req)
				shouldPass := c.role.AtLeast(minRole)
				if shouldPass && deny != nil {
					t.Errorf("%s as %s: expected pass, got deny code=%d msg=%q",
						method, c.role, deny.Error.Code, deny.Error.Message)
				}
				if !shouldPass {
					if deny == nil {
						t.Errorf("%s as %s: expected deny (need %s), got pass", method, c.role, minRole)
					} else if deny.Error.Code != ErrCodeProjectAccess {
						t.Errorf("%s as %s: deny code = %d want %d", method, c.role, deny.Error.Code, ErrCodeProjectAccess)
					}
				}
			}
		})
	}
}

// TestPermissionMatrix_DefaultsToViewer asserts methods absent from the table
// are read-tier (anyone in the project can call them). This is intentional
// since most read-only RPCs don't need an explicit entry.
func TestPermissionMatrix_DefaultsToViewer(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	owner := seedProjectUser(t, h.projects, "alice")
	bob := seedProjectUser(t, h.projects, "bob")
	p, err := h.projects.CreateProject(context.Background(), project.CreateProjectOptions{
		Name: "P", OwnerUserID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	inv, err := h.projects.InviteByUsername(context.Background(), project.InviteOptions{
		ProjectID: p.ID, InviterID: owner.ID, Username: "bob", Role: project.RoleViewer,
	})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if _, err := h.projects.AcceptInvite(context.Background(), inv.ID, bob.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}

	// "made/up/method" is not in methodMinRole — should default to RoleViewer
	// and let bob (viewer) through.
	ctx := withUser(context.Background(), "bob", "user")
	_, deny := h.resolveScope(ctx, rpcRequest("made/up/method", 1, map[string]any{
		"projectId": p.ID,
	}))
	if deny != nil {
		t.Fatalf("unknown method should default to viewer-readable, got %+v", deny.Error)
	}
}

// TestPermissionMatrix_SkipMethodsHaveNoMinRole guards against drift between
// methodSkipProject and methodMinRole — a method should not appear in both,
// since skip means "no project context required" and min-role implies one.
func TestPermissionMatrix_SkipMethodsHaveNoMinRole(t *testing.T) {
	t.Parallel()
	for method := range methodSkipProject {
		if _, found := methodMinRole[method]; found {
			t.Errorf("method %q is in BOTH methodSkipProject and methodMinRole — pick one", method)
		}
	}
}

// TestPermissionMatrix_NoUnreachableMembersInTable catches typos by ensuring
// every role used as a value in methodMinRole is a known role.
func TestPermissionMatrix_NoUnreachableMembersInTable(t *testing.T) {
	t.Parallel()
	known := map[project.Role]bool{
		project.RoleViewer: true,
		project.RoleMember: true,
		project.RoleAdmin:  true,
		project.RoleOwner:  true,
	}
	for method, role := range methodMinRole {
		if !known[role] {
			t.Errorf("method %q maps to unknown role %q", method, role)
		}
	}
}

// TestPermissionMatrix_ManageMethodsAreAdminOrAbove pins the high-impact
// methods to admin/owner so an accidental table edit can't downgrade them.
// Each entry encodes "this method MUST require at least this role".
func TestPermissionMatrix_ManageMethodsAreAdminOrAbove(t *testing.T) {
	t.Parallel()
	guards := map[string]project.Role{
		"project/invite":             project.RoleAdmin,
		"project/member/update-role": project.RoleAdmin,
		"project/member/remove":      project.RoleAdmin,
		"settings/update":            project.RoleAdmin,
		"project/delete":             project.RoleOwner,
		"project/transfer":           project.RoleOwner,
	}
	for method, want := range guards {
		got, ok := methodMinRole[method]
		if !ok {
			t.Errorf("%s missing from methodMinRole — manage-tier method must be explicit", method)
			continue
		}
		// "want" is the *minimum* — got must be at least want.
		if !got.AtLeast(want) {
			t.Errorf("%s: methodMinRole=%s, but plan requires at least %s", method, got, want)
		}
	}
}

// TestPermissionMatrix_ProjectInviteAcceptIsSkippable double-checks the
// invite-accept path: it must NOT require pre-existing membership, otherwise
// you'd need to be a member to accept the invite that makes you a member.
func TestPermissionMatrix_ProjectInviteAcceptIsSkippable(t *testing.T) {
	t.Parallel()
	for _, m := range []string{"project/invite/accept", "project/invite/decline", "project/invite/list-for-me"} {
		if !methodSkipProject[m] {
			t.Errorf("%s must bypass scope (chicken-and-egg w/ membership)", m)
		}
	}
}

// TestPermissionMatrix_ManageMatrixCoverage scans every method in the table
// and asserts they fall into a tier the plan recognises. Catches stragglers
// like "thread/foo: project.Role(\"god\")" that would slip through type checks.
func TestPermissionMatrix_ManageMatrixCoverage(t *testing.T) {
	t.Parallel()
	for method, role := range methodMinRole {
		switch role {
		case project.RoleViewer, project.RoleMember, project.RoleAdmin, project.RoleOwner:
			// ok
		default:
			t.Errorf("method %s: role %q not in {viewer,member,admin,owner}", method, role)
		}
		// Cheap typo check: method names follow `<resource>/<verb>` shape.
		if !strings.Contains(method, "/") {
			t.Errorf("method %q: expected slash-separated name", method)
		}
	}
}

// permissionMatrixExpected mirrors the matrix map inside
// TestPermissionMatrix_AllRoles. Keeping it as a package-level value lets the
// drift test and the runtime test share one source of truth instead of
// reflecting into a closure.
var permissionMatrixExpected = map[string]project.Role{
	"thread/list":                project.RoleViewer,
	"thread/get":                 project.RoleViewer,
	"canvas/load":                project.RoleViewer,
	"settings/get":               project.RoleViewer,
	"project/member/list":        project.RoleViewer,
	"thread/create":              project.RoleMember,
	"thread/update":              project.RoleMember,
	"thread/delete":              project.RoleMember,
	"thread/interrupt":           project.RoleMember,
	"turn/send":                  project.RoleMember,
	"turn/cancel":                project.RoleMember,
	"approval/respond":           project.RoleMember,
	"question/respond":           project.RoleMember,
	"canvas/save":                project.RoleMember,
	"canvas/text-gen":            project.RoleMember,
	"canvas/execute":             project.RoleMember,
	"canvas/run-cancel":          project.RoleMember,
	"tool/run":                   project.RoleMember,
	"skill/remove":               project.RoleMember,
	"skill/promote":              project.RoleMember,
	"skill/patch":                project.RoleMember,
	"skill/import":               project.RoleMember,
	"model/switch":               project.RoleMember,
	"media/cache":                project.RoleMember,
	"persona/save":               project.RoleMember,
	"persona/delete":             project.RoleMember,
	"memory/delete":              project.RoleMember,
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
	"project/delete":             project.RoleOwner,
	"project/transfer":           project.RoleOwner,
}

// TestPermissionMatrix_TableEntriesExercised guards against table drift: every
// method listed in methodMinRole must have an explicit role-tier assertion in
// permissionMatrixExpected. New methods added without coverage fail this test
// so reviewers must consciously decide which tier the method belongs to.
func TestPermissionMatrix_TableEntriesExercised(t *testing.T) {
	t.Parallel()
	for method, declaredMin := range methodMinRole {
		expectedMin, ok := permissionMatrixExpected[method]
		if !ok {
			t.Errorf("methodMinRole has %q with role %s but no test coverage in permissionMatrixExpected — add an entry there", method, declaredMin)
			continue
		}
		if expectedMin != declaredMin {
			t.Errorf("method %q: methodMinRole=%s but permissionMatrixExpected=%s — pick one and align the other", method, declaredMin, expectedMin)
		}
	}
	for method := range permissionMatrixExpected {
		if _, ok := methodMinRole[method]; ok {
			continue
		}
		// Read-tier methods (RoleViewer) are valid even without a methodMinRole
		// entry because the dispatcher defaults to viewer. Non-viewer entries
		// here must be backed by methodMinRole.
		if permissionMatrixExpected[method] != project.RoleViewer {
			t.Errorf("permissionMatrixExpected lists %q as %s but it's missing from methodMinRole — drift detected", method, permissionMatrixExpected[method])
		}
	}
}

// TestResolveScope_WhitespaceProjectID confirms that a projectId of only
// whitespace is treated as missing rather than as a literal value (which
// would error at GetMember with a confusing "not a member" message).
func TestResolveScope_WhitespaceProjectID(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	seedProjectUser(t, h.projects, "alice")
	ctx := withUser(context.Background(), "alice", "user")
	for _, raw := range []string{"   ", "\t", "\n  ", ""} {
		_, deny := h.resolveScope(ctx, rpcRequest("thread/create", 1, map[string]any{
			"projectId": raw,
		}))
		if deny == nil || deny.Error == nil || deny.Error.Code != ErrCodeProjectMissing {
			t.Fatalf("projectId=%q: want ErrCodeProjectMissing, got %+v", raw, deny)
		}
	}
}

// TestResolveScope_SiteAdminNotAutoMember pins down a critical security
// invariant: a global "admin" role (resolved from auth/user table) does NOT
// implicitly grant project-tier access. Site-admins still need an explicit
// project membership row to operate on a project — this matches the plan's
// "site-admin and project role are decoupled" decision.
func TestResolveScope_SiteAdminNotAutoMember(t *testing.T) {
	t.Parallel()
	h := newProjectTestHandler(t)
	owner := seedProjectUser(t, h.projects, "owner-user")
	seedProjectUser(t, h.projects, "site-admin")
	p, err := h.projects.CreateProject(context.Background(), project.CreateProjectOptions{
		Name: "P", OwnerUserID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	// site-admin authenticated globally as admin, but never invited to project P.
	ctx := withUser(context.Background(), "site-admin", "admin")
	_, deny := h.resolveScope(ctx, rpcRequest("thread/list", 1, map[string]any{
		"projectId": p.ID,
	}))
	if deny == nil || deny.Error == nil || deny.Error.Code != ErrCodeProjectAccess {
		t.Fatalf("site-admin without membership must get ErrCodeProjectAccess, got %+v", deny)
	}
}

// TestPermissionMatrix_NoOverlapWithSkip is a stricter version of
// TestPermissionMatrix_SkipMethodsHaveNoMinRole — it also checks the matrix
// expectation table, so a misclassification in either direction surfaces.
func TestPermissionMatrix_NoOverlapWithSkip(t *testing.T) {
	t.Parallel()
	for method := range permissionMatrixExpected {
		if methodSkipProject[method] {
			t.Errorf("method %q is in BOTH methodSkipProject and permissionMatrixExpected — skipped methods cannot have a role tier", method)
		}
	}
}
