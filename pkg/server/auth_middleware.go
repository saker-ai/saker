package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Middleware wraps an http.Handler with authentication checks.
// Localhost requests and unauthenticated endpoints are always allowed.
// Authenticated requests carry username and role in the request context.
func (a *AuthManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always allow health, auth endpoints, static frontend assets,
		// public share-token routes, and bearer-keyed app runs so the login
		// page renders for unauthenticated remote users and API callers work.
		if isPublicPath(r) {
			next.ServeHTTP(w, r)
			return
		}

		// No auth configured → allow all (admin by default). For multi-tenant
		// mode we still want a stable identity so the scope middleware has
		// something to resolve; bind the localhost user when the request is
		// from loopback.
		if a.cfg == nil || a.cfg.Password == "" {
			if isLocalhost(r) && a.projectStore != nil {
				a.ensureLocalhostScope(r.Context(), "")
				localUser := "local-" + strconv.Itoa(os.Getuid())
				ctx := context.WithValue(r.Context(), userContextKey, localUser)
				ctx = context.WithValue(ctx, roleContextKey, "admin")
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// Localhost → allow as admin.
		if isLocalhost(r) {
			adminUser := a.cfg.Username
			if adminUser == "" {
				adminUser = "admin"
			}
			// Lazy-create the localhost project.User + personal project so the
			// scope middleware downstream finds a row. Best-effort: failures
			// are logged inside the helper and never block the request.
			a.ensureLocalhostScope(r.Context(), adminUser)
			ctx := context.WithValue(r.Context(), userContextKey, adminUser)
			ctx = context.WithValue(ctx, roleContextKey, "admin")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Check session cookie.
		cookie, err := r.Cookie(sessionCookieName)
		if err == nil && a.validToken(cookie.Value) {
			// Extract username and role from token payload.
			username, role := a.extractTokenInfo(cookie.Value)
			ctx := context.WithValue(r.Context(), userContextKey, username)
			ctx = context.WithValue(ctx, roleContextKey, role)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Unauthorized.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "authentication required"})
	})
}

// shouldSecureCookie returns true when the request originates from a non-localhost
// address, meaning the cookie should be marked Secure (only sent over HTTPS).
func shouldSecureCookie(r *http.Request) bool {
	return !isLocalhost(r)
}

// isPublicPath returns true for paths that must be accessible without
// authentication. The request is accepted so that the login page renders for
// remote users, S3-SDK requests carry their own SigV4 auth, bearer-keyed app
// runs bypass the cookie check, and public share-token routes work
// anonymously.
func isPublicPath(r *http.Request) bool {
	path := r.URL.Path
	switch path {
	case "/health", "/api/auth/login", "/api/auth/status", "/api/auth/logout",
		"/api/auth/providers", "/api/auth/oidc/login", "/api/auth/oidc/callback":
		return true
	}
	// Embedded S3 API: SigV4 in s2 handles its own auth, so cookie-based
	// auth must not run first (would reject every S3 SDK request that has
	// no saker session cookie). Mount path mirrors storage.DefaultS3MountPath.
	if strings.HasPrefix(path, "/_s3/") || path == "/_s3" {
		return true
	}
	// Static frontend assets required to render the login page.
	if strings.HasPrefix(path, "/_next/") ||
		strings.HasSuffix(path, ".js") ||
		strings.HasSuffix(path, ".css") ||
		strings.HasSuffix(path, ".ico") ||
		strings.HasSuffix(path, ".svg") ||
		strings.HasSuffix(path, ".png") ||
		strings.HasSuffix(path, ".woff") ||
		strings.HasSuffix(path, ".woff2") {
		return true
	}
	// The root page (index.html / SPA entry).
	if path == "/" || path == "/index.html" {
		return true
	}
	// Public share-token endpoints — no authentication required.
	// Single-project: /api/apps/public/{token}/...
	if strings.HasPrefix(path, "/api/apps/public/") {
		return true
	}
	// Multi-tenant: /api/apps/{projectId}/public/{token}/...
	// Detect "public" as the second path segment after "/api/apps/".
	after := strings.TrimPrefix(path, "/api/apps/")
	if after != path {
		parts := strings.SplitN(after, "/", 3)
		if len(parts) >= 2 && parts[1] == "public" {
			return true
		}
	}
	// Bearer API-key auth: allow /run and /runs/... endpoints so the cookie
	// middleware doesn't reject them before the handler validates the key.
	if strings.HasPrefix(path, "/api/apps/") &&
		(strings.HasSuffix(path, "/run") || strings.Contains(path, "/runs/")) {
		if hasBearerAPIKey(r) {
			return true
		}
	}
	return false
}

// hasBearerAPIKey reports whether the request carries an Authorization header
// matching the API-key format: "Bearer ak_" followed by exactly 32 hex chars.
func hasBearerAPIKey(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if len(auth) != 42 { // "Bearer ak_" (10) + 32 hex = 42
		return false
	}
	if !strings.EqualFold(auth[:7], "bearer ") {
		return false
	}
	rest := auth[7:]
	if !strings.HasPrefix(rest, "ak_") {
		return false
	}
	for _, c := range rest[3:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// isLocalhost checks if the request originates from a loopback address.
func isLocalhost(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}