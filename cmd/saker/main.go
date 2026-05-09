package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	acpserver "github.com/cinience/saker/pkg/acp"
	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/clikit"
	"github.com/cinience/saker/pkg/clikit/tui"
	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/im"
	"github.com/cinience/saker/pkg/logging"
	modelpkg "github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/pipeline"
	"github.com/cinience/saker/pkg/profile"
	"github.com/cinience/saker/pkg/project"
	"github.com/cinience/saker/pkg/provider"
	"github.com/cinience/saker/pkg/sandbox/gvisorhelper"
	"github.com/cinience/saker/pkg/sandbox/landlockenv"
	"github.com/cinience/saker/pkg/sandbox/landlockhelper"
	"github.com/cinience/saker/pkg/server"
	"github.com/cinience/saker/pkg/tool"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
	versionpkg "github.com/cinience/saker/pkg/version"
	"github.com/godeps/goim"
	"github.com/google/uuid"
)

var serveACPStdio = acpserver.ServeStdio
var runtimeFactory = func(ctx context.Context, opts api.Options) (runtimeClient, error) {
	return api.New(ctx, opts)
}
var clikitRunStream = clikit.RunStream
var clikitRunREPL = clikit.RunREPL
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
	tuiMode := flags.Bool("tui", true, "Use bubbletea TUI (set false for legacy readline REPL)")
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

		if *tuiMode {
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

// buildModelProvider delegates to the shared provider.Detect function.
func buildModelProvider(providerFlag, modelFlag, system string) (modelpkg.Provider, string) {
	return provider.Detect(providerFlag, modelFlag, system)
}

func buildSandboxOptions(projectRoot, backend, projectMountMode, offlineImage string) (api.SandboxOptions, error) {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		projectRoot = "."
	}
	absProjectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return api.SandboxOptions{}, fmt.Errorf("resolve project root: %w", err)
	}
	projectRoot = absProjectRoot
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" {
		backend = "host"
	}
	projectMountMode = strings.ToLower(strings.TrimSpace(projectMountMode))
	if projectMountMode == "" {
		projectMountMode = "ro"
	}
	switch projectMountMode {
	case "ro", "rw", "off":
	default:
		return api.SandboxOptions{}, fmt.Errorf("invalid --sandbox-project-mount %q (expected ro|rw|off)", projectMountMode)
	}

	switch backend {
	case "host":
		return api.SandboxOptions{}, nil
	case "gvisor":
		opts := api.SandboxOptions{
			Type: "gvisor",
			GVisor: &api.GVisorOptions{
				Enabled:                    true,
				DefaultGuestCwd:            "/workspace",
				AutoCreateSessionWorkspace: true,
				SessionWorkspaceBase:       filepath.Join(projectRoot, "workspace"),
			},
		}
		if projectMountMode != "off" {
			opts.GVisor.Mounts = append(opts.GVisor.Mounts, api.MountSpec{
				HostPath:  projectRoot,
				GuestPath: "/project",
				ReadOnly:  projectMountMode != "rw",
			})
		}
		return opts, nil
	case "govm":
		if strings.TrimSpace(offlineImage) == "" {
			offlineImage = "py312-alpine"
		}
		opts := api.SandboxOptions{
			Type: "govm",
			Govm: &api.GovmOptions{
				Enabled:                    true,
				DefaultGuestCwd:            "/workspace",
				AutoCreateSessionWorkspace: true,
				SessionWorkspaceBase:       filepath.Join(projectRoot, "workspace"),
				RuntimeHome:                filepath.Join(projectRoot, ".govm"),
				OfflineImage:               offlineImage,
			},
		}
		if projectMountMode != "off" {
			opts.Govm.Mounts = append(opts.Govm.Mounts, api.MountSpec{
				HostPath:  projectRoot,
				GuestPath: "/project",
				ReadOnly:  projectMountMode != "rw",
			})
		}
		return opts, nil
	case "landlock":
		opts := api.SandboxOptions{
			Type: "landlock",
			Landlock: &api.LandlockOptions{
				Enabled:                    true,
				DefaultGuestCwd:            projectRoot,
				AutoCreateSessionWorkspace: true,
				SessionWorkspaceBase:       filepath.Join(projectRoot, "workspace"),
			},
		}
		return opts, nil
	default:
		return api.SandboxOptions{}, fmt.Errorf("invalid --sandbox-backend %q (expected host|gvisor|govm|landlock)", backend)
	}
}

func resolvePrompt(literal, file string, tail []string) (string, error) {
	if strings.TrimSpace(literal) != "" {
		return literal, nil
	}
	if strings.TrimSpace(file) != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read prompt file: %w", err)
		}
		return string(data), nil
	}
	if len(tail) > 0 {
		return strings.Join(tail, " "), nil
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", errors.New("no prompt provided")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

func shouldAutoEnterInteractive(literal, file string, tail []string, printMode, acp bool) bool {
	if acp || printMode {
		return false
	}
	if strings.TrimSpace(literal) != "" || strings.TrimSpace(file) != "" || len(tail) > 0 {
		return false
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func printResponse(resp *api.Response, out io.Writer) {
	if resp == nil || out == nil {
		return
	}
	fmt.Fprintf(out, "# saker run (%s)\n", resp.Mode.EntryPoint)
	if resp.Result != nil {
		fmt.Fprintf(out, "stop_reason: %s\n", resp.Result.StopReason)
		fmt.Fprintf(out, "output:\n%s\n", resp.Result.Output)
	}
}

func streamRunJSON(ctx context.Context, rt runtimeClient, req api.Request, out, errOut io.Writer, verbose bool) error {
	ch, err := rt.RunStream(ctx, req)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	for evt := range ch {
		if verbose && errOut != nil {
			switch evt.Type {
			case api.EventToolExecutionResult, api.EventMessageStop, api.EventError:
				_, _ = fmt.Fprintf(errOut, "[event] %s\n", evt.Type)
			}
		}
		if err := encoder.Encode(evt); err != nil {
			return err
		}
	}
	return nil
}

type multiValue []string

func (m *multiValue) String() string {
	return strings.Join(*m, ",")
}

func (m *multiValue) Set(value string) error {
	*m = append(*m, value)
	return nil
}

// promptUpgrade asks the user whether to upgrade and performs the upgrade if accepted.
// Returns true if the upgrade was performed (caller should exit), false otherwise.
func promptUpgrade(stdout, stderr io.Writer, info *versionpkg.UpdateInfo) bool {
	fmt.Fprintf(stdout, "\nUpdate available: v%s -> v%s\n", info.Current, info.Latest)
	if info.Message != "" {
		fmt.Fprintf(stdout, "  %s\n", info.Message)
	}
	fmt.Fprintf(stdout, "\nUpgrade now? [Y/n/r(release notes)] ")

	reader := bufio.NewReader(os.Stdin)
	for {
		line, _ := reader.ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))

		switch answer {
		case "", "y", "yes":
			fmt.Fprintln(stdout)
			if err := versionpkg.SelfUpgrade(info.Latest, func(msg string) {
				fmt.Fprintf(stdout, "  %s\n", msg)
			}); err != nil {
				fmt.Fprintf(stderr, "Upgrade failed: %v\n", err)
				fmt.Fprintln(stdout, "Continuing with current version...")
				return false
			}
			fmt.Fprintf(stdout, "\n  Successfully upgraded to v%s. Restarting...\n\n", info.Latest)
			if err := versionpkg.Restart(); err != nil {
				fmt.Fprintf(stderr, "Restart failed: %v\n", err)
				fmt.Fprintln(stdout, "Please restart saker manually.")
			}
			return true
		case "n", "no":
			fmt.Fprintln(stdout)
			return false
		case "r", "release":
			if info.ReleaseURL != "" {
				fmt.Fprintf(stdout, "  Release notes: %s/tag/v%s\n", info.ReleaseURL, info.Latest)
			} else {
				fmt.Fprintln(stdout, "  No release URL available.")
			}
			fmt.Fprintf(stdout, "\nUpgrade now? [Y/n] ")
		default:
			fmt.Fprintf(stdout, "  Please enter Y, n, or r: ")
		}
	}
}

func parseTags(values multiValue) map[string]string {
	if len(values) == 0 {
		return nil
	}
	tags := map[string]string{}
	for _, value := range values {
		parts := strings.SplitN(value, "=", 2)
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		val := "true"
		if len(parts) == 2 {
			val = strings.TrimSpace(parts[1])
		}
		tags[key] = val
	}
	return tags
}

func clikitTurnRecorder() *clikit.TurnRecorder {
	return clikit.NewTurnRecorder()
}

func loadPipeline(path string) (pipeline.Step, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pipeline.Step{}, err
	}
	var step pipeline.Step
	if err := json.Unmarshal(data, &step); err != nil {
		return pipeline.Step{}, fmt.Errorf("parse pipeline JSON: %w", err)
	}
	return step, nil
}

func runVideoStream(out io.Writer, source string, segDuration time.Duration, windowSize, sampleRate int, events string, timeoutMs int) error {
	ctx := context.Background()
	cancel := func() {}
	if timeoutMs > 0 {
		ctxT, c := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		ctx = ctxT
		cancel = c
	}
	defer cancel()

	// Build source
	src := buildStreamSource(out, source, segDuration, sampleRate)
	defer src.Close()

	// Build tool runner that uses builtin tools directly
	runTool := buildToolRunner(nil) // no model in non-interactive mode

	// Choose mode: frame processor (with events) or stream executor
	if strings.TrimSpace(events) != "" {
		// Phase 3: frame-level event detection
		var rules []pipeline.EventRule
		for _, kw := range strings.Split(events, ",") {
			kw = strings.TrimSpace(kw)
			if kw != "" {
				rules = append(rules, pipeline.NewKeywordEventRule(kw+"_detected", kw, 3*time.Second))
			}
		}
		fp := &pipeline.FrameProcessor{
			Executor: pipeline.Executor{RunTool: runTool},
			Config: pipeline.FrameProcessorConfig{
				Step:          pipeline.Step{Name: "analyze-frame", Tool: "frame_analyzer"},
				SampleRate:    sampleRate,
				ContextWindow: windowSize,
				EventRules:    rules,
				FrameInterval: segDuration,
				OnEvent: func(ev pipeline.Event) {
					fmt.Fprintf(out, "  ** EVENT [%s] frame %d: %s\n", ev.Type, ev.Frame, ev.Detail)
				},
			},
		}
		results := fp.Run(ctx, src)
		processed, skipped, evCount := 0, 0, 0
		for r := range results {
			if r.Skipped {
				skipped++
				continue
			}
			processed++
			evCount += len(r.Events)
			marker := " "
			if len(r.Events) > 0 {
				marker = "!"
			}
			fmt.Fprintf(out, "%s frame %3d: %s\n", marker, r.FrameIndex, r.Analysis)
		}
		fmt.Fprintf(out, "\nDone: %d processed, %d skipped, %d events\n", processed, skipped, evCount)
	} else {
		// Phase 2: stream executor
		se := &pipeline.StreamExecutor{
			Executor: pipeline.Executor{RunTool: runTool},
			Config: pipeline.StreamExecutorConfig{
				Step:            pipeline.Step{Name: "analyze-segment", Tool: "frame_analyzer"},
				WindowSize:      windowSize,
				BufferSize:      16,
				SegmentInterval: segDuration,
			},
		}
		results := se.Run(ctx, src)
		count := 0
		for r := range results {
			count++
			if r.Dropped {
				fmt.Fprintf(out, "[dropped] segment %d\n", r.SegmentIndex)
				continue
			}
			fmt.Fprintf(out, "[%s] segment %d: %s\n", fmtStreamDuration(r.Timestamp), r.SegmentIndex, r.Result.Output)
		}
		fmt.Fprintf(out, "\nStream ended: %d segments processed\n", count)
	}
	return nil
}

func buildStreamSource(out io.Writer, source string, segDuration time.Duration, sampleRate int) pipeline.StreamSource {
	switch {
	case strings.HasPrefix(source, "watch:"):
		dir := strings.TrimPrefix(source, "watch:")
		fmt.Fprintf(out, "Watching directory: %s\n", dir)
		return pipeline.NewDirectoryWatchSource(dir, 500*time.Millisecond)
	case pipeline.IsStreamScheme(source):
		fmt.Fprintf(out, "Streaming via go2rtc: %s (sample rate: 1/%d)\n", source, sampleRate)
		return pipeline.NewGo2RTCStreamSource(source, pipeline.Go2RTCSourceOptions{
			SampleRate: sampleRate,
		})
	default:
		fmt.Fprintf(out, "Streaming from file: %s (segment: %s)\n", source, segDuration)
		return pipeline.NewFileStreamSource(source, segDuration)
	}
}

func fmtStreamDuration(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// videoController manages a background video stream processor.
type videoController struct {
	cancel    context.CancelFunc
	done      chan struct{}
	processed int
	skipped   int
	events    int
	mu        sync.Mutex
}

func (vc *videoController) stop() {
	vc.cancel()
	<-vc.done
}

func (vc *videoController) stats() (processed, skipped, events int) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	return vc.processed, vc.skipped, vc.events
}

func (vc *videoController) incProcessed() { vc.mu.Lock(); vc.processed++; vc.mu.Unlock() }
func (vc *videoController) incSkipped()   { vc.mu.Lock(); vc.skipped++; vc.mu.Unlock() }
func (vc *videoController) addEvents(n int) {
	vc.mu.Lock()
	vc.events += n
	vc.mu.Unlock()
}

// runVideoWithREPL starts video stream processing in background and enters
// the interactive REPL. The user can chat with the agent while events are
// detected and displayed inline. Use /video-status and /video-stop commands.
func runVideoWithREPL(stdout, stderr io.Writer, adapter clikit.ReplEngine,
	source string, segDuration time.Duration, windowSize, sampleRate int,
	events string, timeoutMs int, verbose bool, waterfall, sessionID string) error {

	ctx, cancel := context.WithCancel(context.Background())
	vc := &videoController{
		cancel: cancel,
		done:   make(chan struct{}),
	}

	// Build source
	src := buildStreamSource(stdout, source, segDuration, sampleRate)

	// Build tool runner that uses builtin tools directly
	runTool := buildToolRunner(nil) // model not available in pipeline mode

	// Start background video processing
	go func() {
		defer close(vc.done)
		defer src.Close()

		if strings.TrimSpace(events) != "" {
			// Frame processor with event detection
			var rules []pipeline.EventRule
			for _, kw := range strings.Split(events, ",") {
				kw = strings.TrimSpace(kw)
				if kw != "" {
					rules = append(rules, pipeline.NewKeywordEventRule(kw+"_detected", kw, 3*time.Second))
				}
			}
			fp := &pipeline.FrameProcessor{
				Executor: pipeline.Executor{RunTool: runTool},
				Config: pipeline.FrameProcessorConfig{
					Step:          pipeline.Step{Name: "analyze-frame", Tool: "frame_analyzer"},
					SampleRate:    sampleRate,
					ContextWindow: windowSize,
					EventRules:    rules,
					FrameInterval: segDuration,
					OnEvent: func(ev pipeline.Event) {
						fmt.Fprintf(stdout, "\n  ** VIDEO EVENT [%s] frame %d: %s **\n> ", ev.Type, ev.Frame, ev.Detail)
					},
				},
			}
			results := fp.Run(ctx, src)
			for r := range results {
				if r.Skipped {
					vc.incSkipped()
					continue
				}
				vc.incProcessed()
				vc.addEvents(len(r.Events))
			}
		} else {
			// Stream executor
			se := &pipeline.StreamExecutor{
				Executor: pipeline.Executor{RunTool: runTool},
				Config: pipeline.StreamExecutorConfig{
					Step:            pipeline.Step{Name: "analyze-segment", Tool: "frame_analyzer"},
					WindowSize:      windowSize,
					BufferSize:      16,
					SegmentInterval: segDuration,
				},
			}
			results := se.Run(ctx, src)
			for r := range results {
				vc.incProcessed()
				if r.Dropped {
					vc.incSkipped()
				}
			}
		}
	}()

	// Build custom commands for video control
	customCmds := func(input string, out io.Writer) (bool, bool) {
		fields := strings.Fields(input)
		if len(fields) == 0 {
			return false, false
		}
		cmd := strings.ToLower(fields[0])
		switch cmd {
		case "/video-status":
			p, s, e := vc.stats()
			select {
			case <-vc.done:
				fmt.Fprintf(out, "Video stream: stopped (processed: %d, skipped: %d, events: %d)\n", p, s, e)
			default:
				fmt.Fprintf(out, "Video stream: running (processed: %d, skipped: %d, events: %d)\n", p, s, e)
			}
			return true, false
		case "/video-stop":
			select {
			case <-vc.done:
				fmt.Fprintln(out, "Video stream already stopped.")
			default:
				vc.stop()
				p, s, e := vc.stats()
				fmt.Fprintf(out, "Video stream stopped. (processed: %d, skipped: %d, events: %d)\n", p, s, e)
			}
			return true, false
		}
		return false, false
	}

	// Print banner and enter REPL
	clikit.PrintBanner(stdout, adapter.ModelName(), adapter.Skills())

	bannerExtra := fmt.Sprintf("Video stream active: %s\nCommands: /video-status /video-stop\n", source)

	err := clikit.RunInteractiveShellOpts(context.Background(), os.Stdin, stdout, stderr, clikit.InteractiveShellConfig{
		Engine:            adapter,
		InitialSessionID:  sessionID,
		TimeoutMs:         timeoutMs,
		Verbose:           verbose,
		WaterfallMode:     waterfall,
		ShowStatusPerTurn: true,
		CustomCommands:    customCmds,
		BannerExtra:       bannerExtra,
	})

	// Stop video on REPL exit
	select {
	case <-vc.done:
	default:
		vc.stop()
	}

	p, s, e := vc.stats()
	fmt.Fprintf(stdout, "Video stream ended: %d processed, %d skipped, %d events\n", p, s, e)
	return err
}

// buildToolRunner creates a RunTool function that dispatches to builtin tools.
// If mdl is non-nil, model-aware tools (frame_analyzer, video_summarizer) are available.
func buildToolRunner(mdl modelpkg.Model) func(context.Context, pipeline.Step, []artifact.ArtifactRef) (*tool.ToolResult, error) {
	tools := map[string]tool.Tool{
		"video_sampler":  toolbuiltin.NewVideoSamplerTool(),
		"stream_capture": toolbuiltin.NewStreamCaptureTool(),
	}
	if mdl != nil {
		tools["frame_analyzer"] = toolbuiltin.NewFrameAnalyzerTool(mdl)
		tools["video_summarizer"] = toolbuiltin.NewVideoSummarizerTool(mdl)
		tools["analyze_video"] = toolbuiltin.NewAnalyzeVideoTool(mdl)
	}
	return func(ctx context.Context, step pipeline.Step, refs []artifact.ArtifactRef) (*tool.ToolResult, error) {
		t, ok := tools[step.Tool]
		if !ok {
			return nil, fmt.Errorf("unknown tool %q", step.Tool)
		}
		params := make(map[string]any)
		for k, v := range step.With {
			params[k] = v
		}
		if len(refs) > 0 {
			params["artifacts"] = refs
		}
		return t.Execute(ctx, params)
	}
}

func printPipelineResponse(resp *api.Response, out io.Writer, showTimeline bool, lineageFormat string) {
	if resp == nil || resp.Result == nil || out == nil {
		return
	}
	r := resp.Result

	fmt.Fprintf(out, "=== PIPELINE RESULT ===\n")
	if r.Output != "" {
		fmt.Fprintf(out, "output: %s\n", r.Output)
	}
	if r.StopReason != "" {
		fmt.Fprintf(out, "stop_reason: %s\n", r.StopReason)
	}
	if len(r.Artifacts) > 0 {
		fmt.Fprintf(out, "artifacts: %d\n", len(r.Artifacts))
		for _, a := range r.Artifacts {
			fmt.Fprintf(out, "  [%s] %s (%s)\n", a.Kind, a.ArtifactID, a.Source)
		}
	}
	fmt.Fprintln(out)

	if showTimeline && len(resp.Timeline) > 0 {
		fmt.Fprintf(out, "=== TIMELINE (%d events) ===\n", len(resp.Timeline))
		for _, e := range resp.Timeline {
			switch e.Kind {
			case api.TimelineToolCall:
				fmt.Fprintf(out, "  %-20s %s\n", e.Kind, e.Name)
			case api.TimelineToolResult:
				fmt.Fprintf(out, "  %-20s %-20s %s\n", e.Kind, e.Name, e.Output)
			case api.TimelineLatencySnapshot:
				fmt.Fprintf(out, "  %-20s %-20s %v\n", e.Kind, e.Name, e.Duration)
			case api.TimelineCacheHit, api.TimelineCacheMiss:
				fmt.Fprintf(out, "  %-20s %s\n", e.Kind, e.CacheKey)
			case api.TimelineInputArtifact, api.TimelineGeneratedArtifact:
				id := ""
				if e.Artifact != nil {
					id = e.Artifact.ArtifactID
				}
				fmt.Fprintf(out, "  %-20s %-20s %s\n", e.Kind, e.Name, id)
			default:
				fmt.Fprintf(out, "  %-20s %s\n", e.Kind, e.Name)
			}
		}
		fmt.Fprintln(out)
	}

	if strings.EqualFold(lineageFormat, "dot") && len(r.Lineage.Edges) > 0 {
		fmt.Fprintf(out, "=== LINEAGE ===\n")
		fmt.Fprint(out, r.Lineage.ToDOT())
	}
}

// runGatewayMode starts the IM gateway: creates a Runtime, wraps it in a
// cc-connect Agent adapter, creates Platform(s), and runs the Engine.
func runServerMode(stdout, stderr io.Writer, opts api.Options, addr, dataDir, staticDir, logDir string, debug bool) error {
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
		return err
	case <-sigCh:
		fmt.Fprintln(stdout, "\nShutting down...")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		return srv.Shutdown(shutCtx)
	}
}

func runGatewayMode(stdout, stderr io.Writer, opts api.Options, platform, configPath, token, allowFrom, channelsPath string) error {
	// Load or build config.
	var cfg goim.Config
	var err error
	switch {
	case configPath != "":
		cfg, err = goim.LoadConfig(configPath)
		if err != nil {
			return fmt.Errorf("gateway config: %w", err)
		}
	case token != "":
		cfg = goim.ConfigFromFlags(platform, token, allowFrom)
	default:
		// Fallback: try GATEWAY_TOKEN env, then channels.json.
		if envToken := os.Getenv("GATEWAY_TOKEN"); envToken != "" {
			cfg = goim.ConfigFromFlags(platform, envToken, allowFrom)
		} else if channelsPath != "" {
			chCfg, loadErr := goim.LoadChannelsJSON(channelsPath)
			if loadErr != nil {
				return fmt.Errorf("load channels.json: %w", loadErr)
			}
			if platform != "" {
				// Single platform requested — look up its saved config.
				savedOpts := chCfg.LookupChannel(platform)
				if savedOpts == nil {
					return fmt.Errorf("no saved config for platform %q in %s; provide --gateway-token", platform, channelsPath)
				}
				cfg = chCfg.ToConfig()
				pOpts := make(map[string]any, len(savedOpts))
				for k, v := range savedOpts {
					if k != "enabled" {
						pOpts[k] = v
					}
				}
				cfg.Project.Platforms = []goim.PlatformConfig{{Type: platform, Options: pOpts}}
			} else {
				// No platform specified — load all enabled channels.
				cfg = chCfg.ToConfig()
				if len(cfg.Project.Platforms) == 0 {
					return fmt.Errorf("no enabled channels in %s; provide --gateway-token or configure channels", channelsPath)
				}
			}
		} else {
			return fmt.Errorf("--gateway-token, GATEWAY_TOKEN, or channels.json is required when using --gateway without --gateway-config")
		}
	}

	// Create runtime.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rt, err := runtimeFactory(ctx, opts)
	if err != nil {
		return fmt.Errorf("create runtime: %w", err)
	}
	defer rt.Close()

	// Create agent adapter via goim with the bridge adapter.
	rtAdapter := im.NewRuntimeAdapter(rt.(*api.Runtime))
	agent := goim.NewAgent(rtAdapter, "saker")

	// Create platforms from config.
	platforms, err := goim.CreatePlatforms(cfg)
	if err != nil {
		return fmt.Errorf("create platforms: %w", err)
	}

	// Setup IM log file.
	logCleanup, logErr := goim.SetupIMLogger()
	if logErr != nil {
		fmt.Fprintf(stderr, "warning: im log setup: %v\n", logErr)
	}
	if logCleanup != nil {
		defer logCleanup()
	}

	// Create and start engine.
	engine := goim.NewEngine(agent, platforms, cfg)

	displayPlatform := platform
	if displayPlatform == "" && len(cfg.Project.Platforms) > 0 {
		names := make([]string, len(cfg.Project.Platforms))
		for i, p := range cfg.Project.Platforms {
			names[i] = p.Type
		}
		displayPlatform = strings.Join(names, ", ")
	}
	fmt.Fprintf(stdout, "saker gateway: starting IM bridge (%s)\n", displayPlatform)
	if err := engine.Start(); err != nil {
		return fmt.Errorf("start engine: %w", err)
	}

	// Block until interrupted.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	fmt.Fprintln(stdout, "\nsaker gateway: shutting down...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	done := make(chan error, 1)
	go func() { done <- engine.Stop() }()
	select {
	case err := <-done:
		return err
	case <-shutCtx.Done():
		return fmt.Errorf("gateway shutdown timed out")
	}
}

// runProfileCommand handles "saker profile <action> [name]" subcommands.
func runProfileCommand(stdout, stderr io.Writer, projectRoot string, args []string) error {
	if len(args) == 0 {
		// "saker profile" with no args → show active profile.
		active := profile.GetActive(projectRoot)
		if active == "" {
			active = "default"
		}
		fmt.Fprintf(stdout, "Active profile: %s\n", active)
		fmt.Fprintf(stdout, "Profile dir: %s\n", profile.Dir(projectRoot, active))
		return nil
	}

	action := args[0]
	switch action {
	case "list":
		profiles, err := profile.List(projectRoot)
		if err != nil {
			return fmt.Errorf("profile list: %w", err)
		}
		for _, p := range profiles {
			marker := "  "
			if p.IsDefault {
				marker = "* "
			} else if p.Name == profile.GetActive(projectRoot) {
				marker = "* "
			}
			model := ""
			if p.Model != "" {
				model = " (model: " + p.Model + ")"
			}
			fmt.Fprintf(stdout, "%s%s%s\n", marker, p.Name, model)
		}
		return nil

	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: saker profile create <name> [--clone <source>]")
		}
		name := args[1]
		opts := profile.CreateOptions{}
		for i := 2; i < len(args); i++ {
			if args[i] == "--clone" && i+1 < len(args) {
				opts.CloneFrom = args[i+1]
				i++
			}
		}
		if err := profile.Create(projectRoot, name, opts); err != nil {
			return fmt.Errorf("profile create: %w", err)
		}
		fmt.Fprintf(stdout, "Created profile: %s\n", name)
		fmt.Fprintf(stdout, "  Path: %s\n", profile.Dir(projectRoot, name))
		return nil

	case "use":
		if len(args) < 2 {
			return fmt.Errorf("usage: saker profile use <name>")
		}
		name := args[1]
		if name != "default" && !profile.Exists(projectRoot, name) {
			return fmt.Errorf("profile %q does not exist (use 'saker profile create %s' first)", name, name)
		}
		if err := profile.SetActive(projectRoot, name); err != nil {
			return fmt.Errorf("profile use: %w", err)
		}
		if name == "default" {
			fmt.Fprintln(stdout, "Switched to default profile")
		} else {
			fmt.Fprintf(stdout, "Switched to profile: %s\n", name)
		}
		return nil

	case "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: saker profile delete <name>")
		}
		name := args[1]
		if err := profile.Delete(projectRoot, name); err != nil {
			return fmt.Errorf("profile delete: %w", err)
		}
		fmt.Fprintf(stdout, "Deleted profile: %s\n", name)
		return nil

	case "show":
		name := ""
		if len(args) >= 2 {
			name = args[1]
		} else {
			name = profile.GetActive(projectRoot)
		}
		if name == "" {
			name = "default"
		}
		fmt.Fprintf(stdout, "Profile: %s\n", name)
		fmt.Fprintf(stdout, "  Path: %s\n", profile.Dir(projectRoot, name))
		fmt.Fprintf(stdout, "  Exists: %v\n", profile.Exists(projectRoot, name))
		return nil

	default:
		return fmt.Errorf("unknown profile action: %s (use list, create, use, delete, show)", action)
	}
}
