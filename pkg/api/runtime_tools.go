package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	sdk "github.com/godeps/aigo"

	"github.com/cinience/saker/pkg/agent"
	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/config"
	coreevents "github.com/cinience/saker/pkg/core/events"
	"github.com/cinience/saker/pkg/logging"
	"github.com/cinience/saker/pkg/media/transcribe"
	"github.com/cinience/saker/pkg/message"
	"github.com/cinience/saker/pkg/middleware"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/runtime/commands"
	"github.com/cinience/saker/pkg/runtime/skills"
	"github.com/cinience/saker/pkg/runtime/subagents"
	"github.com/cinience/saker/pkg/runtime/tasks"
	"github.com/cinience/saker/pkg/sandbox"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/security"
	"github.com/cinience/saker/pkg/tool"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
	aigotools "github.com/cinience/saker/pkg/tool/builtin/aigo"
)

type runtimeToolExecutor struct {
	executor  *tool.Executor
	hooks     *runtimeHookAdapter
	history   *message.History
	allow     map[string]struct{}
	root      string
	host      string
	sessionID string
	yolo      bool // skip all whitelist and permission checks

	permissionResolver tool.PermissionResolver
}

func (t *runtimeToolExecutor) measureUsage() sandbox.ResourceUsage {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return sandbox.ResourceUsage{MemoryBytes: stats.Alloc}
}

func (t *runtimeToolExecutor) isAllowed(ctx context.Context, name string) bool {
	if t.yolo {
		return true
	}
	canon := canonicalToolName(name)
	if canon == "" {
		return false
	}
	reqAllowed := len(t.allow) == 0
	if len(t.allow) > 0 {
		_, reqAllowed = t.allow[canon]
	}
	subCtx, ok := subagents.FromContext(ctx)
	if !ok || len(subCtx.ToolWhitelist) == 0 {
		return reqAllowed
	}
	subSet := toLowerSet(subCtx.ToolWhitelist)
	if len(subSet) == 0 {
		return reqAllowed
	}
	_, subAllowed := subSet[canon]
	if len(t.allow) == 0 {
		return subAllowed
	}
	return reqAllowed && subAllowed
}

func (t *runtimeToolExecutor) Execute(ctx context.Context, call agent.ToolCall, _ *agent.Context) (agent.ToolResult, error) {
	appendToolResult := func(content string, blocks []model.ContentBlock, artifacts []artifact.ArtifactRef) {
		if t.history != nil {
			msg := message.Message{
				Role: "tool",
				ToolCalls: []message.ToolCall{{
					ID:     call.ID,
					Name:   call.Name,
					Result: content,
				}},
			}
			if len(blocks) > 0 {
				msg.ContentBlocks = convertAPIContentBlocks(blocks)
			}
			if len(artifacts) > 0 {
				msg.Artifacts = append([]artifact.ArtifactRef(nil), artifacts...)
			}
			t.history.Append(msg)
		}
	}
	appendEarlyError := func(err error) error {
		appendToolResult(fmt.Sprintf("Tool execution failed: %v", err), nil, nil)
		return err
	}

	if t.executor == nil {
		return agent.ToolResult{}, appendEarlyError(errors.New("tool executor not initialised"))
	}
	if !t.isAllowed(ctx, call.Name) {
		return agent.ToolResult{}, appendEarlyError(fmt.Errorf("tool %s is not whitelisted", call.Name))
	}

	// Defensive check: if tool call has empty/nil arguments but the tool requires
	// parameters, return a diagnostic error instead of executing with missing params.
	// This commonly happens when an API proxy strips tool_use.input (returns "input": {}).
	if len(call.Input) == 0 {
		if reg := t.executor.Registry(); reg != nil {
			if impl, err := reg.Get(call.Name); err == nil {
				if schema := impl.Schema(); schema != nil && len(schema.Required) > 0 {
					errMsg := fmt.Sprintf(
						"tool %q called with empty arguments but requires %v; "+
							"the API proxy likely stripped tool_use.input — check proxy configuration",
						call.Name, schema.Required)
					slog.Warn("tool call has empty arguments but requires parameters", "tool", call.Name, "id", call.ID, "message", errMsg)
					if t.history != nil {
						t.history.Append(message.Message{
							Role: "tool",
							ToolCalls: []message.ToolCall{{
								ID:     call.ID,
								Name:   call.Name,
								Result: errMsg,
							}},
						})
					}
					return agent.ToolResult{
						Name:     call.Name,
						Output:   errMsg,
						Metadata: map[string]any{"error": "empty_arguments"},
					}, nil
				}
			}
		}
	}

	params, preErr := t.hooks.PreToolUse(ctx, coreToolUsePayload(call))
	if preErr != nil {
		// In yolo mode, skip permission checks — auto-allow everything.
		if t.yolo && errors.Is(preErr, ErrToolUseRequiresApproval) {
			preErr = nil
		} else if errors.Is(preErr, ErrToolUseRequiresApproval) && t.permissionResolver != nil {
			checkParams := call.Input
			if params != nil {
				checkParams = params
			}
			decision, err := t.permissionResolver(ctx, tool.Call{
				Name:      call.Name,
				Params:    checkParams,
				SessionID: t.sessionID,
			}, security.PermissionDecision{
				Action: security.PermissionAsk,
				Tool:   call.Name,
				Rule:   "hook:pre_tool_use",
			})
			if err != nil {
				preErr = err
			} else {
				switch decision.Action {
				case security.PermissionAllow:
					preErr = nil
				case security.PermissionDeny:
					preErr = fmt.Errorf("%w: %s", ErrToolUseDenied, call.Name)
				default:
					preErr = fmt.Errorf("%w: %s", ErrToolUseRequiresApproval, call.Name)
				}
			}
		}
	}
	if preErr != nil {
		// Hook denied execution - still need to add tool_result to history
		errContent := fmt.Sprintf(`{"error":%q}`, preErr.Error())
		appendToolResult(errContent, nil, nil)
		return agent.ToolResult{Name: call.Name, Output: errContent, Metadata: map[string]any{"error": preErr.Error()}}, preErr
	}
	if params != nil {
		call.Input = params
	}

	toolLogger := logging.From(ctx)
	toolStart := time.Now()
	toolLogger.Info("tool.Execute started", "tool", call.Name, "call_id", call.ID)

	callSpec := tool.Call{
		Name:      call.Name,
		Params:    call.Input,
		Path:      t.root,
		Host:      t.host,
		Usage:     t.measureUsage(),
		SessionID: t.sessionID,
	}
	if emit := streamEmitFromContext(ctx); emit != nil {
		callSpec.StreamSink = func(chunk string, isStderr bool) {
			evt := StreamEvent{
				Type:      EventToolExecutionOutput,
				ToolUseID: call.ID,
				Name:      call.Name,
				Output:    chunk,
			}
			evt.IsStderr = &isStderr
			emit(ctx, evt)
		}
	}
	if t.host != "" {
		callSpec.Host = t.host
	}
	exec := t.executor
	if t.permissionResolver != nil {
		exec = exec.WithPermissionResolver(t.permissionResolver)
	}
	result, err := exec.Execute(ctx, callSpec)
	toolDuration := time.Since(toolStart).Milliseconds()
	if err != nil {
		toolLogger.Warn("tool.Execute failed", "tool", call.Name, "call_id", call.ID, "error", err, "duration_ms", toolDuration)
	} else {
		toolLogger.Info("tool.Execute completed", "tool", call.Name, "call_id", call.ID, "duration_ms", toolDuration)
	}
	toolResult := agent.ToolResult{Name: call.Name}
	meta := map[string]any{}
	content := ""
	var blocks []model.ContentBlock
	var artifacts []artifact.ArtifactRef
	if result != nil && result.Result != nil {
		toolResult.Output = result.Result.Output
		meta["data"] = result.Result.Data
		if result.Result.OutputRef != nil {
			meta["output_ref"] = result.Result.OutputRef
		}
		if result.Result.Summary != "" {
			meta["summary"] = result.Result.Summary
		}
		if result.Result.Structured != nil {
			meta["structured"] = result.Result.Structured
		}
		if result.Result.Preview != nil {
			meta["preview"] = result.Result.Preview
		}
		content = result.Result.Output
		if len(result.Result.ContentBlocks) > 0 {
			blocks = append([]model.ContentBlock(nil), result.Result.ContentBlocks...)
			meta["content_blocks"] = blocks
		}
		if len(result.Result.Artifacts) > 0 {
			artifacts = append([]artifact.ArtifactRef(nil), result.Result.Artifacts...)
			meta["artifacts"] = artifacts
		}
	}
	if err != nil {
		meta["error"] = err.Error()
		content = fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	if len(meta) > 0 {
		toolResult.Metadata = meta
	}

	if hookErr := t.hooks.PostToolUse(ctx, coreToolResultPayload(call, result, err)); hookErr != nil && err == nil {
		// Hook failed - still need to add tool_result to history
		appendToolResult(content, blocks, artifacts)
		return toolResult, hookErr
	}

	appendToolResult(content, blocks, artifacts)
	return toolResult, err
}

func coreToolUsePayload(call agent.ToolCall) coreevents.ToolUsePayload {
	return coreevents.ToolUsePayload{Name: call.Name, Params: call.Input}
}

func coreToolResultPayload(call agent.ToolCall, res *tool.CallResult, err error) coreevents.ToolResultPayload {
	payload := coreevents.ToolResultPayload{Name: call.Name}
	if res != nil && res.Result != nil {
		payload.Result = res.Result.Output
		payload.Duration = res.Duration()
	}
	payload.Err = err
	return payload
}

func buildPermissionResolver(hooks *runtimeHookAdapter, handler PermissionRequestHandler, approvals *security.ApprovalQueue, approver string, whitelistTTL time.Duration, approvalWait bool) tool.PermissionResolver {
	if hooks == nil && handler == nil && approvals == nil {
		return nil
	}
	return func(ctx context.Context, call tool.Call, decision security.PermissionDecision) (security.PermissionDecision, error) {
		if decision.Action != security.PermissionAsk {
			return decision, nil
		}

		req := PermissionRequest{
			ToolName:   call.Name,
			ToolParams: call.Params,
			SessionID:  call.SessionID,
			Rule:       decision.Rule,
			Target:     decision.Target,
			Reason:     buildPermissionReason(decision),
		}

		var record *security.ApprovalRecord
		if approvals != nil && strings.TrimSpace(call.SessionID) != "" {
			command := formatApprovalCommand(call.Name, decision.Target)
			rec, err := approvals.Request(call.SessionID, command, nil)
			if err != nil {
				return decision, err
			}
			record = rec
			req.Approval = rec
			if rec != nil && rec.State == security.ApprovalApproved && rec.AutoApproved {
				return decisionWithAction(decision, security.PermissionAllow), nil
			}
		}

		if hooks != nil {
			hookDecision, err := hooks.PermissionRequest(ctx, coreevents.PermissionRequestPayload{
				ToolName:   call.Name,
				ToolParams: call.Params,
				Reason:     req.Reason,
			})
			if err != nil {
				return decision, err
			}
			switch hookDecision {
			case coreevents.PermissionAllow:
				if record != nil {
					if _, err := approvals.Approve(record.ID, approvalActor(approver), whitelistTTL); err != nil {
						return decision, err
					}
				}
				return decisionWithAction(decision, security.PermissionAllow), nil
			case coreevents.PermissionDeny:
				if record != nil {
					if _, err := approvals.Deny(record.ID, approvalActor(approver), "denied by permission hook"); err != nil {
						return decision, err
					}
				}
				return decisionWithAction(decision, security.PermissionDeny), nil
			}
		}

		if handler != nil {
			hostDecision, err := handler(ctx, req)
			if err != nil {
				return decision, err
			}
			switch hostDecision {
			case coreevents.PermissionAllow:
				if record != nil {
					if _, err := approvals.Approve(record.ID, approvalActor(approver), whitelistTTL); err != nil {
						return decision, err
					}
				}
				return decisionWithAction(decision, security.PermissionAllow), nil
			case coreevents.PermissionDeny:
				if record != nil {
					if _, err := approvals.Deny(record.ID, approvalActor(approver), "denied by host"); err != nil {
						return decision, err
					}
				}
				return decisionWithAction(decision, security.PermissionDeny), nil
			}
		}

		if approvalWait && approvals != nil && record != nil {
			resolved, err := approvals.Wait(ctx, record.ID)
			if err != nil {
				return decision, err
			}
			switch resolved.State {
			case security.ApprovalApproved:
				return decisionWithAction(decision, security.PermissionAllow), nil
			case security.ApprovalDenied:
				return decisionWithAction(decision, security.PermissionDeny), nil
			}
		}

		return decision, nil
	}
}

func buildPermissionReason(decision security.PermissionDecision) string {
	rule := strings.TrimSpace(decision.Rule)
	target := strings.TrimSpace(decision.Target)
	switch {
	case rule == "" && target == "":
		return ""
	case rule == "":
		return fmt.Sprintf("target %q", target)
	case target == "":
		return fmt.Sprintf("rule %q", rule)
	default:
		return fmt.Sprintf("rule %q for %s", rule, target)
	}
}

func formatApprovalCommand(toolName, target string) string {
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return name
	}
	return fmt.Sprintf("%s(%s)", name, target)
}

func decisionWithAction(base security.PermissionDecision, action security.PermissionAction) security.PermissionDecision {
	base.Action = action
	return base
}

func approvalActor(approver string) string {
	if strings.TrimSpace(approver) == "" {
		return "host"
	}
	return strings.TrimSpace(approver)
}

// newSafetyMiddleware creates a SafetyMiddleware that bridges the agent.ToolResult
// type into the middleware layer for leak detection and injection sanitization.
func newSafetyMiddleware() *middleware.SafetyMiddleware {
	extract := func(toolResult any) (string, string, bool) {
		tr, ok := toolResult.(agent.ToolResult)
		if !ok {
			return "", "", false
		}
		return tr.Name, tr.Output, true
	}
	write := func(st *middleware.State, output string, meta map[string]any) {
		tr, ok := st.ToolResult.(agent.ToolResult)
		if !ok {
			return
		}
		tr.Output = output
		if tr.Metadata == nil {
			tr.Metadata = map[string]any{}
		}
		for k, v := range meta {
			tr.Metadata[k] = v
		}
		st.ToolResult = tr
	}
	return middleware.NewSafetyMiddleware(extract, write)
}

// newSubdirHintsMiddleware creates a SubdirHints middleware that bridges the
// agent.ToolCall / agent.ToolResult types into the middleware layer.
func newSubdirHintsMiddleware(workDir string) middleware.Middleware {
	return middleware.NewSubdirHints(middleware.SubdirHintsConfig{
		WorkingDir: workDir,
		ExtractInput: func(toolCall any) map[string]any {
			tc, ok := toolCall.(agent.ToolCall)
			if !ok {
				return nil
			}
			return tc.Input
		},
		AppendToResult: func(st *middleware.State, extra string) {
			tr, ok := st.ToolResult.(agent.ToolResult)
			if !ok {
				return
			}
			tr.Output += extra
			st.ToolResult = tr
		},
	})
}

// ----------------- config + registries -----------------

type registeredToolRefs struct {
	taskTool      *toolbuiltin.TaskTool
	streamMonitor *toolbuiltin.StreamMonitorTool
}

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

func builtinToolFactories(root string, sandboxDisabled bool, entry EntryPoint, settings *config.Settings, skReg *skills.Registry, cmdExec *commands.Executor, taskStore tasks.Store, mdl model.Model, contextWindowTokens int, aigoCfg *config.AigoConfig, canvasDir string, execEnvOpt ...sandboxenv.ExecutionEnvironment) map[string]func() tool.Tool {
	factories := map[string]func() tool.Tool{}
	var execEnv sandboxenv.ExecutionEnvironment
	if len(execEnvOpt) > 0 {
		execEnv = execEnvOpt[0]
	}

	var (
		syncThresholdBytes  int
		asyncThresholdBytes int
	)
	if settings != nil && settings.BashOutput != nil {
		if settings.BashOutput.SyncThresholdBytes != nil {
			syncThresholdBytes = *settings.BashOutput.SyncThresholdBytes
		}
		if settings.BashOutput.AsyncThresholdBytes != nil {
			asyncThresholdBytes = *settings.BashOutput.AsyncThresholdBytes
		}
	}
	if asyncThresholdBytes > 0 {
		toolbuiltin.DefaultAsyncTaskManager().SetMaxOutputLen(asyncThresholdBytes)
	}

	bashCtor := func() tool.Tool {
		var bash *toolbuiltin.BashTool
		if sandboxDisabled {
			bash = toolbuiltin.NewBashToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			bash = toolbuiltin.NewBashToolWithRoot(root)
		}
		if syncThresholdBytes > 0 {
			bash.SetOutputThresholdBytes(syncThresholdBytes)
		}
		bash.SetEnvironment(execEnv)
		if entry == EntryPointCLI {
			bash.AllowShellMetachars(true)
		}
		return bash
	}

	readCtor := func() tool.Tool {
		var read *toolbuiltin.ReadTool
		if sandboxDisabled {
			read = toolbuiltin.NewReadToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			read = toolbuiltin.NewReadToolWithRoot(root)
		}
		read.SetEnvironment(execEnv)
		return read
	}
	writeCtor := func() tool.Tool {
		var write *toolbuiltin.WriteTool
		if sandboxDisabled {
			write = toolbuiltin.NewWriteToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			write = toolbuiltin.NewWriteToolWithRoot(root)
		}
		write.SetEnvironment(execEnv)
		return write
	}
	editCtor := func() tool.Tool {
		var edit *toolbuiltin.EditTool
		if sandboxDisabled {
			edit = toolbuiltin.NewEditToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			edit = toolbuiltin.NewEditToolWithRoot(root)
		}
		edit.SetEnvironment(execEnv)
		return edit
	}

	respectGitignore := true
	if settings != nil && settings.RespectGitignore != nil {
		respectGitignore = *settings.RespectGitignore
	}
	grepCtor := func() tool.Tool {
		var grep *toolbuiltin.GrepTool
		if sandboxDisabled {
			grep = toolbuiltin.NewGrepToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			grep = toolbuiltin.NewGrepToolWithRoot(root)
		}
		grep.SetEnvironment(execEnv)
		grep.SetRespectGitignore(respectGitignore)
		return grep
	}
	globCtor := func() tool.Tool {
		var glob *toolbuiltin.GlobTool
		if sandboxDisabled {
			glob = toolbuiltin.NewGlobToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			glob = toolbuiltin.NewGlobToolWithRoot(root)
		}
		glob.SetEnvironment(execEnv)
		glob.SetRespectGitignore(respectGitignore)
		return glob
	}
	// Keep a defensive fallback because this helper is called directly in tests
	// and package-internal wiring paths outside Runtime.New.
	if taskStore == nil {
		taskStore = tasks.NewTaskStore()
	}

	factories["bash"] = bashCtor
	factories["file_read"] = readCtor
	factories["image_read"] = func() tool.Tool {
		if sandboxDisabled {
			return toolbuiltin.NewImageReadToolWithSandbox(root, security.NewDisabledSandbox())
		}
		return toolbuiltin.NewImageReadToolWithRoot(root)
	}
	factories["canvas_get_node"] = func() tool.Tool {
		return toolbuiltin.NewCanvasGetNodeTool(canvasDir)
	}
	factories["canvas_list_nodes"] = func() tool.Tool {
		return toolbuiltin.NewCanvasListNodesTool(canvasDir)
	}
	factories["canvas_table_write"] = func() tool.Tool {
		return toolbuiltin.NewCanvasTableWriteTool(canvasDir)
	}
	factories["file_write"] = writeCtor
	factories["file_edit"] = editCtor
	factories["grep"] = grepCtor
	factories["glob"] = globCtor
	factories["web_fetch"] = func() tool.Tool { return toolbuiltin.NewWebFetchTool(nil) }
	factories["web_search"] = func() tool.Tool { return toolbuiltin.NewWebSearchTool(nil) }
	factories["bash_output"] = func() tool.Tool { return toolbuiltin.NewBashOutputTool(nil) }
	factories["bash_status"] = func() tool.Tool { return toolbuiltin.NewBashStatusTool() }
	factories["kill_task"] = func() tool.Tool { return toolbuiltin.NewKillTaskTool() }
	factories["task_create"] = func() tool.Tool { return toolbuiltin.NewTaskCreateTool(taskStore) }
	factories["task_list"] = func() tool.Tool { return toolbuiltin.NewTaskListTool(taskStore) }
	factories["task_get"] = func() tool.Tool { return toolbuiltin.NewTaskGetTool(taskStore) }
	factories["task_update"] = func() tool.Tool { return toolbuiltin.NewTaskUpdateTool(taskStore) }
	factories["ask_user_question"] = func() tool.Tool { return toolbuiltin.NewAskUserQuestionTool() }
	factories["skill"] = func() tool.Tool {
		st := toolbuiltin.NewSkillTool(skReg, nil)
		st.SetContextWindow(resolveContextWindow(contextWindowTokens, mdl))
		return st
	}
	factories["slash_command"] = func() tool.Tool { return toolbuiltin.NewSlashCommandTool(cmdExec) }
	factories["video_sampler"] = func() tool.Tool { return toolbuiltin.NewVideoSamplerTool() }
	factories["stream_capture"] = func() tool.Tool { return toolbuiltin.NewStreamCaptureTool() }
	factories["browser"] = func() tool.Tool { return toolbuiltin.NewBrowserTool() }
	factories["stream_monitor"] = func() tool.Tool { return toolbuiltin.NewStreamMonitorTool(taskStore) }
	factories["webhook"] = func() tool.Tool { return toolbuiltin.NewWebhookTool() }
	factories["media_index"] = func() tool.Tool {
		return toolbuiltin.NewMediaIndexTool(func(t *toolbuiltin.MediaIndexTool) {
			t.Model = mdl
		})
	}
	factories["media_search"] = func() tool.Tool { return toolbuiltin.NewMediaSearchTool() }
	if mdl != nil {
		factories["frame_analyzer"] = func() tool.Tool { return toolbuiltin.NewFrameAnalyzerTool(mdl) }
		factories["video_summarizer"] = func() tool.Tool { return toolbuiltin.NewVideoSummarizerTool(mdl) }
		factories["analyze_video"] = func() tool.Tool {
			t := toolbuiltin.NewAnalyzeVideoTool(mdl)
			// Inject TranscribeFunc from aigo ASR if available.
			if transcribeFn := resolveTranscribeFunc(aigoCfg); transcribeFn != nil {
				t.Transcribe = transcribeFn
			}
			// Set base store directory under project root (session subdirs resolved at runtime).
			if root != "" {
				t.StoreDir = filepath.Join(root, ".saker", "media")
			}
			return t
		}
	}

	if shouldRegisterTaskTool(entry) {
		factories["task"] = func() tool.Tool { return toolbuiltin.NewTaskTool() }
	}

	return factories
}

// resolveContextWindow determines the model's context window size (tokens).
// Priority: explicit value > dynamic interface > static registry > 0 (default budget).
func resolveContextWindow(explicit int, mdl model.Model) int {
	if explicit > 0 {
		return explicit
	}
	if mdl == nil {
		return 0
	}
	if cwp, ok := mdl.(model.ContextWindowProvider); ok {
		if tokens := cwp.ContextWindow(); tokens > 0 {
			return tokens
		}
	}
	if namer, ok := mdl.(model.ModelNamer); ok {
		if tokens := model.LookupContextWindow(namer.ModelName()); tokens > 0 {
			return tokens
		}
	}
	return 0
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

// resolveTranscribeFunc builds a TranscribeFunc for audio transcription.
// Priority: whisper CLI (local, no API cost) > aigo ASR (cloud-based).
// Returns nil if neither is available.
func resolveTranscribeFunc(aigoCfg *config.AigoConfig) toolbuiltin.TranscribeFunc {
	// Try whisper CLI first (faster, no API cost).
	if transcribe.WhisperAvailable() != "" {
		return transcribe.WhisperTranscribe
	}

	// Try aigo ASR.
	if aigoCfg == nil {
		return nil
	}
	asrEngines := aigoCfg.Routing["asr"]
	if len(asrEngines) == 0 {
		return nil
	}

	// Build a dedicated client with only ASR engines registered.
	client := sdk.NewClient()
	registered := false
	for _, ref := range asrEngines {
		providerName, modelName, err := aigotools.ParseRef(ref)
		if err != nil {
			slog.Warn("[aigo-asr] invalid routing ref", "ref", ref, "error", err)
			continue
		}
		provider, ok := aigoCfg.Providers[providerName]
		if !ok {
			continue
		}
		eng, err := aigotools.BuildEngine(provider, modelName, "asr")
		if err != nil {
			slog.Warn("[aigo-asr] build engine failed", "ref", ref, "error", err)
			continue
		}
		if err := client.RegisterEngine(ref, eng); err != nil {
			slog.Warn("[aigo-asr] register engine failed", "ref", ref, "error", err)
			continue
		}
		registered = true
	}
	if !registered {
		return nil
	}

	slog.Info("[aigo-asr] ASR transcription available", "engines", asrEngines)

	return func(ctx context.Context, audioPath string) (string, error) {
		// Convert local file to base64 data URI.
		data, err := os.ReadFile(audioPath)
		if err != nil {
			return "", fmt.Errorf("aigo-asr: read audio %s: %w", audioPath, err)
		}
		mimeType := mime.TypeByExtension(filepath.Ext(audioPath))
		if mimeType == "" {
			mimeType = "audio/wav"
		}
		dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data))

		task := sdk.AgentTask{Prompt: dataURI}

		// Try engines with fallback.
		var lastErr error
		for _, eng := range asrEngines {
			result, err := client.ExecuteTask(ctx, eng, task)
			if err != nil {
				lastErr = err
				slog.Warn("[aigo-asr] engine failed", "engine", eng, "error", err)
				continue
			}
			text := strings.TrimSpace(result.Value)
			if text != "" {
				slog.Info("[aigo-asr] transcribed audio", "file", filepath.Base(audioPath), "engine", eng, "chars", len(text))
				return text, nil
			}
		}
		if lastErr != nil {
			return "", fmt.Errorf("aigo-asr: all engines failed: %w", lastErr)
		}
		return "", nil
	}
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
