package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/config"
	"github.com/saker-ai/saker/pkg/conversation"
	"github.com/saker-ai/saker/pkg/logging"
	"github.com/saker-ai/saker/pkg/middleware"
	"github.com/saker-ai/saker/pkg/project"
	"github.com/saker-ai/saker/pkg/sandbox/landlockenv"
	"github.com/saker-ai/saker/pkg/server"
	sakersynapse "github.com/saker-ai/saker/pkg/synapse"
	openaigw "github.com/saker-ai/saker/pkg/server/openai"
	"github.com/gin-gonic/gin"
)

type openaiGatewayFlags struct {
	Enabled                     bool
	MaxRuns                     int
	MaxRunsPerTenant            int
	RPSPerTenant                int
	RingSize                    int
	ExpiresAfterSeconds         int
	DevBypassAuth               bool
	RunHubDSN                   string
	RunHubBatchSize             int
	RunHubBatchBufferSize       int
	RunHubBatchInterval         time.Duration
	RunHubGCInterval            time.Duration
	RunHubTerminalRetention     time.Duration
	RunHubMaxEventBytes         int64
	RunHubSubscriberIdleTimeout time.Duration
	RunHubSinkBreakerThreshold  int
	RunHubSinkBreakerCooldown   time.Duration
	RunHubPGCopyThreshold       int
}

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

type synapseHubFlags struct {
	HubAddr       string
	AuthToken     string
	InstanceID    string
	SandboxID     string
	Models        string
	MaxConcurrent int
	Labels        string
	Insecure      bool
}

func (a *App) runServerMode(stdout, stderr io.Writer, opts api.Options, addr, dataDir, staticDir, logDir string, debug bool, gwFlags openaiGatewayFlags, synFlags synapseHubFlags) error {
	opts.EntryPoint = api.EntryPointPlatform

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

	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".saker", "server")
	}

	opts.CanvasDir = filepath.Join(dataDir, "canvas")

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

	conversationStore, err := conversation.Open(conversation.Config{
		DSN:          os.Getenv("SAKER_CONVERSATION_DSN"),
		FallbackPath: filepath.Join(dataDir, "conversation.db"),
	})
	if err != nil {
		return fmt.Errorf("open conversation store: %w", err)
	}
	defer conversationStore.Close()
	opts.ConversationStore = conversationStore

	rt, err := a.runtimeFactory(ctx, opts)
	if err != nil {
		return fmt.Errorf("create runtime: %w", err)
	}
	defer rt.Close()

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
		sub, subErr := a.GetFrontendFS()
		if subErr != nil {
			fmt.Fprintf(stderr, "Warning: %v, serving API only\n", subErr)
		} else {
			srvOpts.StaticFS = sub
		}
	}
	if editorSub, editorErr := a.GetEditorFS(); editorErr != nil {
		fmt.Fprintf(stderr, "Warning: editor sub-app unavailable: %v\n", editorErr)
	} else {
		srvOpts.StaticEditorFS = editorSub
	}

	apiRuntime, ok := rt.(*api.Runtime)
	if !ok {
		return fmt.Errorf("server mode requires api.Runtime")
	}

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
			pwFile := filepath.Join(srvOpts.DataDir, "initial-password.txt")
			if writeErr := os.WriteFile(pwFile, []byte(plain), 0o600); writeErr != nil {
				fmt.Fprintf(stderr, "Warning: failed to write initial password file: %v\n", writeErr)
				fmt.Fprintf(stdout, "Web auth: username=admin password=%s (remote access only)\n", plain)
			} else {
				fmt.Fprintf(stdout, "Web auth: username=admin — initial password written to %s (remote access only)\n", pwFile)
			}
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

	var synCancel context.CancelFunc
	if synFlags.HubAddr != "" {
		synCtx, cancel := context.WithCancel(context.Background())
		synCancel = cancel
		models := strings.Split(synFlags.Models, ",")
		instanceID := synFlags.InstanceID
		if instanceID == "" {
			instanceID = "saker-" + uuid.NewString()[:8]
		}
		labels := map[string]string{}
		if synFlags.Labels != "" {
			_ = json.Unmarshal([]byte(synFlags.Labels), &labels)
		}
		bridgeCfg := sakersynapse.BridgeConfig{
			HubAddr:       synFlags.HubAddr,
			AuthToken:     synFlags.AuthToken,
			InstanceID:    instanceID,
			SandboxID:     synFlags.SandboxID,
			Models:        models,
			MaxConcurrent: int32(synFlags.MaxConcurrent),
			Labels:        labels,
			InsecureTLS:   synFlags.Insecure,
			SakerBaseURL:  "http://127.0.0.1" + addr,
		}
		go sakersynapse.RunBridge(synCtx, bridgeCfg)
		fmt.Fprintf(stdout, "Synapse hub registration enabled → %s\n", synFlags.HubAddr)
	}

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
		if synCancel != nil {
			synCancel()
		}
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		if gw != nil {
			gw.Shutdown()
		}
		return srv.Shutdown(shutCtx)
	}
}
