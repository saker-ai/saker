package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// registerAppsRoutes wires the apps REST API onto a gin router group using
// native parameterised routes. Per-handler bodies in apps_rest_*.go are
// gin.HandlerFunc.
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
	grp.GET("", s.handleAppsList)
	grp.POST("", s.handleAppsCreate)

	// /:appId bare meta CRUD.
	grp.GET("/:appId", s.handleAppsGet)
	grp.PUT("/:appId", s.handleAppsUpdate)
	grp.DELETE("/:appId", s.handleAppsDelete)

	// /:appId actions.
	grp.POST("/:appId/publish", s.handleAppsPublish)
	grp.GET("/:appId/versions", s.handleAppsVersions)
	grp.PUT("/:appId/published-version", s.handleAppsSetPublishedVersion)
	grp.POST("/:appId/run", s.handleAppsRun)
	grp.GET("/:appId/runs/:runId", s.handleAppsRunStatus)
	grp.POST("/:appId/runs/:runId/cancel", s.handleAppsRunCancel)

	// /:appId keys.
	grp.GET("/:appId/keys", s.handleAppsKeysCollection)
	grp.POST("/:appId/keys", s.handleAppsKeysCollection)
	grp.DELETE("/:appId/keys/:keyId", s.handleAppsKeysItem)
	grp.POST("/:appId/keys/:keyId/rotate", s.handleAppsKeysRotate)

	// /:appId share tokens.
	grp.GET("/:appId/share", s.handleAppsShareCollection)
	grp.POST("/:appId/share", s.handleAppsShareCollection)
	grp.DELETE("/:appId/share/:token", s.handleAppsShareItem)
}

// ── gin → handleAppsPublic adapters ────────────────────────────────────────
//
// handleAppsPublic remains a parts-based sub-dispatcher (parts[0] == "public",
// parts[1] == token, etc.) because it is invoked from two distinct gin routes
// (with and without a projectId prefix) and the legacy implementation already
// switches on parts.

// ginAppsPublic adapts the anonymous share-token catch-all path:
//
//	GET  /public/{token}                 → schema
//	POST /public/{token}/run             → start run
//	GET  /public/{token}/runs/{runId}    → status
//	POST /public/{token}/runs/{runId}/cancel → cancel
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
