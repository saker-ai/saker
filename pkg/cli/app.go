package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	acpserver "github.com/saker-ai/saker/pkg/acp"
	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/clikit"
	"github.com/saker-ai/saker/pkg/clikit/tui"
	"github.com/saker-ai/saker/pkg/config"
	"github.com/saker-ai/saker/pkg/im"
	"github.com/saker-ai/saker/pkg/logging"
	"github.com/saker-ai/saker/pkg/profile"
	"github.com/saker-ai/saker/pkg/project"
	"github.com/saker-ai/saker/pkg/sandbox/gvisorhelper"
	"github.com/saker-ai/saker/pkg/sandbox/landlockhelper"
	"github.com/saker-ai/saker/pkg/server"
	"github.com/saker-ai/saker/pkg/skillhub"
	versionpkg "github.com/saker-ai/saker/pkg/version"
	"github.com/godeps/goim"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

// RuntimeClient is the interface for the saker runtime.
type RuntimeClient interface {
	Run(context.Context, api.Request) (*api.Response, error)
	RunStream(context.Context, api.Request) (<-chan api.StreamEvent, error)
	Close() error
}

type streamEngine = clikit.StreamEngine
type replEngine = clikit.ReplEngine

// App is the saker CLI application.
type App struct {
	Version string

	// Injected at build time via build tags — defaults set in New().
	GetFrontendFS           func() (fs.FS, error)
	GetEditorFS             func() (fs.FS, error)
	ValidateGovmPlatform    func() error
	ValidateGovmRuntime     func(api.GovmOptions) error
	IsGovmNativeUnavailable func(error) bool

	// Internal hooks — overridable for testing within package cli.
	runtimeFactory    func(context.Context, api.Options) (RuntimeClient, error)
	serveACPStdio     func(context.Context, api.Options, io.Reader, io.Writer) error
	runStream         func(ctx context.Context, stdout, stderr io.Writer, engine clikit.StreamEngine, sessionID, prompt string, timeoutMs int, verbose bool, waterfall string) error
	runGVisorHelper   func(context.Context, io.Reader, io.Writer, io.Writer) error
	runLandlockHelper func(context.Context, io.Reader, io.Writer, io.Writer) error
}

// New creates a new App with default implementations.
func New() *App {
	return &App{
		GetFrontendFS:           getEmbeddedFrontend,
		GetEditorFS:             getEmbeddedEditor,
		ValidateGovmPlatform:    validateGovmPlatformDefault,
		ValidateGovmRuntime:     validateGovmRuntimeDefault,
		IsGovmNativeUnavailable: isGovmNativeUnavailableDefault,
		runtimeFactory: func(ctx context.Context, opts api.Options) (RuntimeClient, error) {
			return api.New(ctx, opts)
		},
		serveACPStdio:     acpserver.ServeStdio,
		runStream:         clikit.RunStream,
		runGVisorHelper:   gvisorhelper.Run,
		runLandlockHelper: landlockhelper.Run,
	}
}

// @title Saker API
// @version 1.0
// @description Saker REST + WebSocket API exposed by the embedded web server.
// @description Localhost requests are auto-elevated to admin; remote callers must authenticate via session cookie or, for app runs, a Bearer API key. Public share-token endpoints under /api/apps/public/ require no authentication.
// @contact.name Saker
// @contact.url https://github.com/saker-ai/saker
// @license.name Apache 2.0
// @license.url https://www.apache.org/licenses/LICENSE-2.0.html
// @host localhost:10112
// @BasePath /
// @schemes http https
// @securityDefinitions.apikey CookieAuth
// @in cookie
// @name saker_session
// @description Session cookie issued by POST /api/auth/login. Required for remote (non-localhost) requests.
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Bearer API key for app-run endpoints (e.g. POST /api/apps/{appId}/run). Format: "Bearer ak_...".

// Run is the main CLI entry point.
func (a *App) Run(argv []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("agentctl", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintf(stderr, `Usage: agentctl [options] [prompt]

Saker CLI - starts an interactive session by default, use -p/--print for
non-interactive output

Arguments:
  prompt                        Your prompt

Options:
`)
		flags.PrintDefaults()
	}

	entry := flags.String("entry", "cli", "Entry point type (cli/ci/platform)")
	projectFlag := flags.String("project", ".", "Project root")
	claudeDir := flags.String("claude", "", "Optional path to .saker directory")
	configRoot := flags.String("config-root", "", "Optional config root directory (defaults to <project>/.saker)")
	providerName := flags.String("provider", "", "Model provider: anthropic|openai (default: auto-detect from env)")
	modelName := flags.String("model", "", "Model for the current session")
	systemPrompt := flags.String("system-prompt", "", "System prompt to use for the session")

	sessionID := flags.String("session-id", "", "Session identifier (UUID)")
	sessionAlias := flags.String("session", "", "Session identifier (alias for --session-id)")

	timeoutMs := flags.Int("timeout-ms", 10*60*1000, "Run timeout in milliseconds")
	printConfig := flags.Bool("print-effective-config", false, "Print resolved runtime config before running")
	promptFile := flags.String("prompt-file", "", "Read prompt from file")
	promptLiteral := flags.String("prompt", "", "Prompt literal (overrides stdin)")

	printMode := flags.Bool("print", false, "Print response and exit (non-interactive)")
	printShort := flags.Bool("p", false, "Print response and exit (short for --print)")
	streamAlias := flags.Bool("stream", false, "Stream events (alias for --print)")

	outputFormat := flags.String("output-format", "", "Output format: text, json, stream-json (only with --print)")
	streamFormatAlias := flags.String("stream-format", "", "Output format (alias for --output-format)")

	repl := flags.Bool("repl", false, "Run interactive REPL mode")
	gvisorHelper := flags.Bool("saker-gvisor-helper", false, "Run hidden gVisor helper mode")
	landlockHelper := flags.Bool("saker-landlock-helper", false, "Run hidden Landlock helper mode")
	sandboxBackend := flags.String("sandbox-backend", "host", "Sandbox backend: host|gvisor|govm|landlock")
	sandboxProjectMount := flags.String("sandbox-project-mount", "ro", "Project mount mode for virtualized sandbox: ro|rw|off")
	sandboxImage := flags.String("sandbox-image", "", "Offline image override for govm sandbox")
	verbose := flags.Bool("verbose", false, "Enable verbose output")
	waterfall := flags.String("waterfall", clikit.WaterfallModeOff, "Waterfall output mode: off|summary|full")
	skillsRecursive := flags.Bool("skills-recursive", true, "Discover SKILL.md recursively")
	acpMode := flags.Bool("acp", false, "Run ACP server over stdio")
	tuiMode := flags.String("tui", "auto", "TUI rendering mode: auto|on|off (auto picks TUI when stdin and stdout are TTYs; otherwise legacy REPL)")
	pipelineFile := flags.String("pipeline", "", "Load pipeline definition from JSON file")
	showTimeline := flags.Bool("timeline", false, "Print pipeline timeline events")
	lineageFormat := flags.String("lineage", "", "Output lineage graph (dot)")
	videoStream := flags.String("video-stream", "", "Video stream source: file path or watch:<dir>")
	segmentDuration := flags.Duration("segment-duration", 2*time.Second, "Segment duration for video stream")
	videoWindowSize := flags.Int("video-window", 3, "Sliding window size for stream/frame context")
	videoSampleRate := flags.Int("sample-rate", 1, "Process every Nth frame (frame processor mode)")
	videoEvents := flags.String("events", "", "Comma-separated event keywords for realtime detection")
	showVersion := flags.Bool("version", false, "Output the version number")
	showVersionShort := flags.Bool("v", false, "Output the version number (short)")

	gatewayPlatform := flags.String("gateway", "", "Run as IM gateway (telegram, feishu, discord, slack, dingtalk, ...)")
	gatewayConfig := flags.String("gateway-config", "", "Path to gateway config.toml (optional, can use flags instead)")
	gatewayToken := flags.String("gateway-token", "", "Platform bot token (used with --gateway)")
	gatewayAllow := flags.String("gateway-allow", "", "Comma-separated allowed user IDs (used with --gateway)")

	serverMode := flags.Bool("server", false, "Run as web server")
	serverAddr := flags.String("server-addr", ":10112", "Web server listen address")
	serverDataDir := flags.String("server-data-dir", "", "Web server data directory (default: ~/.saker/server)")
	serverStatic := flags.String("server-static", "", "Serve frontend from disk directory instead of embedded")
	serverLogDir := flags.String("server-log-dir", "", "Server log directory (default: <data-dir>/logs)")
	serverAPIOnly := flags.Bool("api-only", false, "Server mode without web UI — disables web/browser tools")
	debugFlag := flags.Bool("debug", false, "Enable /debug/pprof endpoints (use in trusted environments only)")
	authUser := flags.String("auth-user", "", "Set web auth username and save to settings.local.json")
	authPass := flags.String("auth-pass", "", "Set web auth password and save to settings.local.json")

	openaiGwEnabled := flags.Bool("openai-gw-enabled", false, "Enable OpenAI-compatible /v1/* gateway (requires --server)")
	openaiGwMaxRuns := flags.Int("openai-gw-max-runs", 256, "OpenAI gateway: max in-flight runs the hub will track")
	openaiGwMaxRunsPerTenant := flags.Int("openai-gw-max-runs-per-tenant", 32, "OpenAI gateway: per-Bearer-key in-flight run cap (0 disables)")
	openaiGwRPSPerTenant := flags.Int("openai-gw-rps-per-tenant", 10, "OpenAI gateway: per-Bearer-key request rate cap req/s (0 disables)")
	openaiGwRingSize := flags.Int("openai-gw-ring-size", 512, "OpenAI gateway: per-run event ring buffer size for reconnect replay")
	openaiGwExpiresAfterSeconds := flags.Int("openai-gw-expires-after-seconds", 600, "OpenAI gateway: default Run idle/await timeout in seconds")
	openaiGwDevBypass := flags.Bool("openai-gw-dev-bypass", false, "OpenAI gateway: accept requests without a valid Bearer key (DEV ONLY)")
	openaiGwRunHubDSN := flags.String("openai-gw-runhub-dsn", "", "OpenAI gateway: run-hub persistence DSN (empty=in-memory; sqlite path or sqlite://path; postgres://... requires -tags postgres)")
	openaiGwRunHubBatchSize := flags.Int("openai-gw-runhub-batch-size", 64, "OpenAI gateway: persistent hub async writer batch size (events per InsertEventsBatch)")
	openaiGwRunHubBatchBuffer := flags.Int("openai-gw-runhub-batch-buffer", 1024, "OpenAI gateway: persistent hub async writer enqueue chan capacity (drops oldest when full)")
	openaiGwRunHubBatchInterval := flags.Duration("openai-gw-runhub-batch-interval", 50*time.Millisecond, "OpenAI gateway: persistent hub async writer max idle time before flushing a partial batch")
	openaiGwRunHubGCInterval := flags.Duration("openai-gw-runhub-gc-interval", 30*time.Second, "OpenAI gateway: how often the hub sweeper reclaims expired/terminal runs (1s..1h)")
	openaiGwRunHubTerminalRetention := flags.Duration("openai-gw-runhub-terminal-retention", 60*time.Second, "OpenAI gateway: how long terminal runs are retained for reconnect-status reads (1s..24h)")
	openaiGwRunHubMaxEventBytes := flags.Int64("openai-gw-runhub-max-event-bytes", 1*1024*1024, "OpenAI gateway: per-event payload cap; oversized events are rejected (0 = unbounded; not recommended in production)")
	openaiGwRunHubSubscriberIdleTimeout := flags.Duration("openai-gw-runhub-subscriber-idle-timeout", 0, "OpenAI gateway: GC closes per-run SSE subscriber chans idle past this window (0 = disabled; recommend 5-15m once event-rate floor is measured)")
	openaiGwRunHubSinkBreakerThreshold := flags.Int("openai-gw-runhub-sink-breaker-threshold", 10, "OpenAI gateway: persistent-hub sink circuit breaker consecutive failure threshold (0 = disabled)")
	openaiGwRunHubSinkBreakerCooldown := flags.Duration("openai-gw-runhub-sink-breaker-cooldown", 30*time.Second, "OpenAI gateway: persistent-hub sink circuit breaker cooldown before half-open probe (0 with non-zero threshold = latched-open until restart)")
	openaiGwRunHubPGCopyThreshold := flags.Int("openai-gw-runhub-pg-copy-threshold", 50, "OpenAI gateway: postgres-only batch-insert COPY threshold; batches >= this size use pgx.CopyFrom into a TEMP staging table (preserves ON CONFLICT DO NOTHING dedup). 0 disables COPY; ignored on non-postgres drivers.")

	synapseHubAddr := flags.String("synapse-hub-addr", envOr("", "SYNAPSE_HUB_ADDR"), "Synapse hub gRPC address; enables built-in hub registration (requires --server)")
	synapseAuthToken := flags.String("synapse-auth-token", envOr("", "SYNAPSE_BRIDGE_SECRET"), "Shared secret for synapse hub authentication")
	synapseInstanceID := flags.String("synapse-instance-id", envOr("", "SYNAPSE_BRIDGE_INSTANCE", "HOSTNAME"), "Instance ID for synapse registration (default: auto-generated)")
	synapseSandboxID := flags.String("synapse-sandbox-id", envOr("", "SYNAPSE_SANDBOX_ID", "E2B_SANDBOX_ID"), "Sandbox ID for synapse registration")
	synapseModels := flags.String("synapse-models", envOr("saker-default", "SYNAPSE_BRIDGE_MODELS"), "Comma-separated model IDs to advertise to synapse")
	synapseMaxConcurrent := flags.Int("synapse-max-concurrent", envOrInt(8, "SYNAPSE_BRIDGE_MAX_CONCURRENT"), "Max concurrent requests to advertise to synapse hub")
	synapseLabels := flags.String("synapse-labels", envOr("", "SYNAPSE_BRIDGE_LABELS"), "JSON-encoded labels map for synapse registration")
	synapseInsecure := flags.Bool("synapse-insecure", envOrBool(true, "SYNAPSE_HUB_INSECURE"), "Disable TLS to synapse hub (default: true for private networks)")

	profileName := flags.String("profile", "", "Use named profile for isolated settings/memory/history")
	dangerouslySkipPermissions := flags.Bool("dangerously-skip-permissions", false, "Skip all tool permission checks")

	var allowedTools multiValue
	flags.Var(&allowedTools, "allowed-tools", "Comma or space-separated list of tool names to allow")

	var mcpServers multiValue
	flags.Var(&mcpServers, "mcp", "Register an MCP server (repeatable)")
	var skillsDirs multiValue
	flags.Var(&skillsDirs, "skills-dir", "Additional skills directory (repeatable)")

	var tagFlags multiValue
	flags.Var(&tagFlags, "tag", "Attach tag key=value pairs (repeatable)")

	if err := flags.Parse(argv); err != nil {
		return err
	}

	absProjectRoot, _ := filepath.Abs(*projectFlag)
	cliLogDir := filepath.Join(absProjectRoot, ".saker", "logs")
	_, cliLogCleanup, cliLogErr := logging.SetupCLI(cliLogDir)
	if cliLogErr != nil {
		fmt.Fprintf(stderr, "Warning: failed to setup CLI file logging: %v\n", cliLogErr)
	}
	if cliLogCleanup != nil {
		defer cliLogCleanup()
	}

	if *showVersion || *showVersionShort {
		fmt.Fprintln(stdout, a.Version)
		return nil
	}

	if *authPass != "" {
		username := *authUser
		if username == "" {
			username = "admin"
		}
		hash, hashErr := server.HashPassword(*authPass)
		if hashErr != nil {
			return fmt.Errorf("hash password: %w", hashErr)
		}
		projectRoot, _ := filepath.Abs(".")
		existing, _ := config.LoadSettingsLocal(projectRoot)
		if existing == nil {
			existing = &config.Settings{}
		}
		existing.WebAuth = &config.WebAuthConfig{Username: username, Password: hash}
		if saveErr := config.SaveSettingsLocal(projectRoot, existing); saveErr != nil {
			return fmt.Errorf("save auth config: %w", saveErr)
		}
		fmt.Fprintf(stdout, "Web auth saved: username=%s\n", username)
		return nil
	}
	if *authUser != "" {
		return fmt.Errorf("--auth-user requires --auth-pass")
	}

	if flags.NArg() > 0 && flags.Arg(0) == "profile" {
		projectRoot, _ := filepath.Abs(*projectFlag)
		return runProfileCommand(stdout, stderr, projectRoot, flags.Args()[1:])
	}

	if flags.NArg() > 0 && flags.Arg(0) == "skill" {
		projectRoot, _ := filepath.Abs(*projectFlag)
		return skillhub.RunCommand(stdout, stderr, projectRoot, flags.Args()[1:])
	}

	if flags.NArg() > 0 && flags.Arg(0) == "eval" {
		return runEvalCommand(stdout, stderr, flags.Args()[1:])
	}

	if flags.NArg() > 0 && flags.Arg(0) == "openai-key" {
		return project.RunOpenAIKeyCommand(stdout, stderr, flags.Args()[1:])
	}

	if *sessionID == "" && *sessionAlias != "" {
		*sessionID = *sessionAlias
	}
	stream := *printMode || *printShort || *streamAlias
	if *streamFormatAlias != "" && *outputFormat == "" {
		switch strings.ToLower(strings.TrimSpace(*streamFormatAlias)) {
		case "json":
			*outputFormat = "stream-json"
		case "rendered", "human", "pretty":
			*outputFormat = "text"
		default:
			*outputFormat = *streamFormatAlias
		}
	}
	if stream && *outputFormat == "" {
		*outputFormat = "stream-json"
	}
	if *outputFormat == "" {
		*outputFormat = "text"
	}

	if *gvisorHelper {
		return a.runGVisorHelper(context.Background(), os.Stdin, stdout, stderr)
	}
	if *landlockHelper {
		return a.runLandlockHelper(context.Background(), os.Stdin, stdout, stderr)
	}
	if v := strings.TrimSpace(os.Getenv("SAKER_TIMEOUT_MS")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			*timeoutMs = parsed
		}
	}

	_ = godotenv.Overload(filepath.Join(*projectFlag, ".env"))

	provider, resolvedModel := buildModelProvider(*providerName, *modelName, *systemPrompt)
	selectedBackend := strings.ToLower(strings.TrimSpace(*sandboxBackend))
	resolvedSessionID := strings.TrimSpace(*sessionID)
	if selectedBackend != "" && selectedBackend != "host" && resolvedSessionID == "" {
		resolvedSessionID = uuid.NewString()
	}
	settingsPath := ""
	if strings.TrimSpace(*claudeDir) != "" {
		settingsPath = filepath.Join(*claudeDir, "settings.json")
	}
	finalConfigRoot := strings.TrimSpace(*configRoot)
	if finalConfigRoot == "" && strings.TrimSpace(*claudeDir) != "" {
		finalConfigRoot = *claudeDir
	}
	if finalConfigRoot == "" {
		projectRoot, _ := filepath.Abs(*projectFlag)
		pName := strings.TrimSpace(*profileName)
		if pName == "" {
			pName = profile.GetActive(projectRoot)
		}
		if pName != "" && pName != "default" {
			if err := profile.EnsureExists(projectRoot, pName); err != nil {
				return fmt.Errorf("profile %q: %w", pName, err)
			}
			finalConfigRoot = profile.Resolve(projectRoot, pName)
		}
	}
	options := api.Options{
		EntryPoint:                 api.EntryPoint(strings.ToLower(strings.TrimSpace(*entry))),
		ProjectRoot:                *projectFlag,
		ConfigRoot:                 finalConfigRoot,
		SettingsPath:               settingsPath,
		ModelFactory:               provider,
		MCPServers:                 mcpServers,
		DangerouslySkipPermissions: *dangerouslySkipPermissions,
		SkillsDirs:                 append([]string(nil), skillsDirs...),
		SkillsRecursive: func() *bool {
			v := *skillsRecursive
			return &v
		}(),
		EnabledBuiltinTools: splitMultiValue(allowedTools),
		MemoryDir:           ".saker/memory",
	}
	sandboxOpts, err := buildSandboxOptions(*projectFlag, selectedBackend, *sandboxProjectMount, *sandboxImage)
	if err != nil {
		return err
	}
	options.Sandbox = sandboxOpts
	if selectedBackend == "govm" {
		if err := a.ValidateGovmPlatform(); err != nil {
			return err
		}
		if options.Sandbox.Govm == nil {
			return errors.New("govm sandbox configuration is missing")
		}
		if err := a.ValidateGovmRuntime(*options.Sandbox.Govm); err != nil {
			if a.IsGovmNativeUnavailable(err) {
				return fmt.Errorf("govm native runtime unavailable: build with -tags govm_native and ensure bundled native assets are present")
			}
			return fmt.Errorf("govm runtime preflight failed: %w", err)
		}
	}
	if *acpMode {
		return a.serveACPStdio(context.Background(), options, os.Stdin, stdout)
	}

	if *serverAPIOnly {
		options.ModePreset = api.PresetServerAPI
	}

	if *serverMode {
		gw := openaiGatewayFlags{
			Enabled:                     *openaiGwEnabled,
			MaxRuns:                     *openaiGwMaxRuns,
			MaxRunsPerTenant:            *openaiGwMaxRunsPerTenant,
			RPSPerTenant:                *openaiGwRPSPerTenant,
			RingSize:                    *openaiGwRingSize,
			ExpiresAfterSeconds:         *openaiGwExpiresAfterSeconds,
			DevBypassAuth:               *openaiGwDevBypass,
			RunHubDSN:                   *openaiGwRunHubDSN,
			RunHubBatchSize:             *openaiGwRunHubBatchSize,
			RunHubBatchBufferSize:       *openaiGwRunHubBatchBuffer,
			RunHubBatchInterval:         *openaiGwRunHubBatchInterval,
			RunHubGCInterval:            *openaiGwRunHubGCInterval,
			RunHubTerminalRetention:     *openaiGwRunHubTerminalRetention,
			RunHubMaxEventBytes:         *openaiGwRunHubMaxEventBytes,
			RunHubSubscriberIdleTimeout: *openaiGwRunHubSubscriberIdleTimeout,
			RunHubSinkBreakerThreshold:  *openaiGwRunHubSinkBreakerThreshold,
			RunHubSinkBreakerCooldown:   *openaiGwRunHubSinkBreakerCooldown,
			RunHubPGCopyThreshold:       *openaiGwRunHubPGCopyThreshold,
		}
		syn := synapseHubFlags{
			HubAddr:       *synapseHubAddr,
			AuthToken:     *synapseAuthToken,
			InstanceID:    *synapseInstanceID,
			SandboxID:     *synapseSandboxID,
			Models:        *synapseModels,
			MaxConcurrent: *synapseMaxConcurrent,
			Labels:        *synapseLabels,
			Insecure:      *synapseInsecure,
		}
		return a.runServerMode(stdout, stderr, options, *serverAddr, *serverDataDir, *serverStatic, *serverLogDir, *debugFlag, gw, syn)
	}

	channelsPath := ""
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		channelsPath = filepath.Join(home, ".saker", "channels.json")
	}

	if *gatewayPlatform != "" {
		return a.runGatewayMode(stdout, stderr, options, *gatewayPlatform, *gatewayConfig, *gatewayToken, *gatewayAllow, channelsPath)
	}
	recorder := clikitTurnRecorder()
	options.Middleware = append(options.Middleware, clikit.TurnRecorderMiddleware(recorder))
	if *printConfig {
		clikit.PrintEffectiveConfig(stderr, options.ProjectRoot, clikit.EffectiveConfig{
			ModelName:       resolvedModel,
			ConfigRoot:      finalConfigRoot,
			SkillsDirs:      append([]string(nil), skillsDirs...),
			SkillsRecursive: options.SkillsRecursive,
		}, *timeoutMs)
	}

	imCtrl := goim.NewIMController(channelsPath)
	imTool := im.NewIMBridgeTool(imCtrl, options)
	if len(options.EnabledBuiltinTools) == 0 || containsTool(options.EnabledBuiltinTools, "im_config") {
		options.CustomTools = append(options.CustomTools, imTool)
	}

	convStore := openConversationStoreForCLI(options.ProjectRoot, finalConfigRoot, stderr)
	if convStore != nil {
		options.ConversationStore = convStore
		defer convStore.Close() //nolint:errcheck
	}

	runtime, err := a.runtimeFactory(context.Background(), options)
	if err != nil {
		return fmt.Errorf("create runtime: %w", err)
	}
	defer runtime.Close()

	if apiRT, ok := runtime.(*api.Runtime); ok {
		imTool.SetRuntime(apiRT)
	}

	adapter := clikit.NewRuntimeAdapter(runtime, clikit.RuntimeAdapterConfig{
		ProjectRoot:     options.ProjectRoot,
		ConfigRoot:      finalConfigRoot,
		ModelName:       resolvedModel,
		SandboxBackend:  selectedBackend,
		SkillsDirs:      append([]string(nil), skillsDirs...),
		SkillsRecursive: options.SkillsRecursive,
		TurnRecorder:    recorder,
	})
	if *printConfig {
		clikit.PrintRuntimeEffectiveConfig(stderr, adapter, *timeoutMs)
	}

	if *videoStream != "" {
		if *repl || shouldAutoEnterInteractive(*promptLiteral, *promptFile, flags.Args(), stream, *acpMode) {
			return runVideoWithREPL(stdout, stderr, adapter, *videoStream, *segmentDuration, *videoWindowSize, *videoSampleRate, *videoEvents, *timeoutMs, *verbose, *waterfall, resolvedSessionID)
		}
		return runVideoStream(stdout, *videoStream, *segmentDuration, *videoWindowSize, *videoSampleRate, *videoEvents, *timeoutMs)
	}

	if *repl || shouldAutoEnterInteractive(*promptLiteral, *promptFile, flags.Args(), stream, *acpMode) {
		updateCh := versionpkg.CheckForUpdateAsync(a.Version)

		defer imCtrl.Stop() //nolint:errcheck

		var updateInfo *versionpkg.UpdateInfo
		select {
		case info := <-updateCh:
			updateInfo = info
		default:
		}

		if updateInfo != nil && updateInfo.HasUpdate {
			if promptUpgrade(stdout, stderr, updateInfo) {
				return nil
			}
		}
		updateNotice := versionpkg.FormatUpdateNotice(updateInfo)

		useTUI, err := resolveTUIMode(*tuiMode)
		if err != nil {
			return err
		}
		if useTUI {
			return tui.Run(context.Background(), tui.AppConfig{
				Engine:           adapter,
				InitialSessionID: resolvedSessionID,
				TimeoutMs:        *timeoutMs,
				Verbose:          *verbose,
				WaterfallMode:    *waterfall,
				UpdateNotice:     updateNotice,
			})
		}
		clikit.PrintBanner(stdout, adapter.ModelName(), adapter.Skills())
		if updateNotice != "" {
			fmt.Fprintf(stdout, "%s\n\n", updateNotice)
		}
		return clikit.RunInteractiveShellOpts(context.Background(), os.Stdin, stdout, stderr, clikit.InteractiveShellConfig{
			Engine:            adapter,
			InitialSessionID:  resolvedSessionID,
			TimeoutMs:         *timeoutMs,
			Verbose:           *verbose,
			WaterfallMode:     *waterfall,
			ShowStatusPerTurn: true,
		})
	}

	prompt, err := resolvePrompt(*promptLiteral, *promptFile, flags.Args())
	if err != nil {
		return err
	}
	if *pipelineFile == "" && strings.TrimSpace(prompt) == "" {
		return errors.New("prompt is empty")
	}

	ctx := context.Background()
	cancel := func() {}
	if *timeoutMs > 0 {
		ctxWithTimeout, c := context.WithTimeout(ctx, time.Duration(*timeoutMs)*time.Millisecond)
		ctx = ctxWithTimeout
		cancel = c
	}
	defer cancel()

	req := api.Request{
		Prompt:    prompt,
		SessionID: resolvedSessionID,
		Mode: api.ModeContext{
			EntryPoint: options.EntryPoint,
			CLI: &api.CLIContext{
				User:      os.Getenv("USER"),
				Workspace: *projectFlag,
				Args:      argv,
			},
		},
		Tags: parseTags(tagFlags),
	}
	if *pipelineFile != "" {
		step, err := loadPipeline(*pipelineFile)
		if err != nil {
			return fmt.Errorf("load pipeline: %w", err)
		}
		req.Pipeline = &step
		req.Prompt = ""
	}
	if stream {
		switch strings.ToLower(strings.TrimSpace(*outputFormat)) {
		case "stream-json", "json":
			return streamRunJSON(ctx, runtime, req, stdout, stderr, *verbose)
		case "text", "rendered", "human", "pretty":
			return a.runStream(ctx, stdout, stderr, adapter, req.SessionID, req.Prompt, *timeoutMs, *verbose, *waterfall)
		default:
			return fmt.Errorf("unsupported output format %q", *outputFormat)
		}
	}
	resp, err := runtime.Run(ctx, req)
	if err != nil {
		return err
	}
	if req.Pipeline != nil {
		printPipelineResponse(resp, stdout, *showTimeline, *lineageFormat)
	} else {
		printResponse(resp, stdout)
	}
	return nil
}
