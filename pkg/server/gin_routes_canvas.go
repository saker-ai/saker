package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// registerCanvasRoutes wires the canvas REST API onto a gin router group
// using native parameterised routes. Per-handler bodies live in canvas_rest.go
// as gin.HandlerFunc.
//
// Routes registered (multi-tenant, when s.handler.projects != nil):
//
//	POST /api/canvas/:projectId/:threadId/execute
//	GET  /api/canvas/:projectId/:threadId/document
//	GET  /api/canvas/:projectId/runs/:runId
//	POST /api/canvas/:projectId/runs/:runId/cancel
//
// Single-project mode (s.handler.projects == nil) registers the same routes
// without the :projectId segment.
func (s *Server) registerCanvasRoutes(authed *gin.RouterGroup) {
	if s.handler.projects != nil {
		grp := authed.Group("/api/canvas/:projectId", s.projectScopeMiddleware())
		grp.POST("/:threadId/execute", s.handleCanvasExecuteREST)
		grp.GET("/:threadId/document", s.handleCanvasDocumentREST)
		grp.GET("/runs/:runId", s.handleCanvasRunStatusREST)
		grp.POST("/runs/:runId/cancel", s.handleCanvasRunCancelREST)
		return
	}
	grp := authed.Group("/api/canvas")
	grp.POST("/:threadId/execute", s.handleCanvasExecuteREST)
	grp.GET("/:threadId/document", s.handleCanvasDocumentREST)
	grp.GET("/runs/:runId", s.handleCanvasRunStatusREST)
	grp.POST("/runs/:runId/cancel", s.handleCanvasRunCancelREST)
}

// projectScopeMiddleware extracts :projectId from the route, resolves the
// project scope (via Bearer-key bypass when applicable, otherwise the
// cookie-user membership check), and injects the resulting context into the
// request. Used by the multi-tenant route groups in registerCanvasRoutes,
// registerAppsRoutes, and any future per-project gin group.
//
// In single-project mode (s.handler.projects == nil) this middleware is a
// no-op and should not be installed; callers must check first.
func (s *Server) projectScopeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.handler.projects == nil {
			c.Next()
			return
		}
		projectID := c.Param("projectId")
		if projectID == "" {
			http.Error(c.Writer, "missing projectId", http.StatusBadRequest)
			c.Abort()
			return
		}
		// Anonymous Bearer-API-key calls bypass the cookie membership check
		// — the per-handler validates the actual key against the app under
		// this project.
		anonBearer := UserFromContext(c.Request.Context()) == "" && hasBearerAPIKey(c.Request)
		var ctx context.Context
		if anonBearer {
			ctx = s.handler.bearerProjectScope(c.Request.Context(), projectID)
		} else {
			resolved, err := s.handler.resolveRESTScope(c.Request.Context(), UserFromContext(c.Request.Context()), projectID)
			if err != nil {
				status := http.StatusForbidden
				switch {
				case errors.Is(err, errRESTAuthRequired):
					status = http.StatusUnauthorized
				case errors.Is(err, errRESTProjectMissing):
					status = http.StatusBadRequest
				}
				http.Error(c.Writer, err.Error(), status)
				c.Abort()
				return
			}
			ctx = resolved
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
