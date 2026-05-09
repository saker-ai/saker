package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/runtime/commands"
	"github.com/cinience/saker/pkg/runtime/skills"
	"github.com/cinience/saker/pkg/sandbox"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/tool"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
	aigotools "github.com/cinience/saker/pkg/tool/builtin/aigo"
)

func registerTools(registry *tool.Registry, opts Options, settings *config.Settings, skReg *skills.Registry, cmdExec *commands.Executor, execEnvOpt ...sandboxenv.ExecutionEnvironment) (*registeredToolRefs, error) {
	entry := effectiveEntryPoint(opts)
	tools := opts.Tools
	refs := &registeredToolRefs{}
	var aigoToolNames map[string]struct{}
	var execEnv sandboxenv.ExecutionEnvironment
	if len(execEnvOpt) > 0 {
		execEnv = execEnvOpt[0]
	}

	if len(tools) == 0 {
		sandboxDisabled := settings != nil && settings.Sandbox != nil && settings.Sandbox.Enabled != nil && !*settings.Sandbox.Enabled
		if skReg == nil {
			skReg = skills.NewRegistry()
		}
		if cmdExec == nil {
			cmdExec = commands.NewExecutor()
		}

		// Resolve aigo config early so builtinToolFactories can use it for ASR.
		// If settings has an aigo section but routing is empty, merge env-based
		// defaults so that DASHSCOPE_API_KEY etc. still work.
		aigoCfg := func() *config.AigoConfig {
			if settings != nil && settings.Aigo != nil {
				cfg := settings.Aigo
				if len(cfg.Routing) == 0 {
					envCfg := aigotools.DefaultConfigFromEnv()
					if envCfg != nil {
						cfg.Routing = envCfg.Routing
						for k, v := range envCfg.Providers {
							if _, exists := cfg.Providers[k]; !exists {
								if cfg.Providers == nil {
									cfg.Providers = map[string]config.AigoProvider{}
								}
								cfg.Providers[k] = v
							}
						}
					}
				}
				return cfg
			}
			return aigotools.DefaultConfigFromEnv()
		}()

		factories := builtinToolFactories(opts.ProjectRoot, sandboxDisabled, entry, settings, skReg, cmdExec, opts.TaskStore, opts.Model, opts.ContextWindowTokens, aigoCfg, opts.CanvasDir, execEnv)
		names := builtinOrder(entry)
		selectedNames := filterBuiltinNames(opts.EnabledBuiltinTools, names)
		for _, name := range selectedNames {
			ctor := factories[name]
			if ctor == nil {
				continue
			}
			impl := ctor()
			if impl == nil {
				continue
			}
			if t, ok := impl.(*toolbuiltin.TaskTool); ok {
				refs.taskTool = t
			}
			if t, ok := impl.(*toolbuiltin.StreamMonitorTool); ok {
				refs.streamMonitor = t
			}
			tools = append(tools, impl)
		}

		if len(opts.CustomTools) > 0 {
			tools = append(tools, opts.CustomTools...)
		}

		// Auto-register aigo tools.
		if aigoCfg != nil {
			aigoTools, err := aigotools.NewToolsFromConfig(aigoCfg, aigotools.WithDataDir(filepath.Join(opts.ProjectRoot, ".saker")))
			if err != nil {
				slog.Warn("aigo tools registration warning", "error", err)
			} else if len(aigoTools) > 0 {
				names := make([]string, len(aigoTools))
				for i, t := range aigoTools {
					names[i] = t.Name()
				}
				slog.Info("aigo tools registered", "names", names)
				aigoToolNames = make(map[string]struct{}, len(names))
				for _, n := range names {
					aigoToolNames[n] = struct{}{}
				}
				tools = append(tools, aigoTools...)
			}
		} else {
			slog.Info("aigo tools: no config found (set DASHSCOPE_API_KEY or configure aigo in settings.json)")
		}
	} else {
		refs.taskTool = locateTaskTool(tools)
	}

	disallowed := toLowerSet(opts.DisallowedTools)
	if settings != nil && len(settings.DisallowedTools) > 0 {
		if disallowed == nil {
			disallowed = map[string]struct{}{}
		}
		for _, name := range settings.DisallowedTools {
			if key := canonicalToolName(name); key != "" {
				disallowed[key] = struct{}{}
			}
		}
		if len(disallowed) == 0 {
			disallowed = nil
		}
	}

	filtered := make([]tool.Tool, 0, len(tools))
	seen := make(map[string]int)
	for _, impl := range tools {
		if impl == nil {
			continue
		}
		name := strings.TrimSpace(impl.Name())
		if name == "" {
			continue
		}
		canon := canonicalToolName(name)
		if disallowed != nil {
			if _, blocked := disallowed[canon]; blocked {
				slog.Info("tool skipped: disallowed", "name", name)
				continue
			}
		}
		if idx, ok := seen[canon]; ok {
			slog.Warn("tool overrides previous duplicate", "name", name)
			filtered[idx] = impl
			continue
		}
		seen[canon] = len(filtered)
		filtered = append(filtered, impl)
	}

	for _, impl := range filtered {
		var err error
		if _, isAigo := aigoToolNames[impl.Name()]; isAigo {
			err = registry.RegisterWithSource(impl, "aigo")
		} else {
			err = registry.Register(impl)
		}
		if err != nil {
			return nil, fmt.Errorf("api: register tool %s: %w", impl.Name(), err)
		}
	}

	if refs.taskTool == nil {
		refs.taskTool = locateTaskTool(filtered)
	}
	if refs.streamMonitor == nil {
		for _, impl := range filtered {
			if t, ok := impl.(*toolbuiltin.StreamMonitorTool); ok {
				refs.streamMonitor = t
				break
			}
		}
	}
	return refs, nil
}

func builtinOrder(entry EntryPoint) []string {
	order := []string{
		"bash",
		"file_read",
		"image_read",
		"canvas_get_node",
		"canvas_list_nodes",
		"canvas_table_write",
		"file_write",
		"file_edit",
		"web_fetch",
		"web_search",
		"bash_output",
		"bash_status",
		"kill_task",
		"task_create",
		"task_list",
		"task_get",
		"task_update",
		"ask_user_question",
		"skill",
		"slash_command",
		"grep",
		"glob",
		"video_sampler",
		"stream_capture",
		"browser",
		"stream_monitor",
		"webhook",
		"media_index",
		"media_search",
		"frame_analyzer",
		"video_summarizer",
		"analyze_video",
	}
	if shouldRegisterTaskTool(entry) {
		order = append(order, "task")
	}
	return order
}

func filterBuiltinNames(enabled []string, order []string) []string {
	if enabled == nil {
		return append([]string(nil), order...)
	}
	if len(enabled) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(enabled))
	repl := strings.NewReplacer("-", "_", " ", "_")
	for _, name := range enabled {
		key := strings.ToLower(strings.TrimSpace(name))
		key = repl.Replace(key)
		if key != "" {
			set[key] = struct{}{}
		}
	}
	var filtered []string
	for _, name := range order {
		if _, ok := set[name]; ok {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

func shouldRegisterTaskTool(entry EntryPoint) bool {
	switch entry {
	case EntryPointCLI, EntryPointPlatform:
		return true
	default:
		return false
	}
}

func locateTaskTool(tools []tool.Tool) *toolbuiltin.TaskTool {
	for _, impl := range tools {
		if impl == nil {
			continue
		}
		if task, ok := impl.(*toolbuiltin.TaskTool); ok {
			return task
		}
	}
	return nil
}

func effectiveEntryPoint(opts Options) EntryPoint {
	entry := opts.EntryPoint
	if entry == "" {
		entry = opts.Mode.EntryPoint
	}
	if entry == "" {
		entry = defaultEntrypoint
	}
	return entry
}

func registerMCPServers(ctx context.Context, registry *tool.Registry, manager *sandbox.Manager, servers []mcpServer) error {
	for _, server := range servers {
		spec := server.Spec
		if err := enforceSandboxHost(manager, spec); err != nil {
			return err
		}
		opts := tool.MCPServerOptions{
			Headers:       server.Headers,
			Env:           server.Env,
			EnabledTools:  server.EnabledTools,
			DisabledTools: server.DisabledTools,
		}
		if server.TimeoutSeconds > 0 {
			opts.Timeout = time.Duration(server.TimeoutSeconds) * time.Second
		}
		if server.ToolTimeoutSeconds > 0 {
			opts.ToolTimeout = time.Duration(server.ToolTimeoutSeconds) * time.Second
		}

		var err error
		if !hasMCPServerOptions(opts) {
			err = registry.RegisterMCPServer(ctx, spec, server.Name)
		} else {
			err = registry.RegisterMCPServerWithOptions(ctx, spec, server.Name, opts)
		}
		if err != nil {
			return fmt.Errorf("api: register MCP %s: %w", spec, err)
		}
	}
	return nil
}

func hasMCPServerOptions(opts tool.MCPServerOptions) bool {
	return len(opts.Headers) > 0 ||
		len(opts.Env) > 0 ||
		opts.Timeout > 0 ||
		len(opts.EnabledTools) > 0 ||
		len(opts.DisabledTools) > 0 ||
		opts.ToolTimeout > 0
}

func enforceSandboxHost(manager *sandbox.Manager, server string) error {
	if manager == nil || strings.TrimSpace(server) == "" {
		return nil
	}
	u, err := url.Parse(server)
	if err != nil || u == nil || strings.TrimSpace(u.Scheme) == "" {
		return nil
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	base, _, _ := strings.Cut(scheme, "+")
	switch base {
	case "http", "https", "sse":
		if err := manager.CheckNetwork(u.Host); err != nil {
			return fmt.Errorf("api: MCP host denied: %w", err)
		}
	}
	return nil
}

func newOutputPersister(settings *config.Settings) *tool.OutputPersister {
	persister := tool.NewOutputPersister()
	if settings == nil || settings.ToolOutput == nil {
		return persister
	}
	if settings.ToolOutput.DefaultThresholdBytes > 0 {
		persister.DefaultThresholdBytes = settings.ToolOutput.DefaultThresholdBytes
	}
	if len(settings.ToolOutput.PerToolThresholdBytes) > 0 {
		persister.PerToolThresholdBytes = make(map[string]int, len(settings.ToolOutput.PerToolThresholdBytes))
		for name, threshold := range settings.ToolOutput.PerToolThresholdBytes {
			persister.PerToolThresholdBytes[name] = threshold
		}
	}
	return persister
}

func resolveModel(ctx context.Context, opts Options) (model.Model, error) {
	if opts.Model != nil {
		return opts.Model, nil
	}
	if opts.ModelFactory != nil {
		mdl, err := opts.ModelFactory.Model(ctx)
		if err != nil {
			return nil, fmt.Errorf("api: model factory: %w", err)
		}
		return mdl, nil
	}
	return nil, ErrMissingModel
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func defaultSessionID(entry EntryPoint) string {
	prefix := strings.TrimSpace(string(entry))
	if prefix == "" {
		prefix = string(defaultEntrypoint)
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}