package agui

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/saker-ai/saker/pkg/project"
)

type identityKey struct{}

// Identity is the authenticated principal behind an AG-UI request.
type Identity struct {
	UserID    string
	Username  string
	ProjectID string
	APIKeyID  string
	Bypass    bool
}

func identityFromContext(ctx context.Context) Identity {
	id, _ := ctx.Value(identityKey{}).(Identity)
	return id
}

func withIdentity(c *gin.Context, id Identity) {
	ctx := context.WithValue(c.Request.Context(), identityKey{}, id)
	c.Request = c.Request.WithContext(ctx)
}

func (g *Gateway) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractBearer(c.GetHeader("Authorization"))

		if token != "" && g.deps.ProjectStore != nil {
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

		if g.deps.SessionValidator != nil {
			if username, _, ok := g.deps.SessionValidator(c); ok {
				withIdentity(c, Identity{Username: username})
				c.Next()
				return
			}
		}

		if g.deps.Options.DevBypassAuth {
			withIdentity(c, Identity{Username: "localhost", Bypass: true})
			c.Next()
			return
		}

		c.JSON(401, gin.H{"error": gin.H{
			"message": "missing or invalid Bearer key",
			"type":    "authentication_error",
		}})
		c.Abort()
	}
}

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

var _ = project.HashAPIKey
