package server

import (
	"net/http"
	"net/http/pprof"
	"strings"

	storagecfg "github.com/cinience/saker/pkg/storage"
	"github.com/gin-gonic/gin"
)

// buildGinEngine creates the Gin engine with global middleware and route groups.
func (s *Server) buildGinEngine() *gin.Engine {
	if s.opts.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()

	// Global middleware (applied to all requests).
	engine.Use(RequestIDMiddleware())
	engine.Use(SecurityHeadersMiddleware())
	engine.Use(PrometheusMiddleware())

	// CORS — derive allowed origins from settings; defaults to localhost-only.
	var allowedOrigins []string
	if settings := s.runtime.Settings(); settings != nil && settings.CORS != nil {
		allowedOrigins = settings.CORS.AllowedOrigins
	}
	engine.Use(CORSMiddleware(allowedOrigins))

	// ----- Debug/pprof endpoints (no auth required) -----
	if s.opts.Debug {
		s.logger.Warn("DEBUG MODE ENABLED — pprof endpoints are accessible without authentication; do NOT use in production")

		pprofGroup := engine.Group("/debug/pprof")
		pprofGroup.GET("/", gin.WrapH(http.HandlerFunc(pprof.Index)))
		pprofGroup.GET("/cmdline", gin.WrapH(http.HandlerFunc(pprof.Cmdline)))
		pprofGroup.GET("/profile", gin.WrapH(http.HandlerFunc(pprof.Profile)))
		pprofGroup.GET("/symbol", gin.WrapH(http.HandlerFunc(pprof.Symbol)))
		pprofGroup.GET("/trace", gin.WrapH(http.HandlerFunc(pprof.Trace)))
		pprofGroup.POST("/symbol", gin.WrapH(http.HandlerFunc(pprof.Symbol)))
	}

	// ----- Public routes (no auth middleware) -----
	public := engine.Group("")
	public.GET("/health", gin.WrapH(http.HandlerFunc(s.handleHealth)))

	// Auth endpoints: rate-limited to 5 req/s per IP to prevent brute-force.
	authLimiter, authLimiterCleanup := RateLimitMiddleware(5, 10)
	s.rateLimiterCleanup = authLimiterCleanup
	public.POST("/api/auth/login", authLimiter, gin.WrapH(http.HandlerFunc(s.auth.HandleLogin)))
	public.GET("/api/auth/status", gin.WrapH(http.HandlerFunc(s.auth.HandleStatus)))
	public.POST("/api/auth/logout", gin.WrapH(http.HandlerFunc(s.auth.HandleLogout)))
	public.GET("/api/auth/providers", gin.WrapH(http.HandlerFunc(s.auth.HandleProviders)))
	public.GET("/api/auth/oidc/login", gin.WrapH(http.HandlerFunc(s.auth.HandleOIDCLogin)))
	public.GET("/api/auth/oidc/callback", gin.WrapH(http.HandlerFunc(s.auth.HandleOIDCCallback)))

	// S3 API — has its own auth (SigV4). The embedded handler is resolved
	// per-request so reload-driven backend swaps take effect immediately.
	public.Any("/_s3/*path", s.s3GinHandler())

	// Editor static files — served without auth.
	editorHandler := s.editorGinHandler()
	if editorHandler != nil {
		public.GET("/editor/*filepath", editorHandler)
		public.HEAD("/editor/*filepath", editorHandler)
	}

	// ----- Authenticated routes (auth middleware chain) -----
	authed := engine.Group("")
	authed.Use(s.auth.AuthMiddlewareChain()...)

	// Metrics endpoint: require auth when authentication is configured;
	// allow unauthenticated access only in single-user / localhost dev.
	if s.auth.IsAuthEnabled() {
		authed.GET("/metrics", PrometheusHandler())
	} else {
		engine.GET("/metrics", PrometheusHandler())
	}

	authed.GET("/ws", gin.WrapH(http.HandlerFunc(s.handleWebSocket)))
	authed.GET("/api/files/*path", gin.WrapH(http.HandlerFunc(s.handleServeFile)))

	// Upload: 50MB body limit.
	authed.POST("/api/upload", BodySizeLimitMiddleware(50*1024*1024), gin.WrapH(http.HandlerFunc(s.handleUpload)))
	authed.Any(storagecfg.DefaultPublicBaseURL+"/*filepath", gin.WrapH(http.HandlerFunc(s.handleMediaServe)))
	authed.Any(canvasRESTPath+"*path", gin.WrapH(http.HandlerFunc(s.handleCanvasREST)))
	authed.Any(appsRESTPath+"*path", gin.WrapH(http.HandlerFunc(s.handleAppsREST)))

	// RPC: 10MB body limit.
	authed.Any(rpcRESTPath+"*method", BodySizeLimitMiddleware(10*1024*1024), gin.WrapH(http.HandlerFunc(s.handleRPCREST)))

	// ----- Static catch-all (serves frontend SPA for unmatched routes) -----
	engine.NoRoute(s.staticCatchAllHandler())

	return engine
}

// s3GinHandler returns a Gin handler that strips the S3 mount prefix and
// delegates to the embedded S3 handler.
func (s *Server) s3GinHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := s.embeddedHandler()
		if h == nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "embedded S3 not configured"})
			return
		}
		c.Request.URL.Path = strings.TrimPrefix(c.Request.URL.Path, storagecfg.DefaultS3MountPath)
		if c.Request.URL.Path == "" {
			c.Request.URL.Path = "/"
		}
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// editorGinHandler returns a Gin handler that serves the web-editor static
// files, or nil if no editor FS/dir is configured.
func (s *Server) editorGinHandler() gin.HandlerFunc {
	var handler http.Handler
	switch {
	case s.opts.StaticEditorFS != nil:
		handler = http.StripPrefix("/editor", gzipStaticHandler(http.FileServerFS(s.opts.StaticEditorFS)))
	case s.opts.StaticEditorDir != "":
		handler = http.StripPrefix("/editor", gzipStaticHandler(http.FileServer(http.Dir(s.opts.StaticEditorDir))))
	default:
		return nil
	}
	return gin.WrapH(handler)
}

// staticCatchAllHandler returns a Gin NoRoute handler that serves frontend
// static files. Unmatched /api/, /ws, or /media paths return 404 instead of
// accidentally serving the SPA entry page.
func (s *Server) staticCatchAllHandler() gin.HandlerFunc {
	var staticHandler http.Handler
	switch {
	case s.opts.StaticFS != nil:
		staticHandler = gzipStaticHandler(http.FileServerFS(s.opts.StaticFS))
	case s.opts.StaticDir != "":
		staticHandler = gzipStaticHandler(http.FileServer(http.Dir(s.opts.StaticDir)))
	}

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") || path == "/ws" || strings.HasPrefix(path, storagecfg.DefaultPublicBaseURL+"/") {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		if staticHandler != nil {
			staticHandler.ServeHTTP(c.Writer, c.Request)
			return
		}
		c.AbortWithStatus(http.StatusNotFound)
	}
}