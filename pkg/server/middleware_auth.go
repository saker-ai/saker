package server

import (
	"context"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
)

// PublicPathMiddleware checks whether the request path is public (health,
// auth endpoints, static assets, S3, share-tokens, bearer-keyed app runs).
// For public paths it sets an "auth_bypass" flag in the Gin context and
// continues. For non-public paths it continues to the next middleware in
// the chain (LocalhostIdentityMiddleware, CookieSessionMiddleware).
func (a *AuthManager) PublicPathMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isPublicPath(c.Request) {
			c.Set("auth_bypass", true)
			c.Next()
			return
		}
		c.Next()
	}
}

// LocalhostIdentityMiddleware injects identity for localhost requests.
//
//   - No auth config / empty password: localhost users get a local-UID
//     identity for multi-tenant scope resolution; remote users pass through
//     (no auth required).
//   - Auth configured + localhost: the admin identity is injected so the
//     loopback user is always authenticated.
//   - Auth configured + remote: passes through to CookieSessionMiddleware.
func (a *AuthManager) LocalhostIdentityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip if auth already bypassed (public path).
		if bypass, _ := c.Get("auth_bypass"); bypass == true {
			c.Next()
			return
		}

		// No auth configured -> allow all (admin by default).
		if a.cfg == nil || a.cfg.Password == "" {
			if isLocalhost(c.Request) && a.projectStore != nil {
				a.ensureLocalhostScope(c.Request.Context(), "")
				localUser := "local-" + strconv.Itoa(os.Getuid())
				c.Set("username", localUser)
				c.Set("role", "admin")
			}
			c.Set("auth_bypass", true)
			c.Next()
			return
		}

		// Auth configured + localhost -> inject admin identity.
		if isLocalhost(c.Request) {
			adminUser := a.cfg.Username
			if adminUser == "" {
				adminUser = "admin"
			}
			a.ensureLocalhostScope(c.Request.Context(), adminUser)
			c.Set("username", adminUser)
			c.Set("role", "admin")
			c.Set("auth_bypass", true)
			c.Next()
			return
		}

		// Remote request with auth configured -> continue to cookie check.
		c.Next()
	}
}

// CookieSessionMiddleware validates the session cookie for remote requests
// when auth is configured. On success it sets username and role in the Gin
// context. On failure it aborts with 401 JSON {"error": "authentication required"}.
// Skips when auth_bypass is set (public path, localhost, or no auth config).
func (a *AuthManager) CookieSessionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip if auth already bypassed (public path, localhost, or no auth config).
		if bypass, _ := c.Get("auth_bypass"); bypass == true {
			c.Next()
			return
		}

		// Validate session cookie.
		cookie, err := c.Request.Cookie(sessionCookieName)
		if err == nil && a.validToken(cookie.Value) {
			username, role := a.extractTokenInfo(cookie.Value)
			c.Set("username", username)
			c.Set("role", role)
			c.Next()
			return
		}

		// No valid session -> 401.
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
	}
}

// ContextBridgeMiddleware copies username and role from the Gin context into
// the underlying http.Request context (r.Context()). This ensures that wrapped
// http.Handler implementations (WebSocket, RPC-REST, canvas REST, etc.) can
// read identity via UserFromContext/RoleFromContext, which use context.Context
// values rather than Gin's c.Get/c.Set.
func (a *AuthManager) ContextBridgeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		username, uok := c.Get("username")
		role, rok := c.Get("role")

		ctx := c.Request.Context()
		if uok {
			if s, ok := username.(string); ok {
				ctx = context.WithValue(ctx, userContextKey, s)
			}
		}
		if rok {
			if s, ok := role.(string); ok {
				ctx = context.WithValue(ctx, roleContextKey, s)
			}
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// AuthMiddlewareChain returns the ordered chain of Gin middleware that
// implements the full auth flow: public-path bypass -> localhost identity ->
// cookie session validation -> context bridge.
func (a *AuthManager) AuthMiddlewareChain() []gin.HandlerFunc {
	return []gin.HandlerFunc{
		a.PublicPathMiddleware(),
		a.LocalhostIdentityMiddleware(),
		a.CookieSessionMiddleware(),
		a.ContextBridgeMiddleware(),
	}
}

// UserFromGinContext reads the authenticated username from the Gin context.
// Returns "" if no user is authenticated (e.g., public path without auth).
func UserFromGinContext(c *gin.Context) string {
	if v, ok := c.Get("username"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// RoleFromGinContext reads the user role from the Gin context.
// Returns "admin" if no role is set (backward compatible default).
func RoleFromGinContext(c *gin.Context) string {
	if v, ok := c.Get("role"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return "admin"
}