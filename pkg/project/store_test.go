package project

import (
	"path/filepath"
	"testing"
)

// memStore returns a fresh, fully-isolated Store for a test. We use a real
// sqlite file under t.TempDir (rather than ":memory:") because parallel tests
// would otherwise share the in-memory cache: glebarez/sqlite keys "file:..."
// URIs by their raw path, and GORM's connection pool can resurrect dropped
// in-memory schemas in surprising ways.
func memStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Config{DSN: filepath.Join(t.TempDir(), "app.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// fileStore opens a sqlite file under t.TempDir for cases that need a
// distinct on-disk DB (e.g., reopen-and-verify).
func fileStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	s, err := Open(Config{DSN: path})
	if err != nil {
		t.Fatalf("open file store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestOpen_FallbackPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "app.db")
	s, err := Open(Config{FallbackPath: path})
	if err != nil {
		t.Fatalf("open with fallback: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.Driver() != "sqlite" {
		t.Fatalf("driver: want sqlite, got %s", s.Driver())
	}
}

func TestOpen_EmptyConfig(t *testing.T) {
	t.Parallel()
	if _, err := Open(Config{}); err == nil {
		t.Fatal("expected error for empty config")
	}
}

func TestAutoMigrate_Idempotent(t *testing.T) {
	t.Parallel()
	s, path := fileStore(t)
	if err := s.DB().AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	_ = s.Close()
	// Reopen and migrate again: should be a no-op.
	s2, err := Open(Config{DSN: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
}

func TestUserUniqueConstraint(t *testing.T) {
	t.Parallel()
	s := memStore(t)
	u1 := &User{ID: newID(), Username: "alice", Source: UserSourceLocal}
	if err := s.DB().Create(u1).Error; err != nil {
		t.Fatalf("create alice: %v", err)
	}
	dup := &User{ID: newID(), Username: "alice", Source: UserSourceLocal}
	if err := s.DB().Create(dup).Error; err == nil {
		t.Fatal("expected uniqueness violation")
	}
	// Same username under a different source should be allowed.
	u3 := &User{ID: newID(), Username: "alice", Source: UserSourceOIDC}
	if err := s.DB().Create(u3).Error; err != nil {
		t.Fatalf("create alice@oidc: %v", err)
	}
}

func TestRoleAtLeast(t *testing.T) {
	t.Parallel()
	cases := []struct {
		r, min Role
		want   bool
	}{
		{RoleOwner, RoleAdmin, true},
		{RoleAdmin, RoleAdmin, true},
		{RoleMember, RoleAdmin, false},
		{RoleViewer, RoleMember, false},
		{RoleViewer, RoleViewer, true},
	}
	for _, c := range cases {
		if got := c.r.AtLeast(c.min); got != c.want {
			t.Errorf("%s.AtLeast(%s) = %v want %v", c.r, c.min, got, c.want)
		}
	}
}
