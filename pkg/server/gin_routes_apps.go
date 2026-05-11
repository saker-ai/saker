package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// registerAppsRoutes wires the apps REST API onto a gin router group using
// native parameterised routes instead of the legacy path-split dispatcher
// (handleAppsREST). Per-handler bodies in apps_rest_*.go are reused
// unchanged via thin gin → net/http adapters.
//
// Routes registered (multi-tenant, when s.handler.projects != nil — every
// route below is prefixed with /api/apps/:projectId; project scope is
// resolved by projectScopeMiddleware):
//
//	GET    /api/apps/:projectId
//	POST   /api/apps/:projectId
//	GET    /api/apps/:projectId/:appId
//	PUT    /api/apps/:projectId/:appId
//	DELETE /api/apps/:projectId/:appId
//	POST   /api/apps/:projectId/:appId/publish
//	GET    /api/apps/:projectId/:appId/versions
//	PUT    /api/apps/:projectId/:appId/published-version
//	POST   /api/apps/:projectId/:appId/run
//	GET    /api/apps/:projectId/:appId/runs/:runId
//	POST   /api/apps/:projectId/:appId/runs/:runId/cancel
//	GET    /api/apps/:projectId/:appId/keys
//	POST   /api/apps/:projectId/:appId/keys
//	DELETE /api/apps/:projectId/:appId/keys/:keyId
//	POST   /api/apps/:projectId/:appId/keys/:keyId/rotate
//	GET    /api/apps/:projectId/:appId/share
//	POST   /api/apps/:projectId/:appId/share
//	DELETE /api/apps/:projectId/:appId/share/:token
//
// Anonymous public/share paths (no project scope middleware):
//
//	ANY /api/apps/public/*rest
//	ANY /api/apps/:projectId/public/*rest
//
// Single-project mode (s.handler.projects == nil) registers the same
// per-app routes without the :projectId segment.
//
// The bearerLimiter middleware (Bearer-API-key per-IP throttle) is applied
// to the entire apps group so leaked keys can't pump traffic at line rate.
func (s *Server) registerAppsRoutes(authed *gin.RouterGroup, bearerLimiter gin.HandlerFunc) {
	// The /api/apps prefix gets the Bearer rate-limiter applied first so
	// every nested group inherits it — including the anonymous public paths
	// which are the most likely to be abused.
	root := authed.Group("/api/apps", bearerLimiter)

	if s.handler.projects != nil {
		// Multi-tenant: anonymous share-token endpoints under /api/apps/public/...
		// are mounted on the root and do NOT go through the project-scope
		// middleware (the share token resolves the app and scope itself).
		root.Any("/public/*rest", s.ginAppsPublic())

		// Per-project group with scope middleware.
		grp := root.Group("/:projectId", s.projectScopeMiddleware())
		// /api/apps/:projectId/public/*rest is the multi-tenant share path
		// — must NOT inherit the projectScopeMiddleware's auth check, but
		// the URL still carries the projectId so the bearerProjectScope
		// path runs. We mount it on the per-project group precisely so the
		// :projectId param is exposed; the middleware itself bypasses
		// scope resolution on Bearer/anonymous calls (anonShare path).
		// Actually: the legacy dispatcher synthesises a bearerProjectScope
		// for these calls regardless of cookie auth — we mount on the
		// projectScopeMiddleware-protected group but the middleware's
		// anonBearer branch routes it through bearerProjectScope. To
		// preserve the share-public flow we register the public route on
		// a sibling group WITHOUT the middleware and synthesise the
		// project scope ourselves.
		s.registerAppsItemRoutes(grp)

		// Multi-tenant share path with explicit projectId — bypass cookie
		// auth, synthesise bearerProjectScope.
		root.Any("/:projectId/public/*rest", s.ginAppsPublicWithProject())
		return
	}

	// Single-project mode.
	root.Any("/public/*rest", s.ginAppsPublic())
	s.registerAppsItemRoutes(root)
}

// registerAppsItemRoutes registers all the app-scoped routes on `grp`.
// Used for both single-project and multi-tenant (the latter passes a group
// already nested under /:projectId with the scope middleware).
//
// The collection-level routes (GET/POST /api/apps[/:projectId]) live here
// too because they share the same scope.
func (s *Server) registerAppsItemRoutes(grp *gin.RouterGroup) {
	// Collection-level CRUD.
	grp.GET("", s.ginAppsList())
	grp.POST("", s.ginAppsCreate())

	// /:appId bare meta CRUD.
	grp.GET("/:appId", s.ginAppsGet())
	grp.PUT("/:appId", s.ginAppsUpdate())
	grp.DELETE("/:appId", s.ginAppsDelete())

	// /:appId actions.
	grp.POST("/:appId/publish", s.ginAppsPublish())
	grp.GET("/:appId/versions", s.ginAppsVersions())
	grp.PUT("/:appId/published-version", s.ginAppsSetPublishedVersion())
	grp.POST("/:appId/run", s.ginAppsRun())
	grp.GET("/:appId/runs/:runId", s.ginAppsRunStatus())
	grp.POST("/:appId/runs/:runId/cancel", s.ginAppsRunCancel())

	// /:appId keys.
	grp.GET("/:appId/keys", s.ginAppsKeysCollection())
	grp.POST("/:appId/keys", s.ginAppsKeysCollection())
	grp.DELETE("/:appId/keys/:keyId", s.ginAppsKeysItem())
	grp.POST("/:appId/keys/:keyId/rotate", s.ginAppsKeysRotate())

	// /:appId share tokens.
	grp.GET("/:appId/share", s.ginAppsShareCollection())
	grp.POST("/:appId/share", s.ginAppsShareCollection())
	grp.DELETE("/:appId/share/:token", s.ginAppsShareItem())
}

// ── gin → net/http adapters ─────────────────────────────────────────────────

func (s *Server) ginAppsList() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsList(c.Writer, c.Request)
	}
}

func (s *Server) ginAppsCreate() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsCreate(c.Writer, c.Request)
	}
}

func (s *Server) ginAppsGet() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsGet(c.Writer, c.Request, c.Param("appId"))
	}
}

func (s *Server) ginAppsUpdate() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsUpdate(c.Writer, c.Request, c.Param("appId"))
	}
}

func (s *Server) ginAppsDelete() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsDelete(c.Writer, c.Request, c.Param("appId"))
	}
}

func (s *Server) ginAppsPublish() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsPublish(c.Writer, c.Request, c.Param("appId"))
	}
}

func (s *Server) ginAppsVersions() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsVersions(c.Writer, c.Request, c.Param("appId"))
	}
}

func (s *Server) ginAppsSetPublishedVersion() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsSetPublishedVersion(c.Writer, c.Request, c.Param("appId"))
	}
}

func (s *Server) ginAppsRun() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsRun(c.Writer, c.Request, c.Param("appId"))
	}
}

func (s *Server) ginAppsRunStatus() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsRunStatus(c.Writer, c.Request, c.Param("appId"), []string{c.Param("runId")})
	}
}

func (s *Server) ginAppsRunCancel() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsRunCancel(c.Writer, c.Request, c.Param("appId"), c.Param("runId"))
	}
}

func (s *Server) ginAppsKeysCollection() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsKeysCollection(c.Writer, c.Request, c.Param("appId"))
	}
}

func (s *Server) ginAppsKeysItem() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsKeysItem(c.Writer, c.Request, c.Param("appId"), c.Param("keyId"))
	}
}

func (s *Server) ginAppsKeysRotate() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsKeysRotate(c.Writer, c.Request, c.Param("appId"), c.Param("keyId"))
	}
}

func (s *Server) ginAppsShareCollection() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsShareCollection(c.Writer, c.Request, c.Param("appId"))
	}
}

func (s *Server) ginAppsShareItem() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.handleAppsShareItem(c.Writer, c.Request, c.Param("appId"), c.Param("token"))
	}
}

// ginAppsPublic adapts the anonymous share-token catch-all path:
//
//	GET  /public/{token}                 → schema
//	POST /public/{token}/run             → start run
//	GET  /public/{token}/runs/{runId}    → status
//	POST /public/{token}/runs/{runId}/cancel → cancel
//
// handleAppsPublic expects parts[0] == "public", parts[1] == token,
// parts[2] == subaction. We rebuild the legacy parts slice from the
// captured *rest wildcard so the handler logic stays untouched.
func (s *Server) ginAppsPublic() gin.HandlerFunc {
	return func(c *gin.Context) {
		rest := strings.Trim(c.Param("rest"), "/")
		parts := []string{"public"}
		if rest != "" {
			parts = append(parts, strings.Split(rest, "/")...)
		}
		s.handleAppsPublic(c.Writer, c.Request, parts)
	}
}

// ginAppsPublicWithProject is the multi-tenant public path:
//
//	/api/apps/:projectId/public/*rest
//
// In the legacy dispatcher this branch synthesised a bearerProjectScope
// before falling through to handleAppsPublic. We replicate that here so
// pathsFor downstream resolves to the correct per-project root.
func (s *Server) ginAppsPublicWithProject() gin.HandlerFunc {
	return func(c *gin.Context) {
		projectID := c.Param("projectId")
		if projectID == "" {
			http.Error(c.Writer, "missing projectId", http.StatusBadRequest)
			return
		}
		ctx := s.handler.bearerProjectScope(c.Request.Context(), projectID)
		c.Request = c.Request.WithContext(ctx)
		rest := strings.Trim(c.Param("rest"), "/")
		parts := []string{"public"}
		if rest != "" {
			parts = append(parts, strings.Split(rest, "/")...)
		}
		s.handleAppsPublic(c.Writer, c.Request, parts)
	}
}
