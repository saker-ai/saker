package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RequestIDMiddleware generates a unique request ID for every request,
// sets it in the Gin context and adds the X-Request-ID response header.
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := generateRequestID()
		c.Set("requestID", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

// SecurityHeadersMiddleware adds standard security headers to every response.
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		// CSP allows Saker's Next.js frontend (self, inline scripts/styles for hydration,
		// data/blob URIs for media, ws/wss for WebSocket, and /api/files for local assets).
		c.Header("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline' 'unsafe-eval'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: blob: http: https:; "+
				"media-src 'self' data: blob: http: https:; "+
				"connect-src 'self' ws: wss: http: https:; "+
				"frame-ancestors 'none'")
		c.Next()
	}
}

// CORSMiddleware adds CORS headers for allowed origins.
// If allowedOrigins is empty, only localhost origins are permitted.
//
// Wildcard "*" is rejected from the allow-list because the responses set
// Access-Control-Allow-Credentials: true, and the combination would let any
// origin perform credentialed cross-origin requests. Operators must list
// explicit origins.
func CORSMiddleware(allowedOrigins []string) gin.HandlerFunc {
	allowedSet := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o == "*" {
			// Skip wildcard with credentials — silently drop.
			continue
		}
		allowedSet[o] = true
	}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}

		allowed := false
		if len(allowedSet) == 0 {
			// Default: allow localhost origins
			allowed = isLocalhostOrigin(origin)
		} else {
			allowed = allowedSet[origin]
		}

		if !allowed {
			c.Next()
			return
		}

		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

// RateLimitMiddleware provides per-IP rate limiting using golang.org/x/time/rate.
// Requests exceeding the limit receive 429 Too Many Requests.
// Returns the middleware and a cleanup function that must be called on shutdown
// to stop the background cleanup goroutine.
func RateLimitMiddleware(rps float64, burst int) (gin.HandlerFunc, func()) {
	type visitor struct {
		limiter  *rate.Limiter
		lastSeen time.Time
	}

	var (
		mu       sync.Mutex
		visitors = make(map[string]*visitor)
		stopCh   = make(chan struct{})
	)

	// Periodic cleanup of stale visitors — stops when stopCh is closed.
	go func() {
		ticker := time.NewTicker(3 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				mu.Lock()
				for ip, v := range visitors {
					if time.Since(v.lastSeen) > 10*time.Minute {
						delete(visitors, ip)
					}
				}
				mu.Unlock()
			}
		}
	}()

	cleanup := func() { close(stopCh) }

	middleware := func(c *gin.Context) {
		ip := c.ClientIP()
		mu.Lock()
		v, exists := visitors[ip]
		if !exists {
			v = &visitor{limiter: rate.NewLimiter(rate.Limit(rps), burst)}
			visitors[ip] = v
		}
		v.lastSeen = time.Now()
		mu.Unlock()

		if !v.limiter.Allow() {
			c.AbortWithStatusJSON(429, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}

	return middleware, cleanup
}

// BearerRateLimitMiddleware rate-limits ONLY requests carrying a Bearer
// API key. Cookie-authenticated and localhost-admin requests pass straight
// through (they already have human-paced UI throttling and benefit nothing
// from being throttled). Per-IP keying: 30 req/s burst 60 is generous for
// legitimate machine clients but blocks unattended brute-force scanning.
//
// Returns the middleware and a cleanup function — the cleanup must be
// called on shutdown to stop the background visitor-eviction goroutine.
//
// Why split from RateLimitMiddleware: the auth-endpoint limiter has very
// different RPS targets (5/s for login attempts vs ~30/s for app runs)
// and must keep its own visitor map so the two limit windows don't bleed.
func BearerRateLimitMiddleware(rps float64, burst int) (gin.HandlerFunc, func()) {
	type visitor struct {
		limiter  *rate.Limiter
		lastSeen time.Time
	}

	var (
		mu       sync.Mutex
		visitors = make(map[string]*visitor)
		stopCh   = make(chan struct{})
	)

	go func() {
		ticker := time.NewTicker(3 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				mu.Lock()
				for ip, v := range visitors {
					if time.Since(v.lastSeen) > 10*time.Minute {
						delete(visitors, ip)
					}
				}
				mu.Unlock()
			}
		}
	}()

	cleanup := func() { close(stopCh) }

	middleware := func(c *gin.Context) {
		if !hasBearerAPIKey(c.Request) {
			c.Next()
			return
		}
		ip := c.ClientIP()
		mu.Lock()
		v, exists := visitors[ip]
		if !exists {
			v = &visitor{limiter: rate.NewLimiter(rate.Limit(rps), burst)}
			visitors[ip] = v
		}
		v.lastSeen = time.Now()
		mu.Unlock()

		if !v.limiter.Allow() {
			c.AbortWithStatusJSON(429, gin.H{"error": "rate limit exceeded for bearer-keyed request"})
			return
		}
		c.Next()
	}

	return middleware, cleanup
}

// BodySizeLimitMiddleware limits request body size.
func BodySizeLimitMiddleware(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

func generateRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func isLocalhostOrigin(origin string) bool {
	return strings.HasPrefix(origin, "http://localhost:") ||
		strings.HasPrefix(origin, "http://127.0.0.1:") ||
		strings.HasPrefix(origin, "https://localhost:") ||
		strings.HasPrefix(origin, "https://127.0.0.1:") ||
		strings.HasPrefix(origin, "http://[::1]:") ||
		strings.HasPrefix(origin, "https://[::1]:")
}

// isAllowedWSOrigin checks whether a WebSocket upgrade request origin
// is permitted. It uses the same logic as the HTTP CORS middleware:
// if explicit allowed origins are configured, match against that list;
// otherwise, only localhost origins are permitted.
//
// Wildcard "*" is intentionally NOT honored here: WebSocket upgrades carry
// the session cookie (credentialed), and combining "*" with credentials
// allows any origin to read user data. Operators must list explicit origins.
func isAllowedWSOrigin(origin string, allowedOrigins []string) bool {
	if len(allowedOrigins) == 0 {
		return isLocalhostOrigin(origin)
	}
	for _, o := range allowedOrigins {
		if o == origin {
			return true
		}
	}
	return false
}