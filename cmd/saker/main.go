// Package main is the saker CLI entry point. The bulk of the implementation
// is split across cmd_*.go siblings (cmd_server.go, cmd_gateway.go,
// cmd_video.go, cmd_profile.go, cmd_pipeline.go, cmd_prompt.go,
// cmd_options.go, cmd_helpers.go); this file only holds main(), the run()
// dispatcher, and the package-level vars/types they share.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	acpserver "github.com/cinience/saker/pkg/acp"
	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/clikit"
	"github.com/cinience/saker/pkg/clikit/tui"
	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/im"
	"github.com/cinience/saker/pkg/logging"
	"github.com/cinience/saker/pkg/profile"
	"github.com/cinience/saker/pkg/sandbox/gvisorhelper"
	"github.com/cinience/saker/pkg/sandbox/landlockhelper"
	"github.com/cinience/saker/pkg/server"
	versionpkg "github.com/cinience/saker/pkg/version"
	"github.com/godeps/goim"
	"github.com/google/uuid"
)

var serveACPStdio = acpserver.ServeStdio
var runtimeFactory = func(ctx context.Context, opts api.Options) (runtimeClient, error) {
	return api.New(ctx, opts)
}
var clikitRunStream = clikit.RunStream
var clikitRunInteractiveShell = clikit.RunInteractiveShell
var runGVisorHelper = gvisorhelper.Run
var runLandlockHelper = landlockhelper.Run
var validateGovmPlatform func() error
var validateGovmRuntime func(api.GovmOptions) error

type runtimeClient interface {
	Run(context.Context, api.Request) (*api.Response, error)
	RunStream(context.Context, api.Request) (<-chan api.StreamEvent, error)
	Close() error
}

type streamEngine = clikit.StreamEngine
type replEngine = clikit.ReplEngine

// @title Saker API
// @version 1.0
// @description Saker REST + WebSocket API exposed by the embedded web server.
// @description Localhost requests are auto-elevated to admin; remote callers must authenticate via session cookie or, for app runs, a Bearer API key. Public share-token endpoints under /api/apps/public/ require no authentication.
// @contact.name Saker
// @contact.url https://github.com/cinience/saker
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
func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Version is set at build time via -ldflags.
var Version = "dev"

func run(argv []string, stdout, stderr io.Writer) error {
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
	project := flags.String("project", ".", "Project root")
	claudeDir := flags.String("claude", "", "Optional path to .saker directory")
	configRoot := flags.String("config-root", "", "Optional config root directory (defaults to <project>/.saker)")
	providerName := flags.String("provider", "", "Model provider: anthropic|openai (default: auto-detect from env)")
	modelName := flags.String("model", "", "Model for the current session")
	systemPrompt := flags.String("system-prompt", "", "System prompt to use for the session")

	// --session-id (Claude Code style), with --session as backward-compat alias
	sessionID := flags.String("session-id", "", "Session identifier (UUID)")
	sessionAlias := flags.String("session", "", "Session identifier (alias for --session-id)")

	timeoutMs := flags.Int("timeout-ms", 10*60*1000, "Run timeout in milliseconds")
	printConfig := flags.Bool("print-effective-config", false, "Print resolved runtime config before running")
	promptFile := flags.String("prompt-file", "", "Read prompt from file")
	promptLiteral := flags.String("prompt", "", "Prompt literal (overrides stdin)")

	// -p/--print (Claude Code style), with --stream as backward-compat alias
	printMode := flags.Bool("print", false, "Print response and exit (non-interactive)")
	printShort := flags.Bool("p", false, "Print response and exit (short for --print)")
	streamAlias := flags.Bool("stream", false, "Stream events (alias for --print)")

	// --output-format (Claude Code style), with --stream-format as backward-compat alias
	outputFormat := flags.String("output-format", "text", "Output format: text, json, stream-json (only with --print)")
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

	// IM gateway mode (cc-connect bridge)
	gatewayPlatform := flags.String("gateway", "", "Run as IM gateway (telegram, feishu, discord, slack, dingtalk, ...)")
	gatewayConfig := flags.String("gateway-config", "", "Path to gateway config.toml (optional, can use flags instead)")
	gatewayToken := flags.String("gateway-token", "", "Platform bot token (used with --gateway)")
	gatewayAllow := flags.String("gateway-allow", "", "Comma-separated allowed user IDs (used with --gateway)")

	// Web server mode
	serverMode := flags.Bool("server", false, "Run as web server")
	serverAddr := flags.String("server-addr", ":10112", "Web server listen address")
	serverDataDir := flags.String("server-data-dir", "", "Web server data directory (default: ~/.saker/server)")
	serverStatic := flags.String("server-static", "", "Serve frontend from disk directory instead of embedded")
	serverLogDir := flags.String("server-log-dir", "", "Server log directory (default: <data-dir>/logs)")
	debugFlag := flags.Bool("debug", false, "Enable /debug/pprof endpoints (use in trusted environments only)")
	authUser := flags.String("auth-user", "", "Set web auth username and save to settings.local.json")
	authPass := flags.String("auth-pass", "", "Set web auth password and save to settings.local.json")

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

	// In CLI/TUI mode, redirect slog to a log file only (no stderr)
	// so structured logs don't pollute the terminal output.
	absProjectRoot, _ := filepath.Abs(*project)
	cliLogDir := filepath.Join(absProjectRoot, ".saker", "logs")
	_, cliLogCleanup, cliLogErr := logging.SetupCLI(cliLogDir)
	if cliLogErr != nil {
		fmt.Fprintf(stderr, "Warning: failed to setup CLI file logging: %v\n", cliLogErr)
	}
	if cliLogCleanup != nil {
		defer cliLogCleanup()
	}

	// Handle --version / -v
	if *showVersion || *showVersionShort {
		fmt.Fprintln(stdout, Version)
		return nil
	}

	// Handle --auth-user / --auth-pass: set web auth credentials and exit.
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

	// Handle "profile" subcommand: saker profile <action> [name]
	if flags.NArg() > 0 && flags.Arg(0) == "profile" {
		projectRoot, _ := filepath.Abs(*project)
		return runProfileCommand(stdout, stderr, projectRoot, flags.Args()[1:])
	}

	// Handle "skill" subcommand: saker skill <action> ...
	if flags.NArg() > 0 && flags.Arg(0) == "skill" {
		projectRoot, _ := filepath.Abs(*project)
		return runSkillCommand(stdout, stderr, projectRoot, flags.Args()[1:])
	}

	// Handle "eval" subcommand: saker eval <bench> ...
	if flags.NArg() > 0 && flags.Arg(0) == "eval" {
		return runEvalCommand(stdout, stderr, flags.Args()[1:])
	}

	// Resolve backward-compat aliases
	// --session → --session-id
	if *sessionID == "" && *sessionAlias != "" {
		*sessionID = *sessionAlias
	}
	// --stream → --print
	stream := *printMode || *printShort || *streamAlias
	// --stream-format → --output-format
	if *streamFormatAlias != "" && *outputFormat == "text" {
		// Map old format names to new ones
		switch strings.ToLower(strings.TrimSpace(*streamFormatAlias)) {
		case "json":
			*outputFormat = "stream-json"
		case "rendered", "human", "pretty":
			*outputFormat = "text"
		default:
			*outputFormat = *streamFormatAlias
		}
	}
	// When --print is used without explicit --output-format, default to stream-json
	if stream && *outputFormat == "text" && *streamFormatAlias == "" {
		*outputFormat = "stream-json"
	}

	if *gvisorHelper {
		return runGVisorHelper(context.Background(), os.Stdin, stdout, stderr)
	}
	if *landlockHelper {
		return runLandlockHelper(context.Background(), os.Stdin, stdout, stderr)
	}
	if v := strings.TrimSpace(os.Getenv("SAKER_TIMEOUT_MS")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			*timeoutMs = parsed
		}
	}

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
	// Profile → ConfigRoot resolution (--profile flag or sticky active profile).
	if finalConfigRoot == "" {
		projectRoot, _ := filepath.Abs(*project)
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
		ProjectRoot:                *project,
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
		MemoryDir: ".saker/memory",
	}
	sandboxOpts, err := buildSandboxOptions(*project, selectedBackend, *sandboxProjectMount, *sandboxImage)
	if err != nil {
		return err
	}
	options.Sandbox = sandboxOpts
	if selectedBackend == "govm" {
		if err := validateGovmPlatform(); err != nil {
			return err
		}
		if options.Sandbox.Govm == nil {
			return errors.New("govm sandbox configuration is missing")
		}
		if err := validateGovmRuntime(*options.Sandbox.Govm); err != nil {
			if isGovmNativeUnavailable(err) {
				return fmt.Errorf("govm native runtime unavailable: build with -tags govm_native and ensure bundled native assets are present")
			}
			return fmt.Errorf("govm runtime preflight failed: %w", err)
		}
	}
	if *acpMode {
		return serveACPStdio(context.Background(), options, os.Stdin, stdout)
	}

	if *serverMode {
		return runServerMode(stdout, stderr, options, *serverAddr, *serverDataDir, *serverStatic, *serverLogDir, *debugFlag)
	}

	// Resolve channels path — IM credentials are user-global, not project-specific.
	channelsPath := ""
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		channelsPath = filepath.Join(home, ".saker", "channels.json")
	}

	if *gatewayPlatform != "" {
		return runGatewayMode(stdout, stderr, options, *gatewayPlatform, *gatewayConfig, *gatewayToken, *gatewayAllow, channelsPath)
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

	// Register IM config tool so the LLM can manage IM channel credentials via conversation.
	imCtrl := goim.NewIMController(channelsPath)
	imTool := im.NewIMBridgeTool(imCtrl, options)
	options.CustomTools = append(options.CustomTools, imTool)

	runtime, err := runtimeFactory(context.Background(), options)
	if err != nil {
		return fmt.Errorf("create runtime: %w", err)
	}
	defer runtime.Close()

	// Inject runtime reference now that it's created.
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

	// Handle video stream mode (Phase 2/3)
	if *videoStream != "" {
		// If interactive (terminal), run video in background + REPL in parallel
		if *repl || shouldAutoEnterInteractive(*promptLiteral, *promptFile, flags.Args(), stream, *acpMode) {
			return runVideoWithREPL(stdout, stderr, adapter, *videoStream, *segmentDuration, *videoWindowSize, *videoSampleRate, *videoEvents, *timeoutMs, *verbose, *waterfall, resolvedSessionID)
		}
		// Non-interactive: run video stream only
		return runVideoStream(stdout, *videoStream, *segmentDuration, *videoWindowSize, *videoSampleRate, *videoEvents, *timeoutMs)
	}

	if *repl || shouldAutoEnterInteractive(*promptLiteral, *promptFile, flags.Args(), stream, *acpMode) {
		// Start async version update check.
		updateCh := versionpkg.CheckForUpdateAsync(Version)

		defer imCtrl.Stop() //nolint:errcheck

		// Collect update info (non-blocking).
		var updateInfo *versionpkg.UpdateInfo
		select {
		case info := <-updateCh:
			updateInfo = info
		default:
		}

		// If update available, prompt user before entering interactive mode.
		if updateInfo != nil && updateInfo.HasUpdate {
			if promptUpgrade(stdout, stderr, updateInfo) {
				// User chose to upgrade — on success Restart re-execs and never returns.
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
				Workspace: *project,
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
			return clikitRunStream(ctx, stdout, stderr, adapter, req.SessionID, req.Prompt, *timeoutMs, *verbose, *waterfall)
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
