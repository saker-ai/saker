//go:build postgres && integration

// Postgres integration tests for the project store.
//
// These tests exercise the full GORM CRUD path against a real PostgreSQL
// instance. They are intentionally excluded from the default build and test
// run; run them with:
//
//	SAKER_TEST_PG_DSN=postgres://user:pass@localhost/dbname?sslmode=disable \
//	  go test -tags 'postgres integration' ./pkg/project/dialect/...
//
// The test database must already exist. The tests create an isolated schema
// via AutoMigrate on each run and close/drop nothing — a throwaway database
// is recommended (e.g. createdb saker_test).
//
// If SAKER_TEST_PG_DSN is not set the tests are skipped automatically, so
// a plain `go test ./...` (no tags) or `go test -tags postgres ./...` never
// touches external infrastructure.
package dialect_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/saker-ai/saker/pkg/project"
)

// pgStore opens a project.Store backed by the postgres DSN in
// SAKER_TEST_PG_DSN. It skips the test if the variable is unset and
// registers Cleanup to close the connection.
func pgStore(t *testing.T) *project.Store {
	t.Helper()
	dsn := os.Getenv("SAKER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set SAKER_TEST_PG_DSN to run postgres integration tests")
	}
	s, err := project.Open(project.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("pgStore: open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedPGUser inserts a User and returns it.
func seedPGUser(t *testing.T, s *project.Store, username string) *project.User {
	t.Helper()
	u, err := s.EnsureUserFromAuth(context.Background(), project.UserSourceLocal, username, username, username, "")
	if err != nil {
		t.Fatalf("seedPGUser %s: %v", username, err)
	}
	return u
}

// TestPostgres_CreateProject_OwnerMembership opens a postgres-backed store,
// creates a user + project, and verifies that an owner ProjectMember row exists.
func TestPostgres_CreateProject_OwnerMembership(t *testing.T) {
	s := pgStore(t)

	u := seedPGUser(t, s, "pg_alice_"+t.Name())
	p, err := s.CreateProject(context.Background(), project.CreateProjectOptions{
		Name:        "PGAcme",
		OwnerUserID: u.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	pm, err := s.GetMember(context.Background(), p.ID, u.ID)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if pm.Role != project.RoleOwner {
		t.Fatalf("role: want owner, got %s", pm.Role)
	}
}

// TestPostgres_InviteByUsername_Errors exercises the invite-by-username
// failure paths: user not found, already a member, viewer cannot invite.
func TestPostgres_InviteByUsername_Errors(t *testing.T) {
	s := pgStore(t)

	// Use unique suffixes per test run so parallel or repeated runs don't collide.
	suffix := "_" + t.Name()
	alice := seedPGUser(t, s, "pg_alice"+suffix)
	bob := seedPGUser(t, s, "pg_bob"+suffix)
	carol := seedPGUser(t, s, "pg_carol"+suffix)

	p, err := s.CreateProject(context.Background(), project.CreateProjectOptions{
		Name:        "PGProject",
		OwnerUserID: alice.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Invite bob as viewer, then have him accept so he shows up as a member.
	inv, err := s.InviteByUsername(context.Background(), project.InviteOptions{
		ProjectID: p.ID, InviterID: alice.ID, Username: bob.Username, Role: project.RoleViewer,
	})
	if err != nil {
		t.Fatalf("invite bob: %v", err)
	}
	if _, err := s.AcceptInvite(context.Background(), inv.ID, bob.ID); err != nil {
		t.Fatalf("bob accept: %v", err)
	}

	t.Run("user not found", func(t *testing.T) {
		_, err := s.InviteByUsername(context.Background(), project.InviteOptions{
			ProjectID: p.ID, InviterID: alice.ID, Username: "no_such_user_xyzzy", Role: project.RoleViewer,
		})
		if !errors.Is(err, project.ErrUserNotFound) {
			t.Fatalf("want ErrUserNotFound, got %v", err)
		}
	})

	t.Run("already member", func(t *testing.T) {
		_, err := s.InviteByUsername(context.Background(), project.InviteOptions{
			ProjectID: p.ID, InviterID: alice.ID, Username: bob.Username, Role: project.RoleMember,
		})
		if !errors.Is(err, project.ErrAlreadyMember) {
			t.Fatalf("want ErrAlreadyMember, got %v", err)
		}
	})

	t.Run("viewer cannot invite", func(t *testing.T) {
		_, err := s.InviteByUsername(context.Background(), project.InviteOptions{
			ProjectID: p.ID, InviterID: bob.ID, Username: carol.Username, Role: project.RoleViewer,
		})
		if !errors.Is(err, project.ErrInsufficientRole) {
			t.Fatalf("want ErrInsufficientRole, got %v", err)
		}
	})
}

// TestPostgres_UpdateRole_SoleOwnerProtection verifies that the sole owner
// cannot be demoted.
func TestPostgres_UpdateRole_SoleOwnerProtection(t *testing.T) {
	s := pgStore(t)

	suffix := "_" + t.Name()
	alice := seedPGUser(t, s, "pg_alice"+suffix)
	p, err := s.CreateProject(context.Background(), project.CreateProjectOptions{
		Name:        "PGSoleOwner",
		OwnerUserID: alice.ID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	err = s.UpdateRole(context.Background(), project.UpdateRoleOptions{
		ProjectID:    p.ID,
		ActorUserID:  alice.ID,
		TargetUserID: alice.ID,
		NewRole:      project.RoleAdmin,
	})
	if !errors.Is(err, project.ErrSoleOwner) {
		t.Fatalf("want ErrSoleOwner, got %v", err)
	}
}

// TestPostgres_SoftDeleteProject_PersonalImmutable verifies that personal
// projects refuse deletion.
func TestPostgres_SoftDeleteProject_PersonalImmutable(t *testing.T) {
	s := pgStore(t)

	suffix := "_" + t.Name()
	u := seedPGUser(t, s, "pg_alice"+suffix)

	p, err := s.EnsurePersonalProject(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ensure personal project: %v", err)
	}

	if err := s.SoftDeleteProject(context.Background(), p.ID, u.ID); !errors.Is(err, project.ErrPersonalImmutable) {
		t.Fatalf("want ErrPersonalImmutable, got %v", err)
	}
}
