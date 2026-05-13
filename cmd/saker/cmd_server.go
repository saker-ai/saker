package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/conversation"
	"github.com/cinience/saker/pkg/logging"
	"github.com/cinience/saker/pkg/middleware"
	"github.com/cinience/saker/pkg/project"
	"github.com/cinience/saker/pkg/sandbox/landlockenv"
	"github.com/cinience/saker/pkg/server"
	openaigw "github.com/cinience/saker/pkg/server/openai"
	"github.com/gin-gonic/gin"
)

// openaiGatewayFlags carries the `--openai-gw-*` flags resolved by main.go.
// Bundled into a struct so runServerMode's signature stays readable as more
// knobs are added.
type openaiGatewayFlags struct {
	Enabled             bool
	MaxRuns             int
	MaxRunsPerTenant    int
	RPSPerTenant        int
	RingSize            int
	ExpiresAfterSeconds int
	DevBypassAuth       bool
	// RunHubDSN selects the run-hub backend. Empty = in-memory (default,
	// zero-config); a sqlite path or sqlite://path = embedded SQLite-backed
	// persistence; a postgres://... DSN requires the binary to be built
	// with `-tags postgres`. See pkg/server/openai.Options.RunHubDSN.
	RunHubDSN string
	// RunHubBatchSize bounds the persistent hub's async writer batch
	// size. Zero = default (64).
	RunHubBatchSize int
	// RunHubBatchBufferSize bounds the persistent hub's enqueue chan
	// capacity. Zero = default (1024). Drop-oldest backpressure when full.
	RunHubBatchBufferSize int
	// RunHubBatchInterval bounds the persistent hub's writer idle window.
	// Zero = default (50ms).
	RunHubBatchInterval time.Duration
	// RunHubGCInterval is how often the hub sweeper runs. Zero = default
	// (30s). See pkg/server/openai.Options.RunHubGCInterval.
	RunHubGCInterval time.Duration
	// RunHubTerminalRetention is how long terminal runs are retained
	// before GC reclaims them. Zero = default (60s). See
	// pkg/server/openai.Options.RunHubTerminalRetention.
	RunHubTerminalRetention time.Duration
	// RunHubMaxEventBytes caps per-event payload size. Zero = unbounded
	// (legacy behavior, NOT recommended in production). Default 1 MiB.
	// See pkg/server/openai.Options.RunHubMaxEventBytes.
	RunHubMaxEventBytes int64
	// RunHubSubscriberIdleTimeout closes per-run subscriber channels
	// that sit idle past this window. Zero = disabled. See
	// pkg/server/openai.Options.RunHubSubscriberIdleTimeout.
	RunHubSubscriberIdleTimeout time.Duration
	// RunHubSinkBreakerThreshold is the persistent-hub sink circuit
	// breaker's consecutive-failure threshold. Zero disables the
	// breaker. See pkg/server/openai.Options.RunHubSinkBreakerThreshold.
	RunHubSinkBreakerThreshold int
	// RunHubSinkBreakerCooldown is how long the breaker stays Open
	// before allowing a probe call. Zero with non-zero threshold
	// latches Open until restart. See
	// pkg/server/openai.Options.RunHubSinkBreakerCooldown.
	RunHubSinkBreakerCooldown time.Duration
	// RunHubPGCopyThreshold gates the postgres COPY-based bulk insert
	// path. Zero disables the COPY path entirely; ignored on
	// non-postgres drivers. See
	// pkg/server/openai.Options.RunHubPGCopyThreshold.
	RunHubPGCopyThreshold int
}

// toOptions converts the raw CLI flag values into an openai.Options.
func (g openaiGatewayFlags) toOptions() openaigw.Options {
	return openaigw.Options{
		Enabled:                     g.Enabled,
		MaxRuns:                     g.MaxRuns,
		MaxRunsPerTenant:            g.MaxRunsPerTenant,
		RPSPerTenant:                g.RPSPerTenant,
		RingSize:                    g.RingSize,
		ExpiresAfterSeconds:         g.ExpiresAfterSeconds,
		DevBypassAuth:               g.DevBypassAuth,
		RunHubDSN:                   g.RunHubDSN,
		RunHubBatchSize:             g.RunHubBatchSize,
		RunHubBatchBufferSize:       g.RunHubBatchBufferSize,
		RunHubBatchInterval:         g.RunHubBatchInterval,
		RunHubGCInterval:            g.RunHubGCInterval,
		RunHubTerminalRetention:     g.RunHubTerminalRetention,
		RunHubMaxEventBytes:         g.RunHubMaxEventBytes,
		RunHubSubscriberIdleTimeout: g.RunHubSubscriberIdleTimeout,
		RunHubSinkBreakerThreshold:  g.RunHubSinkBreakerThreshold,
		RunHubSinkBreakerCooldown:   g.RunHubSinkBreakerCooldown,
		RunHubPGCopyThreshold:       g.RunHubPGCopyThreshold,
	}
}

// runServerMode starts the embedded HTTP server, wires the project store,
// auto-enables Landlock when available, and resolves web auth credentials.
func runServerMode(stdout, stderr io.Writer, opts api.Options, addr, dataDir, staticDir, logDir string, debug bool, gwFlags openaiGatewayFlags) error {
	opts.EntryPoint = api.EntryPointPlatform

	// Auto-enable Landlock sandbox when kernel supports it and user didn't
	// explicitly choose a sandbox backend.
	if opts.Sandbox.Type == "" && landlockenv.Available() {
		absRoot, _ := filepath.Abs(opts.ProjectRoot)
		if absRoot == "" {
			absRoot = opts.ProjectRoot
		}
		opts.Sandbox = api.SandboxOptions{
			Type: "landlock",
			Landlock: &api.LandlockOptions{
				Enabled:                    true,
				DefaultGuestCwd:            absRoot,
				AutoCreateSessionWorkspace: true,
				SessionWorkspaceBase:       filepath.Join(absRoot, "workspace"),
			},
		}
		fmt.Fprintln(stdout, "Landlock sandbox auto-enabled (kernel support detected)")
	}

	// Default data directory for session persistence.
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".saker", "server")
	}

	// Expose canvas storage to built-in tools (canvas_get_node).
	opts.CanvasDir = filepath.Join(dataDir, "canvas")

	// Initialize structured logger for server mode.
	if logDir == "" {
		logDir = filepath.Join(dataDir, "logs")
	}
	logger, logCleanup, logErr := logging.Setup(logDir)
	if logErr != nil {
		fmt.Fprintf(stderr, "Warning: failed to setup file logging: %v\n", logErr)
	}
	if logCleanup != nil {
		defer logCleanup()
	}
	if logger != nil {
		logger.Info("server log initialized", "log_dir", logDir)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if logger != nil {
		ctx = logging.WithLogger(ctx, logger)
	}

	// Wire OpenTelemetry tracing when OTEL_EXPORTER_OTLP_ENDPOINT is set.
	// When unset, the global tracer remains a noop and HTTP middleware adds
	// near-zero overhead.
	if otlpCfg, enabled := middleware.OTLPConfigFromEnv(); enabled {
		shutdown, otelErr := middleware.SetupOTLP(ctx, otlpCfg)
		if otelErr != nil {
			fmt.Fprintf(stderr, "Warning: OTLP setup failed: %v\n", otelErr)
		} else {
			fmt.Fprintf(stdout, "OTLP tracing enabled: endpoint=%s service=%s\n",
				otlpCfg.Endpoint, otlpCfg.ServiceName)
			defer func() {
				shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutCancel()
				if err := shutdown(shutCtx); err != nil && logger != nil {
					logger.Warn("OTLP shutdown error", "error", err)
				}
			}()
		}
	}

	// Open the unified conversation store BEFORE the runtime so the runtime
	// can own it (api.New consumes opts.ConversationStore) and the server's
	// SessionStore tee — wired in server.New / per_project_components.go via
	// runtime.ConversationStore() — sees a non-nil store. Without this, the
	// CLI path persists into conversation.db but the Web UI / OpenAI gateway
	// paths silently degrade to legacy-only writes.
	//
	// SAKER_CONVERSATION_DSN keeps the file independent of the project store
	// so an operator can park conversation traffic on a separate disk / pg
	// instance for retention / size-budget reasons.
	conversationStore, err := conversation.Open(conversation.Config{
		DSN:          os.Getenv("SAKER_CONVERSATION_DSN"),
		FallbackPath: filepath.Join(dataDir, "conversation.db"),
	})
	if err != nil {
		return fmt.Errorf("open conversation store: %w", err)
	}
	defer conversationStore.Close()
	opts.ConversationStore = conversationStore

	rt, err := runtimeFactory(ctx, opts)
	if err != nil {
		return fmt.Errorf("create runtime: %w", err)
	}
	defer rt.Close()

	// Open the multi-tenant project store. DSN comes from SAKER_DB_DSN
	// (sqlite/postgres); empty falls back to <dataDir>/app.db (sqlite).
	projectStore, err := project.Open(project.Config{
		DSN:          os.Getenv("SAKER_DB_DSN"),
		FallbackPath: filepath.Join(dataDir, "app.db"),
	})
	if err != nil {
		return fmt.Errorf("open project store: %w", err)
	}
	defer projectStore.Close()

	srvOpts := server.Options{
		Addr:         addr,
		DataDir:      dataDir,
		Debug:        debug,
		Logger:       logger,
		ProjectStore: projectStore,
	}
	if staticDir != "" {
		srvOpts.StaticDir = staticDir
	} else {
		sub, subErr := getEmbeddedFrontend()
		if subErr != nil {
			fmt.Fprintf(stderr, "Warning: %v, serving API only\n", subErr)
		} else {
			srvOpts.StaticFS = sub
		}
	}
	// Mount the OpenCut-derived browser editor at /editor/. Non-fatal on
	// failure — the main app still works; only the editor sub-app is
	// unavailable. An empty placeholder dist still yields a valid FS.
	if editorSub, editorErr := getEmbeddedEditor(); editorErr != nil {
		fmt.Fprintf(stderr, "Warning: editor sub-app unavailable: %v\n", editorErr)
	} else {
		srvOpts.StaticEditorFS = editorSub
	}

	apiRuntime, ok := rt.(*api.Runtime)
	if !ok {
		return fmt.Errorf("server mode requires api.Runtime")
	}

	// Optional OpenAI-compatible gateway. Mounted via Server.EngineHook so the
	// pkg/server package never has to import pkg/server/openai. The hook
	// closure runs once during ListenAndServe; a non-nil error aborts startup.
	// gw is captured here so the SIGTERM path can call Shutdown to drain
	// background goroutines (hub GC, in-flight runs).
	var gw *openaigw.Gateway
	if gwFlags.Enabled {
		srvOpts.EngineHook = func(engine *gin.Engine) error {
			deps := openaigw.Deps{
				Runtime:           apiRuntime,
				ProjectStore:      projectStore,
				ConversationStore: conversationStore,
				Logger:            logger,
				Options:           gwFlags.toOptions(),
			}
			g, err := openaigw.RegisterOpenAIGateway(engine, deps)
			if err != nil {
				return fmt.Errorf("register openai gateway: %w", err)
			}
			gw = g
			return nil
		}
		runhubBackend := "in-memory"
		if gwFlags.RunHubDSN != "" {
			runhubBackend = "persistent (" + gwFlags.RunHubDSN + ")"
		}
		fmt.Fprintf(stdout, "OpenAI-compatible gateway enabled at /v1/* (max_runs=%d, ring=%d, expires=%ds, runhub=%s)\n",
			gwFlags.MaxRuns, gwFlags.RingSize, gwFlags.ExpiresAfterSeconds, runhubBackend)
		if gwFlags.DevBypassAuth {
			fmt.Fprintln(stderr, "WARNING: --openai-gw-dev-bypass=true accepts requests without Bearer auth (localhost identity); never use in production")
		}
	}

	// Resolve web auth config: use existing settings or auto-generate credentials.
	if settings := apiRuntime.Settings(); settings != nil && settings.WebAuth != nil && settings.WebAuth.Password != "" {
		srvOpts.WebAuth = settings.WebAuth
		username := srvOpts.WebAuth.Username
		if username == "" {
			username = "admin"
		}
		fmt.Fprintf(stdout, "Web auth enabled: username=%s (remote access only)\n", username)
	} else {
		plain, hash, genErr := server.GeneratePassword()
		if genErr != nil {
			fmt.Fprintf(stderr, "Warning: failed to generate auth credentials: %v\n", genErr)
		} else {
			srvOpts.WebAuth = &config.WebAuthConfig{Username: "admin", Password: hash}
			// Write initial password to a file instead of stdout so it
			// doesn't persist in infrastructure logs.
			pwFile := filepath.Join(srvOpts.DataDir, "initial-password.txt")
			if writeErr := os.WriteFile(pwFile, []byte(plain), 0o600); writeErr != nil {
				fmt.Fprintf(stderr, "Warning: failed to write initial password file: %v\n", writeErr)
				// Fall back to stdout only if file write fails.
				fmt.Fprintf(stdout, "Web auth: username=admin password=%s (remote access only)\n", plain)
			} else {
				fmt.Fprintf(stdout, "Web auth: username=admin — initial password written to %s (remote access only)\n", pwFile)
			}
			// Persist to settings.local.json so the password survives restarts.
			if saveErr := config.SaveSettingsLocal(opts.ProjectRoot, &config.Settings{WebAuth: srvOpts.WebAuth}); saveErr != nil {
				fmt.Fprintf(stderr, "Warning: failed to save auth config: %v\n", saveErr)
			}
		}
	}

	srv, err := server.New(apiRuntime, srvOpts)
	if err != nil {
		return fmt.Errorf("create server: %w", err)
	}

	fmt.Fprintf(stdout, "Saker server listening on %s\n", addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		if gw != nil {
			gw.Shutdown()
		}
		return err
	case <-sigCh:
		fmt.Fprintln(stdout, "\nShutting down...")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		// Stop the OpenAI gateway first so the hub GC goroutine and any
		// in-flight runs are cancelled before the HTTP server stops accepting.
		if gw != nil {
			gw.Shutdown()
		}
		return srv.Shutdown(shutCtx)
	}
}
