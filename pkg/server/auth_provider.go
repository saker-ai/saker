package server

import (
	"context"
	"errors"
)

// ErrInvalidCredentials is returned when authentication fails.
var ErrInvalidCredentials = errors.New("invalid credentials")

// AuthProvider abstracts an authentication backend (local, LDAP, OIDC, etc.).
type AuthProvider interface {
	// Name returns the provider identifier (e.g., "local", "ldap", "oidc").
	Name() string

	// Type returns the auth flow type: "password" for username/password
	// providers (local, LDAP), "redirect" for OAuth2/OIDC.
	Type() string

	// Authenticate verifies credentials and returns user info on success.
	// Only called for "password" type providers.
	Authenticate(ctx context.Context, username, password string) (*AuthResult, error)
}

// AuthResult holds the result of a successful authentication.
type AuthResult struct {
	Username    string            // Canonical username (used as profile name).
	Role        string            // "admin" or "user".
	DisplayName string            // Human-readable name (e.g., "Alice Zhang").
	Email       string            // User email address.
	AvatarURL   string            // Avatar URL (from OAuth2 provider or Gravatar).
	Groups      []string          // Group memberships (for role mapping).
	Provider    string            // Which provider authenticated this user.
	Extra       map[string]string // Provider-specific metadata.
}

// LocalProvider authenticates against the local WebAuthConfig user list.
type LocalProvider struct {
	am *AuthManager
}

// NewLocalProvider wraps the existing AuthManager credential check as a provider.
func NewLocalProvider(am *AuthManager) *LocalProvider {
	return &LocalProvider{am: am}
}

func (p *LocalProvider) Name() string { return "local" }
func (p *LocalProvider) Type() string { return "password" }

func (p *LocalProvider) Authenticate(_ context.Context, username, password string) (*AuthResult, error) {
	matched, role := p.am.checkCredentials(username, password)
	if !matched {
		return nil, ErrInvalidCredentials
	}
	return &AuthResult{
		Username: username,
		Role:     role,
		Provider: "local",
	}, nil
}
