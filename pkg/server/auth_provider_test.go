package server

import (
	"context"
	"testing"

	"github.com/saker-ai/saker/pkg/config"
)

func TestLocalProvider_Authenticate(t *testing.T) {
	t.Parallel()
	am, adminPass, _ := newMultiUserAuth(t)
	lp := NewLocalProvider(am)

	if lp.Name() != "local" {
		t.Errorf("expected name=local, got %s", lp.Name())
	}
	if lp.Type() != "password" {
		t.Errorf("expected type=password, got %s", lp.Type())
	}

	// Admin login.
	result, err := lp.Authenticate(context.Background(), "admin", adminPass)
	if err != nil {
		t.Fatalf("admin auth: %v", err)
	}
	if result.Username != "admin" {
		t.Errorf("expected username=admin, got %s", result.Username)
	}
	if result.Role != "admin" {
		t.Errorf("expected role=admin, got %s", result.Role)
	}
	if result.Provider != "local" {
		t.Errorf("expected provider=local, got %s", result.Provider)
	}
}

func TestLocalProvider_AuthenticateRegularUser(t *testing.T) {
	t.Parallel()
	am, _, userPass := newMultiUserAuth(t)
	lp := NewLocalProvider(am)

	result, err := lp.Authenticate(context.Background(), "alice", userPass)
	if err != nil {
		t.Fatalf("user auth: %v", err)
	}
	if result.Username != "alice" {
		t.Errorf("expected username=alice, got %s", result.Username)
	}
	if result.Role != "user" {
		t.Errorf("expected role=user, got %s", result.Role)
	}
}

func TestLocalProvider_AuthenticateInvalidPassword(t *testing.T) {
	t.Parallel()
	am, _, _ := newMultiUserAuth(t)
	lp := NewLocalProvider(am)

	_, err := lp.Authenticate(context.Background(), "admin", "wrongpassword")
	if err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLocalProvider_AuthenticateUnknownUser(t *testing.T) {
	t.Parallel()
	am, _, _ := newMultiUserAuth(t)
	lp := NewLocalProvider(am)

	_, err := lp.Authenticate(context.Background(), "nobody", "pass")
	if err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestAuthenticateWithProviders_LocalFirst(t *testing.T) {
	t.Parallel()
	adminPass := "adminpass"
	cfg := &config.WebAuthConfig{
		Username: "admin",
		Password: hashPassword(t, adminPass),
	}
	am := NewAuthManager(cfg, nil)

	result := am.authenticateWithProviders(context.Background(), "admin", adminPass)
	if result == nil {
		t.Fatal("expected auth result, got nil")
	}
	if result.Provider != "local" {
		t.Errorf("expected provider=local, got %s", result.Provider)
	}
}

func TestAuthenticateWithProviders_Fails(t *testing.T) {
	t.Parallel()
	cfg := &config.WebAuthConfig{
		Username: "admin",
		Password: hashPassword(t, "secret"),
	}
	am := NewAuthManager(cfg, nil)

	result := am.authenticateWithProviders(context.Background(), "admin", "wrong")
	if result != nil {
		t.Errorf("expected nil for wrong password, got %+v", result)
	}
}

func TestGetUserInfo_CacheHit(t *testing.T) {
	t.Parallel()
	am := NewAuthManager(nil, nil)

	// Store a cached result using the public cacheUserInfo method.
	am.cacheUserInfo(&AuthResult{
		Username:    "alice",
		DisplayName: "Alice Zhang",
		Email:       "alice@example.com",
		Provider:    "ldap",
	})

	info := am.GetUserInfo("alice")
	if info == nil {
		t.Fatal("expected cached info")
	}
	if info.DisplayName != "Alice Zhang" {
		t.Errorf("expected displayName=Alice Zhang, got %s", info.DisplayName)
	}
}

func TestGetUserInfo_CacheMiss(t *testing.T) {
	t.Parallel()
	am := NewAuthManager(nil, nil)

	info := am.GetUserInfo("nobody")
	if info != nil {
		t.Errorf("expected nil for uncached user, got %+v", info)
	}
}
