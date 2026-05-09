package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/project"
)

// contextKey is an unexported type for context keys to avoid collisions.
type contextKey string

const userContextKey = contextKey("saker.user")
const roleContextKey = contextKey("saker.role")

// UserFromContext extracts the authenticated username from the request context.
// Returns "" if no user is authenticated (e.g., localhost without auth).
func UserFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(userContextKey).(string); ok {
		return v
	}
	return ""
}

// RoleFromContext extracts the user role ("admin" or "user") from context.
// Returns "admin" for localhost or unauthenticated access (backward compatible).
func RoleFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(roleContextKey).(string); ok {
		return v
	}
	return "admin"
}

// AuthManager handles web authentication for remote access.
// Sessions are encoded as HMAC-signed tokens so they survive server restarts.
type AuthManager struct {
	cfg    *config.WebAuthConfig
	logger *slog.Logger
	mu     sync.RWMutex

	// Revoked tokens (logout). This is in-memory only — a logout won't
	// survive a restart, but that is acceptable: the important invariant
	// is that *login* survives restarts.
	revoked map[string]time.Time

	// stopCleanup terminates the background revoked-token cleanup goroutine.
	stopCleanup chan struct{}

	// External auth providers (LDAP, OIDC).
	providers     []AuthProvider
	oidcProvider  *OIDCProvider // direct reference for redirect flow handlers
	userInfoCache     sync.Map      // username → *userInfoCacheEntry (cached external user info)
	stopCacheCleanup chan struct{}  // signals the background cache cleanup goroutine to stop

	// sessionSigningKey is a random 32-byte HMAC key generated on startup.
	// Using a random key prevents predictability from deriving the key
	// from the admin password hash. The key is persisted to a file so
	// existing sessions survive server restarts.
	sessionSigningKey []byte
	keyFile           string // path to persisted signing key

	// projectStore lets the auth layer sync identity-provider rows into the
	// multi-tenant users table on login. nil when running in legacy
	// single-project mode — auth then behaves exactly like before.
	projectStore *project.Store
}

// IsAuthEnabled returns true when at least one authentication mechanism is
// active (local password, LDAP, or OIDC).
func (a *AuthManager) IsAuthEnabled() bool {
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	if cfg == nil {
		return false
	}
	if cfg.Username != "" || cfg.Password != "" || len(cfg.Users) > 0 {
		return true
	}
	if cfg.LDAP != nil && cfg.LDAP.Enabled {
		return true
	}
	if cfg.OIDC != nil && cfg.OIDC.Enabled {
		return true
	}
	return false
}

// SetProjectStore wires the multi-tenant project store so login flows can
// upsert rows into the users table. Safe to pass nil to disable (legacy mode).
func (a *AuthManager) SetProjectStore(store *project.Store) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.projectStore = store
}

// providerToUserSource maps the AuthResult.Provider string to the project
// store's UserSource enum. Unknown providers fall back to local so we still
// get a User row instead of silently dropping the sync.
func providerToUserSource(provider string) project.UserSource {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "ldap":
		return project.UserSourceLDAP
	case "oidc":
		return project.UserSourceOIDC
	case "localhost":
		return project.UserSourceLocalhost
	default:
		return project.UserSourceLocal
	}
}

// syncUserFromAuth upserts a project.User row for the just-authenticated user
// and ensures their personal project exists. Errors are logged but never
// surfaced — login should not fail because of metadata-store hiccups.
func (a *AuthManager) syncUserFromAuth(ctx context.Context, result *AuthResult) {
	a.mu.RLock()
	store := a.projectStore
	a.mu.RUnlock()
	if store == nil || result == nil {
		return
	}
	src := providerToUserSource(result.Provider)
	externalID := ""
	if result.Extra != nil {
		externalID = result.Extra["sub"]
		if externalID == "" {
			externalID = result.Extra["id"]
		}
	}
	u, err := store.EnsureUserFromAuth(ctx, src, result.Username, externalID, result.DisplayName, result.Email)
	if err != nil {
		a.logger.Warn("project store: ensure user failed", "username", result.Username, "provider", result.Provider, "error", err)
		return
	}
	if _, err := store.EnsurePersonalProject(ctx, u.ID); err != nil {
		a.logger.Warn("project store: ensure personal project failed", "username", result.Username, "error", err)
	}
}

// ensureLocalhostScope provisions the localhost-mode user(s) so the scope
// middleware can resolve a project for loopback requests. Two rows may be
// created:
//
//   - `local-<uid>` (UserSourceLocalhost): the machine-identity user, used
//     when no admin password is configured.
//   - `aliasUsername` (UserSourceLocal): created when auth IS configured and
//     the localhost branch binds the configured admin username into the
//     request context. Without this row the scope middleware would fail to
//     look up "admin" against the project store.
//
// Both branches ensure a personal project for the user they touched. Errors
// are logged and never returned — login should never fail because of
// metadata-store hiccups.
func (a *AuthManager) ensureLocalhostScope(ctx context.Context, aliasUsername string) {
	a.mu.RLock()
	store := a.projectStore
	a.mu.RUnlock()
	if store == nil {
		return
	}
	if aliasUsername != "" {
		u, err := store.EnsureUserFromAuth(ctx, project.UserSourceLocal, aliasUsername, "", aliasUsername, "")
		if err != nil {
			a.logger.Warn("project store: ensure localhost alias user failed", "username", aliasUsername, "error", err)
		} else {
			if u.GlobalRole != "admin" {
				_ = store.DB().WithContext(ctx).Model(u).Update("global_role", "admin").Error
			}
			if _, err := store.EnsurePersonalProject(ctx, u.ID); err != nil {
				a.logger.Warn("project store: ensure localhost alias personal project failed", "username", aliasUsername, "error", err)
			}
		}
		return
	}
	osUID := strconv.Itoa(os.Getuid())
	u, err := store.EnsureLocalhostUser(ctx, osUID)
	if err != nil {
		a.logger.Warn("project store: ensure localhost user failed", "uid", osUID, "error", err)
		return
	}
	if _, err := store.EnsurePersonalProject(ctx, u.ID); err != nil {
		a.logger.Warn("project store: ensure localhost personal project failed", "uid", osUID, "error", err)
	}
}

// NewAuthManager creates an AuthManager. If cfg is nil, all requests are allowed.
// External auth providers (LDAP, OIDC) are initialized based on the config.
func NewAuthManager(cfg *config.WebAuthConfig, logger *slog.Logger) *AuthManager {
	if logger == nil {
		logger = slog.Default()
	}
	am := &AuthManager{
		cfg:              cfg,
		logger:           logger,
		revoked:          make(map[string]time.Time),
		stopCleanup:      make(chan struct{}),
		stopCacheCleanup: make(chan struct{}),
		sessionSigningKey: generateSigningKey(),
	}

	// Start background cleanup of expired revoked tokens.
	go am.cleanupRevokedLoop()

	// Start background cleanup of stale userInfo cache entries.
	go am.cleanupUserInfoCacheLoop()

	if cfg != nil {
		// Local provider is always first in the chain.
		am.providers = append(am.providers, NewLocalProvider(am))

		// LDAP provider.
		if cfg.LDAP != nil && cfg.LDAP.Enabled {
			am.providers = append(am.providers, NewLDAPProvider(cfg.LDAP, logger))
		}

		// OIDC provider (redirect flow — not part of password chain).
		if cfg.OIDC != nil && cfg.OIDC.Enabled {
			am.oidcProvider = NewOIDCProvider(cfg.OIDC, logger)
		}
	}

	return am
}

// HandleLogin validates credentials and sets a session cookie.
//
// @Summary Login
// @Description Validates username/password credentials against local and LDAP providers. On success, sets a session cookie and returns user info. On failure, returns 401.
// @Tags auth
// @Accept json
// @Produce json
// @Param body body object true "{username: string, password: string}"
// @Success 200 {object} map[string]any "ok, username, role, displayName, email, avatarUrl"
// @Failure 400 {string} string "invalid request body"
// @Failure 401 {object} map[string]string "invalid credentials"
// @Failure 405 {string} string "method not allowed"
// @Router /api/auth/login [post]
func (a *AuthManager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 4KB to prevent memory exhaustion attacks.
	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	result := a.authenticateWithProviders(r.Context(), req.Username, req.Password)
	if result == nil {
		a.logger.Warn("login failed", "username", req.Username, "remote_addr", r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
		return
	}

	// Apply role mapping for external providers.
	if result.Provider != "local" && a.cfg.RoleMapping != nil {
		result.Role = resolveRole(result, a.cfg.RoleMapping)
	}

	// Cache user info for external providers.
	if result.Provider != "local" {
		a.cacheUserInfo(result)
	}

	// Sync into multi-tenant store (creates user row + personal project on
	// first login). No-op when projectStore is unset.
	a.syncUserFromAuth(r.Context(), result)

	token := a.createToken(result.Username, result.Role)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   shouldSecureCookie(r),
	})

	a.logger.Info("login success", "username", result.Username, "role", result.Role, "provider", result.Provider, "remote_addr", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{"ok": true, "username": result.Username, "role": result.Role}
	if result.DisplayName != "" {
		resp["displayName"] = result.DisplayName
	}
	if result.Email != "" {
		resp["email"] = result.Email
	}
	if result.AvatarURL != "" {
		resp["avatarUrl"] = result.AvatarURL
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleStatus returns whether auth is required and if the current request is authenticated.
//
// @Summary Auth status
// @Description Returns whether authentication is required for the server and whether the current request is authenticated. Localhost requests are always considered authenticated.
// @Tags auth
// @Produce json
// @Success 200 {object} map[string]bool "required and authenticated flags"
// @Router /api/auth/status [get]
func (a *AuthManager) HandleStatus(w http.ResponseWriter, r *http.Request) {
	required := a.cfg != nil && a.cfg.Password != ""
	authenticated := false

	if isLocalhost(r) {
		authenticated = true
	} else if cookie, err := r.Cookie(sessionCookieName); err == nil {
		authenticated = a.validToken(cookie.Value)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{
		"required":      required,
		"authenticated": authenticated,
	})
}

// HandleLogout clears the session cookie and revokes the token.
//
// @Summary Logout
// @Description Clears the session cookie and revokes the session token.
// @Tags auth
// @Produce json
// @Success 200 {object} map[string]bool "ok: true"
// @Router /api/auth/logout [post]
func (a *AuthManager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		a.revokeToken(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// authenticateWithProviders tries each password-based provider in order.
// Returns the first successful AuthResult, or nil if all fail.
func (a *AuthManager) authenticateWithProviders(ctx context.Context, username, password string) *AuthResult {
	for _, p := range a.providers {
		if p.Type() != "password" {
			continue
		}
		result, err := p.Authenticate(ctx, username, password)
		if err == nil && result != nil {
			return result
		}
	}
	return nil
}

// HandleOIDCCallback processes the OAuth2/OIDC callback after the user authenticates
// with the external identity provider. It exchanges the code for tokens, creates
// a session, and redirects to the frontend.
//
// @Summary OIDC callback
// @Description Processes the OAuth2/OIDC callback after user authenticates with the external identity provider. Exchanges the authorization code for tokens, creates a session cookie, and redirects to the frontend root.
// @Tags auth
// @Produce json
// @Param code query string true "OAuth2 authorization code"
// @Param state query string true "CSRF state token"
// @Success 302 {string} string "Redirect to /"
// @Failure 400 {string} string "missing code or state"
// @Failure 401 {string} string "authentication failed"
// @Failure 404 {string} string "OIDC not configured"
// @Router /api/auth/oidc/callback [get]
func (a *AuthManager) HandleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if a.oidcProvider == nil {
		http.Error(w, "OIDC not configured", http.StatusNotFound)
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}

	result, err := a.oidcProvider.HandleCallback(r.Context(), code, state)
	if err != nil {
		a.logger.Error("oidc callback failed", "error", err)
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	// Apply role mapping.
	if a.cfg.RoleMapping != nil {
		result.Role = resolveRole(result, a.cfg.RoleMapping)
	}

	// Cache user info.
	a.cacheUserInfo(result)

	// Sync into multi-tenant store (creates user row + personal project on
	// first OIDC login).
	a.syncUserFromAuth(r.Context(), result)

	token := a.createToken(result.Username, result.Role)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   shouldSecureCookie(r),
	})

	a.logger.Info("oidc login success", "username", result.Username, "role", result.Role)

	// Redirect to frontend root.
	http.Redirect(w, r, "/", http.StatusFound)
}

// HandleOIDCLogin initiates the OIDC redirect flow.
//
// @Summary OIDC login
// @Description Initiates the OIDC redirect flow by redirecting the browser to the external identity provider's authorization URL.
// @Tags auth
// @Success 302 {string} string "Redirect to OIDC provider"
// @Failure 404 {string} string "OIDC not configured"
// @Failure 503 {string} string "OIDC login unavailable (discovery failed)"
// @Router /api/auth/oidc/login [get]
func (a *AuthManager) HandleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if a.oidcProvider == nil {
		http.Error(w, "OIDC not configured", http.StatusNotFound)
		return
	}
	a.oidcProvider.HandleOIDCLogin(w, r)
}

// HandleProviders returns the list of enabled auth providers for the frontend.
//
// @Summary Auth providers
// @Description Returns the list of enabled authentication providers (local, LDAP, OIDC) for the frontend login page.
// @Tags auth
// @Produce json
// @Success 200 {object} map[string]any "providers list"
// @Router /api/auth/providers [get]
func (a *AuthManager) HandleProviders(w http.ResponseWriter, r *http.Request) {
	type providerInfo struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}

	var providers []providerInfo

	// Local/LDAP are password-based — the frontend shows a single login form.
	providers = append(providers, providerInfo{Name: "local", Type: "password"})

	if a.cfg != nil && a.cfg.LDAP != nil && a.cfg.LDAP.Enabled {
		providers = append(providers, providerInfo{Name: "ldap", Type: "password"})
	}
	if a.cfg != nil && a.cfg.OIDC != nil && a.cfg.OIDC.Enabled {
		providers = append(providers, providerInfo{Name: "oidc", Type: "redirect"})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"providers": providers})
}

// userInfoCacheEntry wraps an AuthResult with a timestamp for TTL-based eviction.
type userInfoCacheEntry struct {
	result   *AuthResult
	cachedAt time.Time
}

const userInfoCacheTTL = 1 * time.Hour

// GetUserInfo returns cached user info for an authenticated user.
// Returns nil if the user has no cached info (e.g., local users) or the entry expired.
func (a *AuthManager) GetUserInfo(username string) *AuthResult {
	if v, ok := a.userInfoCache.Load(username); ok {
		entry := v.(*userInfoCacheEntry)
		if time.Since(entry.cachedAt) < userInfoCacheTTL {
			return entry.result
		}
		a.userInfoCache.Delete(username) // expired
	}
	return nil
}

func (a *AuthManager) cacheUserInfo(result *AuthResult) {
	a.userInfoCache.Store(result.Username, &userInfoCacheEntry{
		result:   result,
		cachedAt: time.Now(),
	})
}

func (a *AuthManager) cleanupUserInfoCacheLoop() {
	ticker := time.NewTicker(userInfoCacheTTL)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopCacheCleanup:
			return
		case <-ticker.C:
			a.userInfoCache.Range(func(key, value any) bool {
				entry := value.(*userInfoCacheEntry)
				if time.Since(entry.cachedAt) >= userInfoCacheTTL {
					a.userInfoCache.Delete(key)
				}
				return true
			})
		}
	}
}

// UpdateConfig replaces the auth configuration at runtime.
// Pass nil to disable authentication.
func (a *AuthManager) UpdateConfig(cfg *config.WebAuthConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg = cfg
	// Clear revocation list — old tokens are invalid anyway since the
	// signing key (derived from password hash) has changed.
	a.revoked = make(map[string]time.Time)
}

func (a *AuthManager) Close() {
	if a.stopCleanup != nil {
		close(a.stopCleanup)
	}
	if a.stopCacheCleanup != nil {
		close(a.stopCacheCleanup)
	}
	if a.oidcProvider != nil {
		a.oidcProvider.Close()
	}
}