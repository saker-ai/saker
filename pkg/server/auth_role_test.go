package server

import (
	"testing"

	"github.com/cinience/saker/pkg/config"
)

func TestResolveRole_NilMapping(t *testing.T) {
	t.Parallel()
	result := &AuthResult{Username: "alice", Role: "user"}
	got := resolveRole(result, nil)
	if got != "user" {
		t.Errorf("expected user, got %s", got)
	}
}

func TestResolveRole_AdminUser(t *testing.T) {
	t.Parallel()
	result := &AuthResult{Username: "alice", Role: "user"}
	mapping := &config.RoleMappingConfig{
		AdminUsers: []string{"alice", "bob"},
	}
	got := resolveRole(result, mapping)
	if got != "admin" {
		t.Errorf("expected admin, got %s", got)
	}
}

func TestResolveRole_AdminGroup(t *testing.T) {
	t.Parallel()
	result := &AuthResult{
		Username: "charlie",
		Role:     "user",
		Groups:   []string{"dev-team", "saker-admins"},
	}
	mapping := &config.RoleMappingConfig{
		AdminGroups: []string{"saker-admins"},
	}
	got := resolveRole(result, mapping)
	if got != "admin" {
		t.Errorf("expected admin, got %s", got)
	}
}

func TestResolveRole_NoMatch_DefaultRole(t *testing.T) {
	t.Parallel()
	result := &AuthResult{
		Username: "dave",
		Role:     "user",
		Groups:   []string{"viewers"},
	}
	mapping := &config.RoleMappingConfig{
		AdminGroups: []string{"saker-admins"},
		DefaultRole: "viewer",
	}
	got := resolveRole(result, mapping)
	if got != "viewer" {
		t.Errorf("expected viewer, got %s", got)
	}
}

func TestResolveRole_NoMatch_FallbackUser(t *testing.T) {
	t.Parallel()
	result := &AuthResult{Username: "eve", Role: "user"}
	mapping := &config.RoleMappingConfig{
		AdminUsers: []string{"alice"},
	}
	got := resolveRole(result, mapping)
	if got != "user" {
		t.Errorf("expected user, got %s", got)
	}
}

func TestResolveRole_EmptyGroups(t *testing.T) {
	t.Parallel()
	result := &AuthResult{Username: "frank", Role: "user"}
	mapping := &config.RoleMappingConfig{
		AdminGroups: []string{"saker-admins"},
		DefaultRole: "user",
	}
	got := resolveRole(result, mapping)
	if got != "user" {
		t.Errorf("expected user, got %s", got)
	}
}
