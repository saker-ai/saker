package project

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// seedUser inserts a User and returns it.
func seedUser(t *testing.T, s *Store, username string, source UserSource) *User {
	t.Helper()
	u, err := s.EnsureUserFromAuth(context.Background(), source, username, username, username, "")
	if err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	return u
}

func TestEnsurePersonalProject_Idempotent(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	u := seedUser(t, s, "alice", UserSourceLocal)
	p1, err := s.EnsurePersonalProject(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	p2, err := s.EnsurePersonalProject(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if p1.ID != p2.ID {
		t.Fatalf("expected same project, got %s vs %s", p1.ID, p2.ID)
	}
	if p1.Kind != ProjectKindPersonal {
		t.Fatalf("kind: want personal, got %s", p1.Kind)
	}
	// Owner membership must exist.
	pm, err := s.GetMember(context.Background(), p1.ID, u.ID)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if pm.Role != RoleOwner {
		t.Fatalf("role: want owner, got %s", pm.Role)
	}
}

func TestEnsureLocalhostUser_PromotesGlobalAdmin(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	u, err := s.EnsureLocalhostUser(context.Background(), "1000")
	if err != nil {
		t.Fatalf("ensure localhost: %v", err)
	}
	if u.GlobalRole != "admin" {
		t.Fatalf("want admin, got %s", u.GlobalRole)
	}
	if u.Source != UserSourceLocalhost {
		t.Fatalf("want localhost source, got %s", u.Source)
	}
}

func TestCreateProject_OwnerMembership(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	u := seedUser(t, s, "alice", UserSourceLocal)
	p, err := s.CreateProject(context.Background(), CreateProjectOptions{
		Name:        "Acme",
		OwnerUserID: u.ID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pm, err := s.GetMember(context.Background(), p.ID, u.ID)
	if err != nil || pm.Role != RoleOwner {
		t.Fatalf("owner membership missing: %v %v", pm, err)
	}
	// Slug derived from name.
	if p.Slug != "acme" {
		t.Fatalf("slug: want acme, got %s", p.Slug)
	}
}

func TestCreateProject_SlugCollision(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	u := seedUser(t, s, "alice", UserSourceLocal)
	p1, _ := s.CreateProject(context.Background(), CreateProjectOptions{Name: "Acme", OwnerUserID: u.ID})
	p2, err := s.CreateProject(context.Background(), CreateProjectOptions{Name: "acme", OwnerUserID: u.ID})
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if p1.Slug == p2.Slug {
		t.Fatalf("slugs collide: %s", p1.Slug)
	}
}

func TestListProjects_OnlyMemberships(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	alice := seedUser(t, s, "alice", UserSourceLocal)
	bob := seedUser(t, s, "bob", UserSourceLocal)
	pAlice, _ := s.CreateProject(context.Background(), CreateProjectOptions{Name: "AliceCo", OwnerUserID: alice.ID})
	_, _ = s.CreateProject(context.Background(), CreateProjectOptions{Name: "BobCo", OwnerUserID: bob.ID})

	list, err := s.ListProjects(context.Background(), alice.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Project.ID != pAlice.ID {
		t.Fatalf("alice should only see her own project, got %+v", list)
	}
	if list[0].Role != RoleOwner {
		t.Fatalf("role: want owner, got %s", list[0].Role)
	}
}

func TestInviteByUsername_Errors(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	alice := seedUser(t, s, "alice", UserSourceLocal)
	bob := seedUser(t, s, "bob", UserSourceLocal)
	carol := seedUser(t, s, "carol", UserSourceLocal)
	p, _ := s.CreateProject(context.Background(), CreateProjectOptions{Name: "P", OwnerUserID: alice.ID})

	// Add bob as a viewer so we can test "viewer trying to invite".
	if _, err := s.InviteByUsername(context.Background(), InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: "bob", Role: RoleViewer,
	}); err != nil {
		t.Fatalf("invite bob: %v", err)
	}
	// Bob accepts so he shows up as a member.
	invs, _ := s.ListInvitesForUser(context.Background(), bob.ID)
	if len(invs) != 1 {
		t.Fatalf("expected 1 pending invite for bob, got %d", len(invs))
	}
	if _, err := s.AcceptInvite(context.Background(), invs[0].ID, bob.ID); err != nil {
		t.Fatalf("bob accept: %v", err)
	}

	t.Run("user not found", func(t *testing.T) {
		_, err := s.InviteByUsername(context.Background(), InviteOptions{
			ProjectID: p.ID, InviterID: alice.ID, Username: "ghost", Role: RoleViewer,
		})
		if !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("want ErrUserNotFound, got %v", err)
		}
	})

	t.Run("already member", func(t *testing.T) {
		_, err := s.InviteByUsername(context.Background(), InviteOptions{
			ProjectID: p.ID, InviterID: alice.ID, Username: "bob", Role: RoleMember,
		})
		if !errors.Is(err, ErrAlreadyMember) {
			t.Fatalf("want ErrAlreadyMember, got %v", err)
		}
	})

	t.Run("viewer cannot invite", func(t *testing.T) {
		_, err := s.InviteByUsername(context.Background(), InviteOptions{
			ProjectID: p.ID, InviterID: bob.ID, Username: "carol", Role: RoleViewer,
		})
		if !errors.Is(err, ErrInsufficientRole) {
			t.Fatalf("want ErrInsufficientRole, got %v", err)
		}
	})

	t.Run("self invite", func(t *testing.T) {
		_, err := s.InviteByUsername(context.Background(), InviteOptions{
			ProjectID: p.ID, InviterID: alice.ID, Username: "alice", Role: RoleMember,
		})
		if !errors.Is(err, ErrSelfInvite) {
			t.Fatalf("want ErrSelfInvite, got %v", err)
		}
	})

	_ = carol
}

func TestAcceptInvite_WrongUser(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	alice := seedUser(t, s, "alice", UserSourceLocal)
	bob := seedUser(t, s, "bob", UserSourceLocal)
	carol := seedUser(t, s, "carol", UserSourceLocal)
	p, _ := s.CreateProject(context.Background(), CreateProjectOptions{Name: "P", OwnerUserID: alice.ID})
	inv, err := s.InviteByUsername(context.Background(), InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: "bob", Role: RoleMember,
	})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if _, err := s.AcceptInvite(context.Background(), inv.ID, carol.ID); !errors.Is(err, ErrInviteWrongUser) {
		t.Fatalf("want ErrInviteWrongUser, got %v", err)
	}
	if _, err := s.AcceptInvite(context.Background(), inv.ID, bob.ID); err != nil {
		t.Fatalf("bob accept: %v", err)
	}
	// Re-accept should fail (status changed).
	if _, err := s.AcceptInvite(context.Background(), inv.ID, bob.ID); !errors.Is(err, ErrInviteNotPending) {
		t.Fatalf("want ErrInviteNotPending, got %v", err)
	}
}

func TestUpdateRole_SoleOwnerProtection(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	alice := seedUser(t, s, "alice", UserSourceLocal)
	p, _ := s.CreateProject(context.Background(), CreateProjectOptions{Name: "P", OwnerUserID: alice.ID})
	err := s.UpdateRole(context.Background(), UpdateRoleOptions{
		ProjectID: p.ID, ActorUserID: alice.ID, TargetUserID: alice.ID, NewRole: RoleAdmin,
	})
	if !errors.Is(err, ErrSoleOwner) {
		t.Fatalf("want ErrSoleOwner, got %v", err)
	}
}

func TestUpdateRole_AdminCannotPromoteToOwner(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	alice := seedUser(t, s, "alice", UserSourceLocal)
	bob := seedUser(t, s, "bob", UserSourceLocal)
	carol := seedUser(t, s, "carol", UserSourceLocal)
	p, _ := s.CreateProject(context.Background(), CreateProjectOptions{Name: "P", OwnerUserID: alice.ID})
	// Alice promotes Bob to admin.
	inv, _ := s.InviteByUsername(context.Background(), InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: "bob", Role: RoleAdmin,
	})
	if _, err := s.AcceptInvite(context.Background(), inv.ID, bob.ID); err != nil {
		t.Fatalf("bob accept: %v", err)
	}
	// Carol joins as member.
	inv2, _ := s.InviteByUsername(context.Background(), InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: "carol", Role: RoleMember,
	})
	if _, err := s.AcceptInvite(context.Background(), inv2.ID, carol.ID); err != nil {
		t.Fatalf("carol accept: %v", err)
	}

	// Bob (admin) tries to promote Carol to owner — must fail.
	err := s.UpdateRole(context.Background(), UpdateRoleOptions{
		ProjectID: p.ID, ActorUserID: bob.ID, TargetUserID: carol.ID, NewRole: RoleOwner,
	})
	if !errors.Is(err, ErrInsufficientRole) {
		t.Fatalf("want ErrInsufficientRole, got %v", err)
	}
}

func TestRemoveMember_SelfRemovalAllowed(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	alice := seedUser(t, s, "alice", UserSourceLocal)
	bob := seedUser(t, s, "bob", UserSourceLocal)
	p, _ := s.CreateProject(context.Background(), CreateProjectOptions{Name: "P", OwnerUserID: alice.ID})
	inv, _ := s.InviteByUsername(context.Background(), InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: "bob", Role: RoleMember,
	})
	if _, err := s.AcceptInvite(context.Background(), inv.ID, bob.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := s.RemoveMember(context.Background(), p.ID, bob.ID, bob.ID); err != nil {
		t.Fatalf("self remove: %v", err)
	}
	if _, err := s.GetMember(context.Background(), p.ID, bob.ID); !errors.Is(err, ErrNotMember) {
		t.Fatalf("want ErrNotMember, got %v", err)
	}
}

func TestSoftDeleteProject_PersonalImmutable(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	u := seedUser(t, s, "alice", UserSourceLocal)
	p, _ := s.EnsurePersonalProject(context.Background(), u.ID)
	if err := s.SoftDeleteProject(context.Background(), p.ID, u.ID); !errors.Is(err, ErrPersonalImmutable) {
		t.Fatalf("want ErrPersonalImmutable, got %v", err)
	}
}

func TestSoftDeleteProject_OwnerOnly(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	alice := seedUser(t, s, "alice", UserSourceLocal)
	bob := seedUser(t, s, "bob", UserSourceLocal)
	p, _ := s.CreateProject(context.Background(), CreateProjectOptions{Name: "P", OwnerUserID: alice.ID})
	inv, _ := s.InviteByUsername(context.Background(), InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: "bob", Role: RoleAdmin,
	})
	if _, err := s.AcceptInvite(context.Background(), inv.ID, bob.ID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := s.SoftDeleteProject(context.Background(), p.ID, bob.ID); !errors.Is(err, ErrInsufficientRole) {
		t.Fatalf("admin should not delete: got %v", err)
	}
	if err := s.SoftDeleteProject(context.Background(), p.ID, alice.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
}

// TestEnsureUserFromAuth_Concurrent verifies that concurrent first-request
// bursts for the same identity produce exactly one row, not N. Without the
// per-key provisioning lock the SELECT-then-CREATE flow would race and either
// crash with a unique-constraint error or insert duplicates depending on the
// dialect.
func TestEnsureUserFromAuth_Concurrent(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	const N = 16
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		ids  = map[string]struct{}{}
		errs []error
	)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, err := s.EnsureUserFromAuth(context.Background(), UserSourceLocal, "alice", "ext", "Alice", "")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			ids[u.ID] = struct{}{}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent errors: %v", errs)
	}
	if len(ids) != 1 {
		t.Fatalf("want 1 distinct user ID, got %d: %v", len(ids), ids)
	}
	// And the DB itself must contain exactly one matching row.
	var n int64
	if err := s.DB().Model(&User{}).Where("source = ? AND username = ?", UserSourceLocal, "alice").Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 user row, got %d", n)
	}
}

// TestEnsurePersonalProject_Concurrent guards the same race for the personal-
// project provisioning path: 8 goroutines, all the same userID, must result
// in exactly one personal project + one owner membership.
func TestEnsurePersonalProject_Concurrent(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	u := seedUser(t, s, "alice", UserSourceLocal)
	const N = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		ids  = map[string]struct{}{}
		errs []error
	)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := s.EnsurePersonalProject(context.Background(), u.ID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			ids[p.ID] = struct{}{}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent errors: %v", errs)
	}
	if len(ids) != 1 {
		t.Fatalf("want 1 distinct project ID, got %d: %v", len(ids), ids)
	}
	var projectCount, memberCount int64
	if err := s.DB().Model(&Project{}).
		Where("owner_user_id = ? AND kind = ?", u.ID, ProjectKindPersonal).
		Count(&projectCount).Error; err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if projectCount != 1 {
		t.Fatalf("want 1 project, got %d", projectCount)
	}
	if err := s.DB().Model(&ProjectMember{}).
		Where("user_id = ? AND role = ?", u.ID, RoleOwner).
		Count(&memberCount).Error; err != nil {
		t.Fatalf("count members: %v", err)
	}
	if memberCount != 1 {
		t.Fatalf("want 1 owner membership, got %d", memberCount)
	}
}
