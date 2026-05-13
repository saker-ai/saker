package server

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/project"
	storagecfg "github.com/cinience/saker/pkg/storage"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// Options configures the server.
type Options struct {
	Addr      string // listen address, default ":10112"
	DataDir   string // persistence directory, default "~/.saker/server"
	StaticDir string // optional: serve frontend static files from disk
	StaticFS  fs.FS  // optional: embedded frontend filesystem (takes precedence over StaticDir)
	// Optional sub-app served at /editor/ (OpenCut-derived browser editor).
	// EditorFS takes precedence over EditorDir when both are set.
	StaticEditorDir string
	StaticEditorFS  fs.FS
	Debug           bool                  // enable /debug/pprof endpoints
	Logger          *slog.Logger          // structured logger; nil falls back to slog.Default()
	WebAuth         *config.WebAuthConfig // optional: web auth config for remote access
	// ProjectStore is the multi-tenant metadata store (users/teams/projects).
	// Optional: when nil, the server runs in single-project compatibility mode
	// using the legacy DataDir layout. P2+ handlers require this to be set.
	ProjectStore *project.Store
	// EngineHook, when non-nil, is invoked once during ListenAndServe after
	// the core routes are mounted but BEFORE the static catch-all NoRoute
	// handler. Used by cmd_server to mount the optional OpenAI-compatible
	// gateway (pkg/server/openai) without making the server package import
	// the gateway package directly. A non-nil error returned from the hook
	// aborts ListenAndServe.
	EngineHook func(*gin.Engine) error
}

func (o *Options) defaults() {
	if o.Addr == "" {
		o.Addr = ":10112"
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

// Server is a WebSocket JSON-RPC server backed by an api.Runtime.
type Server struct {
	runtime     *api.Runtime
	sessions    *SessionStore
	handler     *Handler
	httpSrv     *http.Server
	opts        Options
	logger      *slog.Logger
	upgrader    websocket.Upgrader
	scheduler   *Scheduler
	auth        *AuthManager
	projects    *project.Store
	uploadCount atomic.Int64

	// autoSyncCancel terminates the background skillhub auto-sync goroutine.
	// nil when no goroutine is running.
	autoSyncCancel context.CancelFunc

	// embedded is the in-process s2 server when settings.storage.backend ==
	// "embedded". nil for every other backend; Shutdown stops it.
	//
	// embeddedMu guards swaps from reloadObjectStore. Reads must go through
	// embeddedHandler() / Shutdown's snapshot so a concurrent settings/update
	// can replace the backend without crashing in-flight /_s3/ requests.
	embeddedMu sync.RWMutex
	embedded   *storagecfg.Embedded

	// rateLimiterCleanup stops the RateLimitMiddleware background goroutine
	// on shutdown. nil when no rate limiter is active.
	rateLimiterCleanup func()

	// bearerRateLimiterCleanup stops the BearerRateLimitMiddleware visitor
	// eviction goroutine on shutdown. nil when no Bearer limiter is active.
	bearerRateLimiterCleanup func()
}

// ProjectStore returns the multi-tenant metadata store, or nil if the server
// was started without one (legacy single-project mode).
func (s *Server) ProjectStore() *project.Store { return s.projects }

// New creates a Server wrapping the given Runtime.
func New(runtime *api.Runtime, opts Options) (*Server, error) {
	opts.defaults()
	sessions, err := NewSessionStore()
	if err != nil {
		return nil, err
	}
	if cs := runtime.ConversationStore(); cs != nil {
		_ = sessions.LoadFromConversation(cs, "default")
	}
	sessions.AttachConvTee(newConvTee(runtime.ConversationStore(), "default", opts.Logger))
	h := newHandler(runtime, sessions, opts.DataDir, opts.Logger)

	// Initialize active turn tracker.
	tracker := NewActiveTurnTracker()
	h.SetTracker(tracker)

	// Initialize cron store and scheduler.
	cronStore, err := NewCronStore(opts.DataDir)
	if err != nil {
		return nil, err
	}
	scheduler := NewScheduler(cronStore, h, tracker, opts.Logger)
	h.SetCron(cronStore, scheduler)

	auth := NewAuthManager(opts.WebAuth, opts.Logger)
	h.SetAuth(auth)

	// Wire the multi-tenant project store. Pass nil for embedded library
	// callers that don't need multi-tenancy — the scope middleware then
	// stays inert and all RPCs run on h.dataDir.
	h.SetProjects(opts.ProjectStore)
	// Same store goes into the auth layer so login flows can upsert user
	// rows and provision a personal project for the localhost branch.
	auth.SetProjectStore(opts.ProjectStore)
	auth.SetKeyDir(opts.DataDir)

	var s *Server
	s = &Server{
		runtime:   runtime,
		sessions:  sessions,
		handler:   h,
		httpSrv:   nil,
		opts:      opts,
		logger:    opts.Logger,
		scheduler: scheduler,
		auth:      auth,
		projects:  opts.ProjectStore,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true // non-browser clients (CLI, scripts)
				}
				var allowed []string
				if settings := s.runtime.Settings(); settings != nil && settings.CORS != nil {
					allowed = settings.CORS.AllowedOrigins
				}
				return isAllowedWSOrigin(origin, allowed)
			},
			EnableCompression: true,
		},
	}

	// Wire the pluggable object store. Failure here is non-fatal — the
	// handler keeps the legacy on-disk path under <projectRoot>/.saker/
	// canvas-media when objectStore is nil, so a misconfigured backend
	// degrades to the prior behavior rather than crashing startup.
	emb, err := s.openObjectStore(context.Background())
	if err != nil {
		opts.Logger.Warn("object store init failed; falling back to legacy on-disk cache", "error", err)
	} else {
		s.embedded = emb
	}

	// Wire the reloader so handleSettingsUpdate can hot-swap the backend
	// when the admin edits settings.storage from the web UI.
	s.handler.SetStorageReloader(s.reloadObjectStore)

	return s, nil
}

// ListenAndServe starts the HTTP server. Route registration and middleware
// live in gin_engine.go; background loops live in lifecycle.go.
func (s *Server) ListenAndServe() error {
	engine, err := s.buildGinEngine()
	if err != nil {
		return err
	}

	s.httpSrv = &http.Server{
		Addr:              s.opts.Addr,
		Handler:           engine,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Start cron scheduler.
	if s.scheduler != nil {
		s.scheduler.Start()
	}

	s.startBackgroundLoops()

	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return err
	}
	s.logger.Info("server started", "addr", ln.Addr().String(), "data_dir", s.opts.DataDir)
	return s.httpSrv.Serve(ln)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	// Cancel all active turns so they terminate cleanly with proper
	// turn/finished notifications before the HTTP server shuts down.
	if s.handler != nil {
		s.handler.CancelAllTurns()
		s.handler.Close()
	}
	if s.auth != nil {
		s.auth.Close()
	}
	if s.rateLimiterCleanup != nil {
		s.rateLimiterCleanup()
		s.rateLimiterCleanup = nil
	}
	if s.bearerRateLimiterCleanup != nil {
		s.bearerRateLimiterCleanup()
		s.bearerRateLimiterCleanup = nil
	}
	if s.autoSyncCancel != nil {
		s.autoSyncCancel()
		s.autoSyncCancel = nil
	}
	if s.scheduler != nil {
		s.scheduler.Stop()
	}
	s.embeddedMu.Lock()
	emb := s.embedded
	s.embedded = nil
	s.embeddedMu.Unlock()
	if emb != nil {
		if err := emb.Stop(); err != nil {
			s.logger.Warn("embedded storage shutdown error", "error", err)
		}
	}
	if s.httpSrv != nil {
		return s.httpSrv.Shutdown(ctx)
	}
	return nil
}

// runMediaCacheCleanup builds the union of media-file references across
// every project's SessionStore (or the legacy single store when no projects
// table is wired) and sweeps the on-disk media cache once.
//
// Skillhub auto-sync is intentionally NOT walked per-project here: that
// loop reads a single server-scoped skillhub config (via runtime.ProjectRoot)
// and would need the whole skillhub stack to grow a Scope before it could
// be split. Tracked for a future change rather than papered over here.
func (s *Server) runMediaCacheCleanup() {
	referenced := map[string]bool{}
	collectMediaReferences(s.sessions, referenced)

	if s.projects != nil && s.handler != nil {
		ctx := context.Background()
		projects, err := s.projects.ListAllProjects(ctx)
		if err != nil {
			s.logger.Warn("media cache cleanup: list projects failed", "error", err)
		} else {
			for _, p := range projects {
				scope := project.Scope{
					ProjectID: p.ID,
					Paths:     project.BuildPaths(s.opts.DataDir, p.ID),
				}
				store := s.handler.sessionsFor(project.WithScope(ctx, scope))
				if store != nil && store != s.sessions {
					collectMediaReferences(store, referenced)
				}
			}
		}
	}

	sweepMediaCache(filepath.Join(s.runtime.ProjectRoot(), canvasMediaCacheDir), referenced, s.logger)
}

// runSkillhubAutoSyncLoop periodically calls handler.RunSkillhubAutoSync.
//
// We re-read the interval each iteration so a settings change picks up on the
// next wake-up rather than requiring a server restart. The interval defaults
// to 15 minutes when unset; when AutoSync is disabled the wake-up is a cheap
// no-op that just checks the flag again. We also delay the FIRST run by the
// interval so server startup isn't immediately followed by network I/O.
func (s *Server) runSkillhubAutoSyncLoop(ctx context.Context) {
	for {
		interval := s.handler.SkillhubSyncInterval()
		if interval <= 0 {
			interval = 15 * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		if !s.handler.SkillhubAutoSyncEnabled() {
			continue
		}
		// Use a bounded child context so a hung sync can't block the loop.
		runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		if err := s.handler.RunSkillhubAutoSync(runCtx); err != nil {
			s.logger.Warn("skillhub auto-sync failed", "error", err)
		}
		cancel()
	}
}
