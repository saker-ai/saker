package server

import (
	"testing"

	"github.com/cinience/saker/pkg/config"
)

func TestIsAuthEnabled_NilConfig(t *testing.T) {
	am := NewAuthManager(nil, nil)
	if am.IsAuthEnabled() {
		t.Error("expected IsAuthEnabled=false with nil config")
	}
}

func TestIsAuthEnabled_PasswordOnly(t *testing.T) {
	cfg := &config.WebAuthConfig{Username: "admin", Password: "hash"}
	am := NewAuthManager(cfg, nil)
	if !am.IsAuthEnabled() {
		t.Error("expected IsAuthEnabled=true with password config")
	}
}

func TestIsAuthEnabled_LDAP(t *testing.T) {
	cfg := &config.WebAuthConfig{LDAP: &config.LDAPConfig{Enabled: true}}
	am := NewAuthManager(cfg, nil)
	if !am.IsAuthEnabled() {
		t.Error("expected IsAuthEnabled=true with LDAP enabled")
	}
}

func TestIsAuthEnabled_OIDC(t *testing.T) {
	cfg := &config.WebAuthConfig{OIDC: &config.OIDCConfig{Enabled: true}}
	am := NewAuthManager(cfg, nil)
	if !am.IsAuthEnabled() {
		t.Error("expected IsAuthEnabled=true with OIDC enabled")
	}
}

func TestIsAuthEnabled_EmptyConfig(t *testing.T) {
	cfg := &config.WebAuthConfig{}
	am := NewAuthManager(cfg, nil)
	if am.IsAuthEnabled() {
		t.Error("expected IsAuthEnabled=false with empty config")
	}
}

func TestIsAuthEnabled_LDAPDisabled(t *testing.T) {
	cfg := &config.WebAuthConfig{LDAP: &config.LDAPConfig{Enabled: false}}
	am := NewAuthManager(cfg, nil)
	if am.IsAuthEnabled() {
		t.Error("expected IsAuthEnabled=false with LDAP disabled")
	}
}
