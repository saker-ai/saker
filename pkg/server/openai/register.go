package openai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/conversation"
	"github.com/cinience/saker/pkg/project"
	"github.com/cinience/saker/pkg/runhub"
	"github.com/cinience/saker/pkg/runhub/store"
	"github.com/gin-gonic/gin"
)

// Runner is the narrow stream-execution interface the gateway needs from
// the host runtime. *api.Runtime satisfies it directly; tests substitute a
// scripted fake. Keeping this minimal interface in the gateway package
// avoids leaking the full *api.Runtime surface area into every handler
// signature and lets us exercise the SSE/sync code paths without spinning
// up a real runtime.
type Runner interface {
	RunStream(ctx context.Context, req api.Request) (<-chan api.StreamEvent, error)
}

// Deps bundles the runtime dependencies the gateway needs from the host
// server. They are wired by the cmd_server bootstrap and not discovered
// inside the gateway — the gateway never reaches into the wider package
// graph (no skill / canvas / persona imports).
type Deps struct {
	// Runtime is the saker agent runtime that processes /v1/chat/completions.
	// Typed as the narrow Runner interface so test code can inject a fake
	// without dragging in the full pkg/api dependency. *api.Runtime
	// satisfies it directly.
	Runtime Runner
	// ProjectStore is the multi-tenant metadata store. Used by the auth
	// middleware to look up Bearer keys and resolve tenant scope. Optional
	// in dev/bypass mode but required for production.
	ProjectStore *project.Store
	// ConversationStore persists /v1/chat/completions traffic into the
	// unified conversation log. Optional: when nil the gateway runs
	// unchanged with no persistence (back-compat for tests + opt-in
	// rollout).
	ConversationStore *conversation.Store
	// Logger is the structured logger. Required.
	Logger *slog.Logger
	// Options are operator-side gateway options (rate caps, ring size, etc.).
	Options Options
}

// Gateway carries the runtime dependencies for OpenAI-compatible HTTP
// handlers. One Gateway is constructed per server start and shared across
// all /v1/* requests.
//
// hub is held as the runhub.Hub interface so the registration site can pick
// the in-memory or persistence-backed implementation based on Options.
// Handlers never need to know which backend they're talking to.
type Gateway struct {
	deps        Deps
	hub         runhub.Hub
	rateLimiter *rateLimiter
	pendingAsks *pendingAskRegistry
}

// Runtime returns the saker agent runtime backing this gateway.
func (g *Gateway) Runtime() Runner { return g.deps.Runtime }

// Options returns the operator-side options for the gateway.
func (g *Gateway) Options() Options { return g.deps.Options }

// Logger returns the structured logger configured for the gateway.
func (g *Gateway) Logger() *slog.Logger { return g.deps.Logger }

// Hub returns the run hub backing this gateway. Used by stream / reconnect
// handlers. Concrete type depends on Options.RunHubDSN (MemoryHub by default,
// PersistentHub when a DSN is configured).
func (g *Gateway) Hub() runhub.Hub { return g.hub }

// ProjectStore returns the multi-tenant metadata store. nil in legacy mode.
func (g *Gateway) ProjectStore() *project.Store { return g.deps.ProjectStore }

// RegisterOpenAIGateway mounts the /v1/* OpenAI-compatible routes on the
// supplied Gin engine and returns the Gateway handle so the caller can
// reach into hub state at shutdown (e.g. cancel in-flight runs).
//
// The function is a no-op (returns nil, nil error) when deps.Options.Enabled
// is false — this lets cmd_server unconditionally call Register without
// extra branching.
//
// Returns an error if Validate fails or required deps are missing. The
// caller should treat that as fatal: the server should refuse to start
// rather than silently disabling the gateway.
func RegisterOpenAIGateway(engine *gin.Engine, deps Deps) (*Gateway, error) {
	if !deps.Options.Enabled {
		return nil, nil
	}
	if engine == nil {
		return nil, errors.New("openai-gw: gin engine is nil")
	}
	if deps.Runtime == nil {
		return nil, errors.New("openai-gw: api.Runtime is required")
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if err := deps.Options.Validate(); err != nil {
		return nil, err
	}

	hubCfg := runhub.Config{
		MaxRuns:               deps.Options.MaxRuns,
		MaxRunsPerTenant:      deps.Options.MaxRunsPerTenant,
		RingSize:              deps.Options.RingSize,
		GCInterval:            deps.Options.RunHubGCInterval,
		TerminalRetention:     deps.Options.RunHubTerminalRetention,
		MaxEventBytes:         deps.Options.RunHubMaxEventBytes,
		SubscriberIdleTimeout: deps.Options.RunHubSubscriberIdleTimeout,
		Metrics:               NewRunhubMetricsHooks(),
		Logger:                deps.Logger,
	}
	hub, err := buildHub(hubCfg, deps.Options, deps.Logger)
	if err != nil {
		return nil, err
	}

	g := &Gateway{deps: deps, hub: hub, pendingAsks: newPendingAskRegistry()}

	// Start hub GC. Caller can stop it via Gateway.Shutdown.
	hub.StartGC(context.Background())

	// Per-tenant rate limiter. nil when RPSPerTenant <= 0, in which case
	// rateLimitMiddleware degrades to a no-op handler.
	g.rateLimiter = newRateLimiter(context.Background(), float64(deps.Options.RPSPerTenant))

	v1 := engine.Group("/v1")
	v1.Use(g.authMiddleware())
	v1.Use(rateLimitMiddleware(g.rateLimiter))
	{
		v1.GET("/models", g.handleModels)
		v1.POST("/chat/completions", g.handleChatCompletions)
		// Reconnect endpoint — clients resume an in-flight (or recently
		// terminated) run by run id, supplying their last seen seq via
		// ?last_event_id=N or the SSE Last-Event-ID header.
		v1.GET("/runs/:id/events", g.handleRunsEvents)
		// Cancel endpoint — clients abandon an in-flight run by id.
		// 204 on success; 404 (existence-leak guard) for unknown id
		// or cross-tenant access.
		v1.DELETE("/runs/:id", g.handleRunsCancel)
		v1.POST("/runs/:id/submit", g.handleRunsSubmit)
	}

	deps.Logger.Info("openai gateway mounted",
		"max_runs", deps.Options.MaxRuns,
		"max_runs_per_tenant", deps.Options.MaxRunsPerTenant,
		"rps_per_tenant", deps.Options.RPSPerTenant,
		"ring_size", deps.Options.RingSize,
		"expires_after_seconds", deps.Options.ExpiresAfterSeconds,
		"dev_bypass", deps.Options.DevBypassAuth,
		"runhub_persisted", deps.Options.RunHubDSN != "",
	)

	return g, nil
}

// buildHub returns the runhub backend selected by dsn. Empty DSN gives
// the in-memory hub (default, zero-config). Non-empty opens a *store.Store
// and wraps it in a PersistentHub. Errors here surface back to Register
// so a misconfigured DSN refuses to start the server instead of silently
// degrading to in-memory. The store's reconnect callback is wired to the
// runhub_listener_reconnects_total counter so dashboards see drop+recover
// cycles in real time.
func buildHub(cfg runhub.Config, opts Options, logger *slog.Logger) (runhub.Hub, error) {
	if opts.RunHubDSN == "" {
		return runhub.NewHub(cfg), nil
	}
	hooks := NewRunhubMetricsHooks()
	st, err := store.Open(store.Config{
		DSN:                 opts.RunHubDSN,
		OnListenerReconnect: hooks.OnListenerReconnect,
		PGCopyThreshold:     opts.RunHubPGCopyThreshold,
	})
	if err != nil {
		return nil, fmt.Errorf("openai-gw: open runhub store: %w", err)
	}
	hub, err := runhub.NewPersistentHub(runhub.PersistentConfig{
		Config:               cfg,
		Store:                st,
		Metrics:              hooks,
		BatchSize:            opts.RunHubBatchSize,
		BatchBufferSize:      opts.RunHubBatchBufferSize,
		BatchInterval:        opts.RunHubBatchInterval,
		SinkBreakerThreshold: opts.RunHubSinkBreakerThreshold,
		SinkBreakerCooldown:  opts.RunHubSinkBreakerCooldown,
	})
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("openai-gw: build persistent hub: %w", err)
	}
	logger.Info("openai gateway runhub backend",
		"backend", "persistent",
		"driver", st.Driver(),
		"dsn", opts.RunHubDSN,
		"batch_size", opts.RunHubBatchSize,
		"batch_buffer", opts.RunHubBatchBufferSize,
		"batch_interval", opts.RunHubBatchInterval,
		"pg_copy_threshold", opts.RunHubPGCopyThreshold,
	)
	return hub, nil
}

// Shutdown stops the hub's background GC and signals all in-flight runs
// to terminate. Idempotent.
func (g *Gateway) Shutdown() {
	if g == nil {
		return
	}
	g.rateLimiter.Close()
	if g.hub != nil {
		g.hub.Shutdown()
	}
}
