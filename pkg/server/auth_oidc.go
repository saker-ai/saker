package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/saker-ai/saker/pkg/config"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCProvider authenticates users via OAuth2/OIDC (Google, GitHub, Keycloak, etc.).
type OIDCProvider struct {
	cfg       *config.OIDCConfig
	provider  *oidc.Provider
	oauth2Cfg *oauth2.Config
	verifier  *oidc.IDTokenVerifier
	log       *slog.Logger
	states    sync.Map // state → expiry (CSRF protection)

	initOnce  sync.Once
	initErr   error
	cleanOnce sync.Once
	stopClean chan struct{}
}

// NewOIDCProvider creates a new OIDC authentication provider.
// Discovery is deferred until first use to avoid blocking startup.
func NewOIDCProvider(cfg *config.OIDCConfig, log *slog.Logger) *OIDCProvider {
	if log == nil {
		log = slog.Default()
	}
	return &OIDCProvider{cfg: cfg, log: log}
}

func (p *OIDCProvider) Name() string { return "oidc" }
func (p *OIDCProvider) Type() string { return "redirect" }

// Authenticate is not used for OIDC — the redirect flow uses InitiateLogin/HandleCallback.
func (p *OIDCProvider) Authenticate(_ context.Context, _, _ string) (*AuthResult, error) {
	return nil, ErrInvalidCredentials
}

// init performs OIDC discovery (once).
func (p *OIDCProvider) init(ctx context.Context) error {
	p.initOnce.Do(func() {
		provider, err := oidc.NewProvider(ctx, p.cfg.Issuer)
		if err != nil {
			p.initErr = fmt.Errorf("oidc discovery for %s: %w", p.cfg.Issuer, err)
			return
		}
		p.provider = provider

		scopes := p.cfg.Scopes
		if len(scopes) == 0 {
			scopes = []string{oidc.ScopeOpenID, "profile", "email"}
		}

		clientSecret := expandEnvVar(p.cfg.ClientSecret)
		p.oauth2Cfg = &oauth2.Config{
			ClientID:     p.cfg.ClientID,
			ClientSecret: clientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  p.cfg.RedirectURL,
			Scopes:       scopes,
		}

		p.verifier = provider.Verifier(&oidc.Config{ClientID: p.cfg.ClientID})
	})
	return p.initErr
}

// InitiateLogin returns the OAuth2 authorization URL and the CSRF state token.
func (p *OIDCProvider) InitiateLogin(ctx context.Context) (redirectURL, state string, err error) {
	if err := p.init(ctx); err != nil {
		return "", "", err
	}

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state = hex.EncodeToString(b)

	// Store state with 10-minute expiry.
	p.states.Store(state, time.Now().Add(10*time.Minute))

	// Start lazy cleanup goroutine to sweep expired states.
	p.startCleanup()

	redirectURL = p.oauth2Cfg.AuthCodeURL(state)
	return redirectURL, state, nil
}

// HandleCallback exchanges the authorization code for tokens and returns user info.
func (p *OIDCProvider) HandleCallback(ctx context.Context, code, state string) (*AuthResult, error) {
	if err := p.init(ctx); err != nil {
		return nil, err
	}

	// Verify CSRF state.
	expiryVal, ok := p.states.LoadAndDelete(state)
	if !ok {
		return nil, fmt.Errorf("oidc: invalid or expired state")
	}
	if expiry, ok := expiryVal.(time.Time); ok && time.Now().After(expiry) {
		return nil, fmt.Errorf("oidc: state expired")
	}

	// Exchange code for token.
	token, err := p.oauth2Cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("oidc token exchange: %w", err)
	}

	// Extract and verify ID token.
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("oidc: no id_token in response")
	}

	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("oidc verify id_token: %w", err)
	}

	// Parse claims.
	var claims map[string]json.RawMessage
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oidc parse claims: %w", err)
	}

	result := &AuthResult{
		Username:    p.claimString(claims, p.usernameClaim(), ""),
		Email:       p.claimString(claims, p.emailClaim(), ""),
		DisplayName: p.claimString(claims, p.nameClaim(), ""),
		AvatarURL:   p.claimString(claims, p.avatarClaim(), ""),
		Groups:      p.claimStringSlice(claims, p.groupsClaim()),
		Provider:    "oidc",
		Role:        "user", // resolved later by resolveRole
	}

	// Fallback: use "sub" if username is empty.
	if result.Username == "" {
		result.Username = p.claimString(claims, "sub", idToken.Subject)
	}

	p.log.Info("oidc auth success", "username", result.Username, "email", result.Email)
	return result, nil
}

// HandleOIDCLogin is the HTTP handler that initiates the OIDC redirect flow.
func (p *OIDCProvider) HandleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	redirectURL, _, err := p.InitiateLogin(r.Context())
	if err != nil {
		p.log.Error("oidc login initiation failed", "error", err)
		http.Error(w, "OIDC login unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// claim mapping helpers

func (p *OIDCProvider) usernameClaim() string {
	if p.cfg.ClaimMapping.Username != "" {
		return p.cfg.ClaimMapping.Username
	}
	return "preferred_username"
}

func (p *OIDCProvider) emailClaim() string {
	if p.cfg.ClaimMapping.Email != "" {
		return p.cfg.ClaimMapping.Email
	}
	return "email"
}

func (p *OIDCProvider) nameClaim() string {
	if p.cfg.ClaimMapping.Name != "" {
		return p.cfg.ClaimMapping.Name
	}
	return "name"
}

func (p *OIDCProvider) groupsClaim() string {
	if p.cfg.ClaimMapping.Groups != "" {
		return p.cfg.ClaimMapping.Groups
	}
	return "groups"
}

func (p *OIDCProvider) avatarClaim() string {
	if p.cfg.ClaimMapping.Avatar != "" {
		return p.cfg.ClaimMapping.Avatar
	}
	return "picture"
}

// claimString extracts a string claim from the claims map.
func (p *OIDCProvider) claimString(claims map[string]json.RawMessage, key, fallback string) string {
	raw, ok := claims[key]
	if !ok {
		return fallback
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return fallback
	}
	return s
}

// claimStringSlice extracts a []string claim from the claims map.
func (p *OIDCProvider) claimStringSlice(claims map[string]json.RawMessage, key string) []string {
	raw, ok := claims[key]
	if !ok {
		return nil
	}
	var ss []string
	if err := json.Unmarshal(raw, &ss); err != nil {
		return nil
	}
	return ss
}

// startCleanup lazily starts a goroutine that sweeps expired OIDC states every
// 5 minutes. Uses sync.Once so the goroutine is only launched when the first
// state is actually stored (i.e. OIDC is in use).
func (p *OIDCProvider) startCleanup() {
	p.cleanOnce.Do(func() {
		p.stopClean = make(chan struct{})
		go p.cleanupStatesLoop()
	})
}

// cleanupStatesLoop periodically removes expired OIDC states from the sync.Map.
func (p *OIDCProvider) cleanupStatesLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopClean:
			return
		case <-ticker.C:
			now := time.Now()
			p.states.Range(func(key, value any) bool {
				if expiry, ok := value.(time.Time); ok && now.After(expiry) {
					p.states.Delete(key)
				}
				return true
			})
		}
	}
}

// Close stops the OIDC states cleanup goroutine.
func (p *OIDCProvider) Close() {
	if p.stopClean != nil {
		close(p.stopClean)
	}
}
