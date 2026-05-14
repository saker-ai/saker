package openai

import (
	"context"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/project"
	"github.com/gin-gonic/gin"
)

// authIdentityKey is the gin context key under which the resolved
// identity is stored. Read with IdentityFromContext.
type authIdentityKey struct{}

// Identity is the authenticated principal behind an /v1/* request. The
// gateway populates it from the Bearer key (or the dev-bypass localhost
// fallback) and downstream handlers consume it to scope sessions and
// hub fan-out.
type Identity struct {
	// UserID is the project.User.ID. Empty for the localhost dev bypass.
	UserID string
	// Username is a human-readable label used in logs.
	Username string
	// ProjectID is the scope the key was issued under. Empty for
	// admin-style keys that have access to every project on the user.
	ProjectID string
	// APIKeyID is the database row id for telemetry / auditing.
	APIKeyID string
	// Bypass is true when the request was admitted via DevBypassAuth.
	Bypass bool
}

// IdentityFromContext returns the resolved identity, or a zero-value
// Identity if none was set (which should never happen on /v1/* routes
// after authMiddleware ran).
func IdentityFromContext(ctx context.Context) Identity {
	id, _ := ctx.Value(authIdentityKey{}).(Identity)
	return id
}

// withIdentity attaches an identity to the gin context (stored on both
// the gin Keys map AND the request context so downstream handlers can
// read it via either path).
func withIdentity(c *gin.Context, id Identity) {
	c.Set("openai.identity", id)
	ctx := context.WithValue(c.Request.Context(), authIdentityKey{}, id)
	c.Request = c.Request.WithContext(ctx)
}

// authMiddleware enforces Bearer key auth on /v1/* routes. Empty Bearer
// or unknown key → 401. Dev bypass (Options.DevBypassAuth) admits
// missing-or-unknown keys with the localhost identity attached.
func (g *Gateway) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractBearer(c.GetHeader("Authorization"))

		// Path 1: have a token. Look it up unless the project store is nil
		// (legacy embedded mode without GORM). In legacy mode, any token
		// is accepted as an "anonymous" identity — saker server isn't
		// configurable that way through cmd_server, but tests can.
		if token != "" {
			if g.deps.ProjectStore == nil {
				if g.deps.Options.DevBypassAuth {
					withIdentity(c, Identity{Username: "anonymous", APIKeyID: "", Bypass: true})
					c.Next()
					return
				}
				Unauthorized(c, "authentication service unavailable")
				return
			}
			row, err := g.deps.ProjectStore.LookupAPIKey(c.Request.Context(), token)
			if err == nil && row != nil {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					g.deps.ProjectStore.TouchAPIKey(ctx, row.ID)
				}()
				withIdentity(c, Identity{
					UserID:    row.UserID,
					Username:  row.Name,
					ProjectID: row.ProjectID,
					APIKeyID:  row.ID,
				})
				c.Next()
				return
			}
		}

		// Path 2: dev bypass. Only honored when the operator explicitly
		// flipped DevBypassAuth (mirrors OPENAI_GW_DEV_BYPASS=true).
		if g.deps.Options.DevBypassAuth {
			withIdentity(c, Identity{
				Username: "localhost",
				Bypass:   true,
			})
			c.Next()
			return
		}

		Unauthorized(c, "missing or invalid Bearer key. See https://platform.openai.com/api-keys for the equivalent saker setup.")
	}
}

// extractBearer pulls the token out of an Authorization header value.
// Returns "" if the header is missing or doesn't start with "Bearer ".
// Casing of the scheme is tolerated to match every SDK we've seen.
func extractBearer(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}
	const prefix = "bearer "
	if len(h) <= len(prefix) {
		return ""
	}
	if !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// _ ensures we don't import project lazily.
var _ = project.HashAPIKey
