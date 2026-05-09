package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acpclient "github.com/cinience/saker/pkg/acp/client"
	"github.com/cinience/saker/pkg/agent"
	"github.com/cinience/saker/pkg/config"
	coreevents "github.com/cinience/saker/pkg/core/events"
	corehooks "github.com/cinience/saker/pkg/core/hooks"
	"github.com/cinience/saker/pkg/logging"
	"github.com/cinience/saker/pkg/memory"
	"github.com/cinience/saker/pkg/message"
	"github.com/cinience/saker/pkg/middleware"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/persona"
	runtimecache "github.com/cinience/saker/pkg/runtime/cache"
	"github.com/cinience/saker/pkg/runtime/checkpoint"
	"github.com/cinience/saker/pkg/runtime/commands"
	"github.com/cinience/saker/pkg/runtime/skills"
	"github.com/cinience/saker/pkg/runtime/subagents"
	"github.com/cinience/saker/pkg/runtime/tasks"
	"github.com/cinience/saker/pkg/sandbox"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/sessiondb"
	"github.com/cinience/saker/pkg/tool"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
	aigotools "github.com/cinience/saker/pkg/tool/builtin/aigo"
	"github.com/google/uuid"
)

type streamContextKey string

const streamEmitCtxKey streamContextKey = "saker.stream.emit"

// Limits for the output-schema post-processing extraction call.
const (
	outputSchemaMaxTokens  = 8192
	outputSchemaMaxHistory = 10
)

func withStreamEmit(ctx context.Context, emit streamEmitFunc) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, streamEmitCtxKey, emit)
}

func streamEmitFromContext(ctx context.Context) streamEmitFunc {
	if ctx == nil {
		return nil
	}
	if emit, ok := ctx.Value(streamEmitCtxKey).(streamEmitFunc); ok {
		return emit
	}
	return nil
}

// Runtime exposes the unified SDK surface that powers CLI/CI/enterprise entrypoints.
type Runtime struct {
	opts             Options
	mode             ModeContext
	settings         *config.Settings // raw merged settings from files
	cfg              *config.Settings // project-scoped config derived from settings
	fs               *config.FS
	rulesLoader      *config.RulesLoader
	sandbox          *sandbox.Manager
	execEnv          sandboxenv.ExecutionEnvironment
	sbRoot           string
	registry         *tool.Registry
	executor         *tool.Executor
	recorder         HookRecorder
	hooks            *corehooks.Executor
	histories        *historyStore
	historyPersister *diskHistoryPersister
	sessionGate      *sessionGate

	cmdExec            *commands.Executor
	skReg              *skills.Registry
	subMgr             *subagents.Manager
	subStore           subagents.Store
	subExec            *subagents.Executor
	taskStore          tasks.Store
	streamMonitor      *toolbuiltin.StreamMonitorTool
	checkpoints        checkpoint.Store
	cacheStore         runtimecache.Store
	tokens             *tokenTracker
	compactor          *compactor
	memoryStore        *memory.Store
	personaRegistry    *persona.Registry
	personaRouter      *persona.Router
	sessionDB          *sessiondb.Store
	skillLearner       *skills.Learner
	skillTracker       *skills.SkillTracker
	systemPromptBlocks []string // cache-optimized prompt blocks (nil = use single System string)
	tracer             Tracer

	mu sync.RWMutex

	runMu         sync.Mutex
	runWG         sync.WaitGroup
	closeOnce     sync.Once
	closeErr      error
	closed        bool
	ownsTaskStore bool
}

// New instantiates a unified runtime bound to the provided options.
func New(ctx context.Context, opts Options) (*Runtime, error) {
	opts = opts.withDefaults()
	opts = opts.frozen()
	mode := opts.modeContext()

	// 初始化文件系统抽象层
	fsLayer := config.NewFS(opts.ProjectRoot, opts.EmbedFS)
	opts.fsLayer = fsLayer

	if err := materializeEmbeddedClaudeHooks(opts.ProjectRoot, opts.EmbedFS); err != nil {
		logging.From(ctx).Warn("claude hooks materializer warning", "error", err)
	}

	// Inject default system prompt when none is provided.
	// Build cache-optimized blocks: static (cacheable) + dynamic (session-specific).
	var promptBlocks []string
	if strings.TrimSpace(opts.SystemPrompt) == "" {
		envInfo := collectEnvironmentInfo(opts)
		promptBlocks = buildSystemPromptBlocks(opts, envInfo, defaultBuiltinToolNames)
		opts.SystemPrompt = strings.Join(promptBlocks, "\n\n")
	}

	if memory, err := config.LoadClaudeMD(opts.ProjectRoot, fsLayer); err != nil {
		logging.From(ctx).Warn("claude.md loader warning", "error", err)
	} else if strings.TrimSpace(memory) != "" {
		claudeMD := fmt.Sprintf("## Memory\n\n%s", strings.TrimSpace(memory))
		opts.SystemPrompt = fmt.Sprintf("%s\n\n%s", opts.SystemPrompt, claudeMD)
		// Append CLAUDE.md to the last (dynamic) block for cache optimization.
		if len(promptBlocks) > 0 {
			promptBlocks[len(promptBlocks)-1] += "\n\n" + claudeMD
		}
	}

	settings, err := loadSettings(opts)
	if err != nil {
		return nil, err
	}

	mdl, err := resolveModel(ctx, opts)
	if err != nil {
		return nil, err
	}
	opts.Model = mdl

	sbox, sbRoot := buildSandboxManager(opts, settings)
	execEnv := buildExecutionEnvironment(opts)
	cmdExec, cmdErrs := buildCommandsExecutor(opts)
	if len(cmdErrs) > 0 {
		for _, err := range cmdErrs {
			logging.From(ctx).Warn("command loader warning", "error", err)
		}
	}
	skReg, skErrs := buildSkillsRegistry(opts)
	if len(skErrs) > 0 {
		for _, err := range skErrs {
			logging.From(ctx).Warn("skill loader warning", "error", err)
		}
	}
	subMgr, subErrs := buildSubagentsManager(opts)
	if len(subErrs) > 0 {
		for _, err := range subErrs {
			logging.From(ctx).Warn("subagent loader warning", "error", err)
		}
	}
	ownsTaskStore := false
	if opts.TaskStore == nil {
		opts.TaskStore = tasks.NewTaskStore()
		ownsTaskStore = true
	}
	registry := tool.NewRegistry()
	toolRefs, err := registerTools(registry, opts, settings, skReg, cmdExec, execEnv)
	if err != nil {
		return nil, err
	}
	taskTool := toolRefs.taskTool
	mcpServers := collectMCPServers(settings, opts.MCPServers)
	if err := registerMCPServers(ctx, registry, sbox, mcpServers); err != nil {
		return nil, err
	}
	executor := tool.NewExecutor(registry, sbox).WithOutputPersister(newOutputPersister(settings))

	recorder := defaultHookRecorder()
	hooks := newHookExecutor(opts, recorder, settings)
	compactor := newCompactor(opts.ProjectRoot, opts.AutoCompact, opts.Model, opts.TokenLimit, hooks)

	// Initialize session memory store.
	var memoryStore *memory.Store
	if dir := strings.TrimSpace(opts.MemoryDir); dir != "" {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(opts.ProjectRoot, dir)
		}
		ms, msErr := memory.NewStore(dir)
		if msErr != nil {
			logging.From(ctx).Warn("memory store warning", "error", msErr)
		} else {
			memoryStore = ms
			if memCtx, ctxErr := ms.BuildContext(6000); ctxErr == nil && strings.TrimSpace(memCtx) != "" {
				memContent := strings.TrimSpace(memCtx)
				opts.SystemPrompt = fmt.Sprintf("%s\n\n%s", opts.SystemPrompt, memContent)
				if len(promptBlocks) > 0 {
					promptBlocks[len(promptBlocks)-1] += "\n\n" + memContent
				}
			}
			// Register memory tools.
			if regErr := registry.Register(toolbuiltin.NewMemorySaveTool(ms)); regErr != nil {
				logging.From(ctx).Warn("memory_save tool registration warning", "error", regErr)
			}
			if regErr := registry.Register(toolbuiltin.NewMemoryReadTool(ms)); regErr != nil {
				logging.From(ctx).Warn("memory_read tool registration warning", "error", regErr)
			}
		}
	}

	// Wire memory store into compactor for session memory compaction.
	if compactor != nil && memoryStore != nil {
		compactor.SetMemoryStore(memoryStore)
	}

	// Initialize persona registry and router.
	personaRegistry, personaRouter := initPersonas(opts, settings)

	// Initialize tracer (noop without 'otel' build tag)
	tracer, err := NewTracer(opts.OTEL)
	if err != nil {
		return nil, fmt.Errorf("otel tracer init: %w", err)
	}

	var rulesLoader *config.RulesLoader
	if opts.RulesEnabled == nil || (opts.RulesEnabled != nil && *opts.RulesEnabled) {
		rulesLoader = config.NewRulesLoaderWithConfigRoot(opts.ProjectRoot, opts.ConfigRoot)
		if _, err := rulesLoader.LoadRules(); err != nil {
			logging.From(ctx).Warn("rules loader warning", "error", err)
		}
		if err := rulesLoader.WatchChanges(nil); err != nil {
			logging.From(ctx).Warn("rules watcher warning", "error", err)
		}
	}

	histories := newHistoryStore(opts.MaxSessions)
	var historyPersister *diskHistoryPersister
	retainDays := 0
	if settings != nil && settings.CleanupPeriodDays != nil {
		retainDays = *settings.CleanupPeriodDays
	}
	if retainDays > 0 {
		historyPersister = newDiskHistoryPersister(opts.ProjectRoot, opts.ConfigRoot)
		if historyPersister != nil {
			histories.loader = historyPersister.Load
			if err := historyPersister.Cleanup(retainDays); err != nil {
				logging.From(ctx).Warn("history cleanup warning", "error", err)
			}
		}
	}

	// Initialize SQLite session index (additive alongside JSON history files).
	var sessDB *sessiondb.Store
	if base := resolveConfigBase(opts.ProjectRoot, opts.ConfigRoot); base != "" {
		dbPath := filepath.Join(base, "sessions.db")
		if db, dbErr := sessiondb.Open(dbPath); dbErr != nil {
			logging.From(ctx).Warn("session db warning", "error", dbErr)
		} else {
			sessDB = db
		}
	}

	// Bridge ACP agent config from settings into Options when not already set.
	if len(opts.ACPAgents) == 0 && settings != nil && settings.ACP != nil && len(settings.ACP.Agents) > 0 {
		opts.ACPAgents = settings.ACP.Agents
	}

	// Auto-detect ACP agents on PATH and merge into Options (settings entries take precedence).
	for _, detected := range acpclient.DetectAgents() {
		if opts.ACPAgents == nil {
			opts.ACPAgents = map[string]config.ACPAgentEntry{}
		}
		if _, exists := opts.ACPAgents[detected.Name]; !exists {
			opts.ACPAgents[detected.Name] = config.ACPAgentEntry{
				Command: detected.Path,
				Args:    detected.Args,
				Timeout: "5m",
			}
		}
	}

	rt := &Runtime{
		opts:               opts,
		mode:               mode,
		settings:           settings,
		cfg:                projectConfigFromSettings(settings),
		fs:                 fsLayer,
		rulesLoader:        rulesLoader,
		sandbox:            sbox,
		execEnv:            execEnv,
		sbRoot:             sbRoot,
		registry:           registry,
		executor:           executor,
		recorder:           recorder,
		hooks:              hooks,
		histories:          histories,
		historyPersister:   historyPersister,
		cmdExec:            cmdExec,
		skReg:              skReg,
		subMgr:             subMgr,
		subStore:           subagents.NewMemoryStore(),
		taskStore:          opts.TaskStore,
		streamMonitor:      toolRefs.streamMonitor,
		checkpoints:        opts.CheckpointStore,
		cacheStore:         opts.CacheStore,
		tokens:             newTokenTracker(opts.TokenTracking, opts.TokenCallback),
		compactor:          compactor,
		memoryStore:        memoryStore,
		personaRegistry:    personaRegistry,
		personaRouter:      personaRouter,
		sessionDB:          sessDB,
		systemPromptBlocks: promptBlocks,
		tracer:             tracer,
		ownsTaskStore:      ownsTaskStore,
	}
	if base := resolveConfigBase(opts.ProjectRoot, opts.ConfigRoot); base != "" {
		rt.skillLearner = skills.NewLearner(filepath.Join(base, "learned-skills"), skReg)
		if opts.Model != nil {
			rt.skillLearner.SetRefiner(&modelSkillRefiner{model: opts.Model})
		}
		rt.skillTracker = skills.NewSkillTracker(filepath.Join(base, "skill-analytics.json"))
	}
	if rt.checkpoints == nil {
		rt.checkpoints = checkpoint.NewMemoryStore()
	}
	// Ensure subagent manager exists when Task tool is registered, so the
	// agent-loop fallback path in runTraditional works even without
	// explicit handler registrations.
	if rt.subMgr == nil && taskTool != nil {
		rt.subMgr = subagents.NewManager()
	}
	if rt.subMgr != nil {
		rt.subExec = subagents.NewExecutor(rt.subMgr, rt.subStore, rt.buildSubagentRunner())
	}
	rt.sessionGate = newSessionGate()

	if taskTool != nil {
		taskTool.SetRunner(rt.taskRunner())
		if desc := buildACPAgentDescriptions(opts.ACPAgents); desc != "" {
			taskTool.AppendAgentDescriptions(desc)
		}
	}
	return rt, nil
}

func (rt *Runtime) beginRun() error {
	rt.runMu.Lock()
	defer rt.runMu.Unlock()
	if rt.closed {
		return ErrRuntimeClosed
	}
	rt.runWG.Add(1)
	return nil
}

func (rt *Runtime) endRun() {
	rt.runWG.Done()
}

// Run executes the unified pipeline synchronously.
func (rt *Runtime) Run(ctx context.Context, req Request) (*Response, error) {
	if rt == nil {
		return nil, ErrRuntimeClosed
	}
	if err := rt.beginRun(); err != nil {
		return nil, err
	}
	defer rt.endRun()

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = defaultSessionID(rt.mode.EntryPoint)
	}
	req.SessionID = sessionID

	logger := logging.From(ctx)
	logger.Info("runtime.Run started", "session_id", sessionID, "prompt_len", len(req.Prompt))
	start := time.Now()

	if err := rt.sessionGate.Acquire(ctx, sessionID); err != nil {
		return nil, ErrConcurrentExecution
	}
	defer rt.sessionGate.Release(sessionID)

	if req.Pipeline != nil || strings.TrimSpace(req.ResumeFromCheckpoint) != "" {
		return rt.runPipeline(ctx, req)
	}

	prep, err := rt.prepare(ctx, req)
	if err != nil {
		logger.Error("runtime.Run prepare failed", "session_id", sessionID, "error", err)
		return nil, err
	}
	if !prep.normalized.Ephemeral {
		defer rt.persistHistory(prep.normalized.SessionID, prep.history)
	}
	result, err := rt.runAgent(prep)
	if err != nil {
		logger.Error("runtime.Run agent failed", "session_id", sessionID, "error", err, "duration_ms", time.Since(start).Milliseconds())
		return nil, err
	}
	logger.Info("runtime.Run completed", "session_id", sessionID, "duration_ms", time.Since(start).Milliseconds())
	return rt.buildResponse(prep, result), nil
}

// RunStream executes the pipeline asynchronously and returns events over a channel.
func (rt *Runtime) RunStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	if rt == nil {
		return nil, ErrRuntimeClosed
	}
	if req.Pipeline == nil && strings.TrimSpace(req.ResumeFromCheckpoint) == "" && strings.TrimSpace(req.Prompt) == "" && len(req.ContentBlocks) == 0 {
		return nil, errors.New("api: prompt is empty")
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = defaultSessionID(rt.mode.EntryPoint)
	}
	req.SessionID = sessionID

	logger := logging.From(ctx)
	logger.Info("runtime.RunStream started", "session_id", sessionID, "prompt_len", len(req.Prompt))

	if err := rt.beginRun(); err != nil {
		return nil, err
	}

	if req.Pipeline != nil || strings.TrimSpace(req.ResumeFromCheckpoint) != "" {
		out := make(chan StreamEvent, 256)
		go func() {
			defer rt.endRun()
			defer close(out)
			if err := rt.sessionGate.Acquire(ctx, sessionID); err != nil {
				isErr := true
				out <- StreamEvent{Type: EventError, Output: ErrConcurrentExecution.Error(), IsError: &isErr}
				return
			}
			defer rt.sessionGate.Release(sessionID)

			out <- StreamEvent{Type: EventAgentStart, SessionID: sessionID}
			resp, err := rt.runPipeline(ctx, req)
			if err != nil {
				isErr := true
				out <- StreamEvent{Type: EventError, Output: err.Error(), IsError: &isErr, SessionID: sessionID}
				return
			}
			for _, entry := range resp.Timeline {
				entryCopy := entry
				out <- StreamEvent{Type: EventTimeline, Timeline: &entryCopy, SessionID: sessionID}
			}
			out <- StreamEvent{Type: EventAgentStop, SessionID: sessionID}
		}()
		return out, nil
	}

	// 缓冲区增大以吸收前端延迟（逐字符渲染等）导致的背压，避免 progress emit 阻塞工具执行
	out := make(chan StreamEvent, 512)
	progressChan := make(chan StreamEvent, 256)
	baseCtx := ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	progressMW := newProgressMiddleware(progressChan)
	ctxWithEmit := withStreamEmit(baseCtx, progressMW.streamEmit())
	go func() {
		defer rt.endRun()
		defer close(out)
		if err := rt.sessionGate.Acquire(ctxWithEmit, sessionID); err != nil {
			isErr := true
			out <- StreamEvent{Type: EventError, Output: ErrConcurrentExecution.Error(), IsError: &isErr}
			return
		}
		defer rt.sessionGate.Release(sessionID)

		prep, err := rt.prepare(ctxWithEmit, req)
		if err != nil {
			isErr := true
			out <- StreamEvent{Type: EventError, Output: err.Error(), IsError: &isErr}
			return
		}
		if !prep.normalized.Ephemeral {
			defer rt.persistHistory(prep.normalized.SessionID, prep.history)
		}

		// Emit skill activation events for explicitly matched skills only.
		// Skills with reason "always" (no matchers) or "mentioned" (name
		// appeared in prompt) are implicit and should not appear on the canvas.
		for _, sk := range prep.skillResults {
			if sk.MatchReason == "always" || sk.MatchReason == "mentioned" {
				continue
			}
			out <- StreamEvent{
				Type:      EventSkillActivation,
				Name:      sk.Definition.Name,
				SessionID: sessionID,
				Output: map[string]any{
					"description": sk.Definition.Description,
				},
			}
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			dropping := false
			for event := range progressChan {
				if dropping {
					continue
				}
				select {
				case out <- event:
				case <-ctxWithEmit.Done():
					dropping = true
				}
			}
		}()

		var runErr error
		var result runResult
		defer func() {
			if rt.hooks != nil {
				reason := "completed"
				if runErr != nil {
					reason = "error"
				}
				//nolint:errcheck // session end events are non-critical notifications
				rt.hooks.Publish(coreevents.Event{
					Type:      coreevents.SessionEnd,
					SessionID: req.SessionID,
					Payload:   coreevents.SessionEndPayload{SessionID: req.SessionID, Reason: reason},
				})
			}
		}()

		result, runErr = rt.runAgentWithMiddleware(prep, progressMW)
		close(progressChan)
		<-done

		if runErr != nil {
			isErr := true
			out <- StreamEvent{Type: EventError, Output: runErr.Error(), IsError: &isErr}
			return
		}
		rt.buildResponse(prep, result)
	}()
	return out, nil
}

// Close releases held resources.
func (rt *Runtime) Close() error {
	if rt == nil {
		return nil
	}
	rt.closeOnce.Do(func() {
		rt.runMu.Lock()
		rt.closed = true
		rt.runMu.Unlock()

		rt.runWG.Wait()

		var err error
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := toolbuiltin.DefaultAsyncTaskManager().Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			err = errors.Join(err, shutdownErr)
		}
		if shutdownErr == nil && rt.histories != nil {
			for _, sessionID := range rt.histories.SessionIDs() {
				if cleanupErr := cleanupBashOutputSessionDir(sessionID); cleanupErr != nil {
					slog.Error("api: session temp cleanup failed", "session_id", sessionID, "error", cleanupErr)
				}
				if cleanupErr := cleanupToolOutputSessionDir(sessionID); cleanupErr != nil {
					slog.Error("api: session tool output cleanup failed", "session_id", sessionID, "error", cleanupErr)
				}
			}
		}
		if rt.streamMonitor != nil {
			rt.streamMonitor.Close()
		}
		if rt.rulesLoader != nil {
			if e := rt.rulesLoader.Close(); e != nil {
				err = errors.Join(err, e)
			}
		}
		if rt.ownsTaskStore && rt.taskStore != nil {
			if e := rt.taskStore.Close(); e != nil {
				err = errors.Join(err, e)
			}
		}
		if rt.sessionDB != nil {
			if e := rt.sessionDB.Close(); e != nil {
				err = errors.Join(err, e)
			}
		}
		if rt.skillTracker != nil {
			if e := rt.skillTracker.Flush(); e != nil {
				err = errors.Join(err, e)
			}
		}
		if rt.registry != nil {
			rt.registry.Close()
		}
		if rt.tracer != nil {
			if e := rt.tracer.Shutdown(); e != nil {
				err = errors.Join(err, e)
			}
		}
		rt.closeErr = err
	})
	return rt.closeErr
}

// Config returns the last loaded project config.
func (rt *Runtime) Config() *config.Settings {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return config.MergeSettings(nil, rt.cfg)
}

// Settings exposes the merged settings.json snapshot for callers that need it.
func (rt *Runtime) Settings() *config.Settings {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return config.MergeSettings(nil, rt.settings)
}

// ToolInfo holds the name, description, and category of a registered tool.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// ToolInfos returns info for all registered tools, sorted by name.
func (rt *Runtime) ToolInfos() []ToolInfo {
	if rt.registry == nil {
		return nil
	}
	tools := rt.registry.List()
	infos := make([]ToolInfo, len(tools))
	for i, t := range tools {
		infos[i] = ToolInfo{
			Name:        t.Name(),
			Description: t.Description(),
			Category:    rt.registry.ToolSource(t.Name()),
		}
	}
	return infos
}

// ExecuteTool directly executes a registered tool by name with the given params.
// This bypasses the agent loop and is used by the web UI for direct tool invocation.
func (rt *Runtime) ExecuteTool(ctx context.Context, name string, params map[string]any) (*tool.ToolResult, error) {
	if rt.registry == nil {
		return nil, errors.New("api: tool registry is not initialized")
	}
	t, err := rt.registry.Get(name)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	return t.Execute(ctx, params)
}

// ToolSchemaResult holds the schema and engine info for a tool.
type ToolSchemaResult struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
	Engines     []string       `json:"engines,omitempty"`
}

// ToolSchema returns the JSON schema and available engines for a registered tool.
// If an engine is specified, it attempts to merge engine-specific capabilities (e.g. models, voices).
func (rt *Runtime) ToolSchema(name string, engineName string) (*ToolSchemaResult, error) {
	if rt.registry == nil {
		return nil, errors.New("api: tool registry is not initialized")
	}
	t, err := rt.registry.Get(name)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}

	schema := t.Schema()
	schemaMap := make(map[string]any)
	if schema != nil {
		raw, err := json.Marshal(schema)
		if err == nil {
			_ = json.Unmarshal(raw, &schemaMap)
		}
	}

	result := &ToolSchemaResult{
		Name:        t.Name(),
		Description: t.Description(),
		Schema:      schemaMap,
	}

	// Extract engines and potentially merge engine-specific capabilities if it's an AigoTool.
	if at, ok := t.(*aigotools.AigoTool); ok {
		result.Engines = at.Engines()

		// If a specific engine is requested, try to get its capabilities to override schema enums
		targetEngine := engineName
		if targetEngine == "" && len(result.Engines) > 0 {
			targetEngine = result.Engines[0]
		}

		if targetEngine != "" {
			// Get capabilities for the specific engine
			cap := at.EngineCapabilities(targetEngine)
			if cap != nil {
				// Merge capabilities into schema properties (e.g. voices, models, sizes)
				if props, ok := result.Schema["properties"].(map[string]any); ok {
					if len(cap.Voices) > 0 {
						props["voice"] = map[string]any{"type": "string", "enum": cap.Voices}
					}
					if len(cap.Models) > 0 {
						props["model"] = map[string]any{"type": "string", "enum": cap.Models}
					}
					if len(cap.Sizes) > 0 {
						props["size"] = map[string]any{"type": "string", "enum": cap.Sizes}
					}
				}
			}
		}
	}

	return result, nil
}

// AigoModels returns all known aigo models grouped by provider and capability.
func (rt *Runtime) AigoModels() map[string]map[string][]string {
	return aigotools.AvailableModels()
}

// AigoProviders returns all known providers with config schemas and models.
func (rt *Runtime) AigoProviders() []aigotools.ProviderInfo {
	return aigotools.AvailableProviders()
}

// AigoProviderStatus checks connectivity of configured aigo providers.
func (rt *Runtime) AigoProviderStatus() []aigotools.ProviderStatus {
	s := rt.Settings()
	if s == nil || s.Aigo == nil {
		return nil
	}
	return aigotools.CheckProviderConnectivity(s.Aigo.Providers)
}

// PersonaRegistry returns the persona registry (may be nil if no personas configured).
func (rt *Runtime) PersonaRegistry() *persona.Registry { return rt.personaRegistry }

// ProjectRoot returns the project root directory.
func (rt *Runtime) ProjectRoot() string { return rt.opts.ProjectRoot }

func (rt *Runtime) ConfigRoot() string { return rt.opts.ConfigRoot }

// SessionDB returns the SQLite session index store (may be nil).
func (rt *Runtime) SessionDB() *sessiondb.Store { return rt.sessionDB }

// ModelName returns the name of the currently active model.
func (rt *Runtime) ModelName() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if namer, ok := rt.opts.Model.(model.ModelNamer); ok {
		return namer.ModelName()
	}
	return ""
}

// SetModel hot-swaps the active model at runtime.
func (rt *Runtime) SetModel(ctx context.Context, modelName string) error {
	if strings.TrimSpace(modelName) == "" {
		return fmt.Errorf("model name is required")
	}

	// Detect provider from model name prefix.
	entry := config.FailoverModelEntry{Model: modelName}
	switch {
	case strings.HasPrefix(modelName, "gpt-") || strings.HasPrefix(modelName, "o1") || strings.HasPrefix(modelName, "o3") || strings.HasPrefix(modelName, "o4"):
		entry.Provider = "openai"
	default:
		entry.Provider = "anthropic"
	}

	newModel, err := rt.createModelFromEntry(entry)
	if err != nil {
		return fmt.Errorf("switch model to %s: %w", modelName, err)
	}

	rt.mu.Lock()
	rt.opts.Model = newModel
	rt.mu.Unlock()
	return nil
}

// ReloadSettings re-reads settings from disk and updates the runtime snapshot.
func (rt *Runtime) ReloadSettings() error {
	loader := &config.SettingsLoader{
		ProjectRoot: rt.opts.ProjectRoot,
		ConfigRoot:  rt.opts.ConfigRoot,
	}
	s, err := loader.Load()
	if err != nil {
		return err
	}

	// Rebuild persona registry and router from updated settings.
	newRegistry, newRouter := initPersonas(rt.opts, s)

	rt.mu.Lock()
	rt.settings = s
	rt.cfg = config.MergeSettings(nil, s)
	rt.personaRegistry = newRegistry
	rt.personaRouter = newRouter
	rt.mu.Unlock()
	return nil
}

// Sandbox exposes the sandbox manager.
func (rt *Runtime) Sandbox() *sandbox.Manager { return rt.sandbox }

// MemoryStore returns the session memory store, or nil if not configured.
func (rt *Runtime) MemoryStore() *memory.Store { return rt.memoryStore }

// GetSessionStats returns aggregated token stats for a session.
func (rt *Runtime) GetSessionStats(sessionID string) *SessionTokenStats {
	if rt == nil || rt.tokens == nil {
		return nil
	}
	return rt.tokens.GetSessionStats(sessionID)
}

// GetTotalStats returns aggregated token stats across all sessions.
func (rt *Runtime) GetTotalStats() *SessionTokenStats {
	if rt == nil || rt.tokens == nil {
		return nil
	}
	return rt.tokens.GetTotalStats()
}

// ListMonitors returns the status of all active stream monitors.
func (rt *Runtime) ListMonitors() []toolbuiltin.MonitorInfo {
	if rt == nil || rt.streamMonitor == nil {
		return nil
	}
	return rt.streamMonitor.ListMonitors()
}

// StreamMonitorTool returns the stream monitor tool instance, or nil if not registered.
func (rt *Runtime) StreamMonitorTool() *toolbuiltin.StreamMonitorTool {
	if rt == nil {
		return nil
	}
	return rt.streamMonitor
}

// ----------------- internal helpers -----------------

type preparedRun struct {
	ctx                 context.Context
	prompt              string
	contentBlocks       []model.ContentBlock
	history             *message.History
	normalized          Request
	recorder            *hookRecorder
	commandResults      []CommandExecution
	skillResults        []SkillExecution
	subagentResult      *subagents.Result
	mode                ModeContext
	toolWhitelist       map[string]struct{}
	detectedLanguage    string
	personaProfile      *persona.Profile
	personaSystemPrompt string
	personaPromptBlocks []string
	personaDisallowed   map[string]struct{}
	// maxIterationsOverride lets a code path force a specific cap for this
	// single run (used by the subagent runner to apply the
	// DefaultSubagentMaxIterations contract on top of the runtime-wide
	// MaxIterations). Zero falls back to rt.opts.MaxIterations; -1 means
	// explicit unlimited even if the runtime had a positive default.
	maxIterationsOverride int
}

type runResult struct {
	output *agent.ModelOutput
	usage  model.Usage
	reason string
}

func (rt *Runtime) prepare(ctx context.Context, req Request) (preparedRun, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fallbackSession := defaultSessionID(rt.mode.EntryPoint)
	normalized := req.normalized(rt.mode, fallbackSession)
	commandExists := func(string) bool { return false }
	if rt.cmdExec != nil {
		known := map[string]struct{}{}
		for _, def := range rt.cmdExec.List() {
			name := canonicalToolName(def.Name)
			if name != "" {
				known[name] = struct{}{}
			}
		}
		commandExists = func(name string) bool {
			_, ok := known[canonicalToolName(name)]
			return ok
		}
	}
	skillExists := func(string) bool { return false }
	if rt.skReg != nil {
		skillExists = func(name string) bool {
			_, ok := rt.skReg.Get(canonicalToolName(name))
			return ok
		}
	}
	parsedSkills, cleanedPrompt, missingSkills := extractPromptSkillInvocations(normalized.Prompt, skillExists, commandExists)
	if err := unknownForcedSkillsError(missingSkills); err != nil {
		return preparedRun{}, err
	}
	normalized.ForceSkills = mergeOrderedNames(normalized.ForceSkills, parsedSkills)
	normalized.Prompt = cleanedPrompt
	prompt := strings.TrimSpace(normalized.Prompt)
	if prompt == "" && len(normalized.ContentBlocks) == 0 && len(normalized.ForceSkills) == 0 {
		return preparedRun{}, errors.New("api: prompt is empty")
	}

	if normalized.SessionID == "" {
		normalized.SessionID = fallbackSession
	}

	// Auto-generate RequestID if not provided (UUID tracking)
	if normalized.RequestID == "" {
		normalized.RequestID = uuid.New().String()
	}

	history := rt.histories.Get(normalized.SessionID)
	logging.From(ctx).Debug("prepare", "session_id", normalized.SessionID, "request_id", normalized.RequestID, "history_len", history.Len(), "force_skills", len(normalized.ForceSkills))

	// Fork parent session's history if requested and child is empty.
	if normalized.ParentSessionID != "" && history.Len() == 0 {
		parentHistory := rt.histories.Get(normalized.ParentSessionID)
		for _, msg := range parentHistory.All() {
			history.Append(msg)
		}
	}

	recorder := defaultHookRecorder()

	if rt.compactor != nil {
		if _, _, err := rt.compactor.maybeCompact(ctx, history, normalized.SessionID, recorder); err != nil {
			return preparedRun{}, err
		}
	}

	activation := normalized.activationContext(prompt)

	cmdRes, cleanPrompt, err := rt.executeCommands(ctx, prompt, &normalized)
	if err != nil {
		return preparedRun{}, err
	}
	prompt = cleanPrompt
	activation.Prompt = prompt

	skillRes, promptAfterSkills, err := rt.executeSkills(ctx, prompt, activation, &normalized)
	if err != nil {
		return preparedRun{}, err
	}
	prompt = promptAfterSkills
	activation.Prompt = prompt
	subRes, promptAfterSubagent, err := rt.executeSubagent(ctx, prompt, activation, &normalized)
	if err != nil {
		return preparedRun{}, err
	}
	prompt = promptAfterSubagent
	activation.Prompt = prompt
	whitelist := combineToolWhitelists(normalized.ToolWhitelist, nil)

	// Auto-detect language from user prompt when no explicit language is configured
	// or the default English is in use.
	var detectedLang string
	if rt.opts.Language == "" || rt.opts.Language == "English" {
		detectedLang = detectLanguage(normalized.Prompt)
	}

	// Resolve persona and build persona-specific system prompt.
	var personaProf *persona.Profile
	var personaSysPrompt string
	var personaBlocks []string
	var personaDisallowed map[string]struct{}
	if rt.personaRegistry != nil {
		personaProf = resolveRequestPersona(normalized, rt.personaRegistry, rt.personaRouter)
		if personaProf != nil {
			personaSysPrompt, personaBlocks = buildPersonaSystemPrompt(
				rt.opts.SystemPrompt, rt.systemPromptBlocks, personaProf, rt.opts.ProjectRoot, rt.personaRegistry,
			)
			// Override language if persona specifies one.
			if personaProf.Language != "" {
				detectedLang = personaProf.Language
			}
			// Scope session ID for history isolation.
			normalized.SessionID = persona.ScopedSessionID(personaProf.ID, normalized.SessionID)
			// Merge persona's enabled tools into whitelist.
			if len(personaProf.EnabledTools) > 0 {
				whitelist = combineToolWhitelists(normalized.ToolWhitelist, personaProf.EnabledTools)
			}
			// Build disallowed tools set.
			if len(personaProf.DisallowedTools) > 0 {
				personaDisallowed = make(map[string]struct{}, len(personaProf.DisallowedTools))
				for _, t := range personaProf.DisallowedTools {
					personaDisallowed[canonicalToolName(t)] = struct{}{}
				}
			}
		}
	}

	return preparedRun{
		ctx:                 ctx,
		prompt:              prompt,
		contentBlocks:       normalized.ContentBlocks,
		history:             history,
		normalized:          normalized,
		recorder:            recorder,
		commandResults:      cmdRes,
		skillResults:        skillRes,
		subagentResult:      subRes,
		mode:                normalized.Mode,
		toolWhitelist:       whitelist,
		detectedLanguage:    detectedLang,
		personaProfile:      personaProf,
		personaSystemPrompt: personaSysPrompt,
		personaPromptBlocks: personaBlocks,
		personaDisallowed:   personaDisallowed,
	}, nil
}

func (rt *Runtime) runAgent(prep preparedRun) (runResult, error) {
	return rt.runAgentWithMiddleware(prep)
}

func (rt *Runtime) runAgentWithMiddleware(prep preparedRun, extras ...middleware.Middleware) (runResult, error) {
	logger := logging.From(prep.ctx)

	// Select model based on request tier or subagent mapping
	selectedModel, selectedTier := rt.selectModelForSubagent(prep.normalized.TargetSubagent, prep.normalized.Model)

	// Emit ModelSelected event if a non-default model was selected
	if selectedTier != "" {
		hookAdapter := &runtimeHookAdapter{executor: rt.hooks, recorder: prep.recorder}
		// Best-effort event emission; errors are logged but don't block execution
		if err := hookAdapter.ModelSelected(prep.ctx, coreevents.ModelSelectedPayload{
			ToolName:  prep.normalized.TargetSubagent,
			ModelTier: string(selectedTier),
			Reason:    "subagent model mapping",
		}); err != nil {
			logger.Warn("api: failed to emit ModelSelected event", "error", err)
		}
	}

	// Wrap with failover if configured
	selectedModel = rt.wrapWithFailover(selectedModel)

	// Determine cache enablement: request-level overrides global default
	enableCache := rt.opts.DefaultEnableCache
	if prep.normalized.EnablePromptCache != nil {
		enableCache = *prep.normalized.EnablePromptCache
	}

	hookAdapter := &runtimeHookAdapter{executor: rt.hooks, recorder: prep.recorder}

	// Override model when persona specifies one.
	if prep.personaProfile != nil && prep.personaProfile.Model != "" {
		entry := config.FailoverModelEntry{Model: prep.personaProfile.Model}
		switch {
		case strings.HasPrefix(prep.personaProfile.Model, "gpt-") ||
			strings.HasPrefix(prep.personaProfile.Model, "o1") ||
			strings.HasPrefix(prep.personaProfile.Model, "o3") ||
			strings.HasPrefix(prep.personaProfile.Model, "o4"):
			entry.Provider = "openai"
		default:
			entry.Provider = "anthropic"
		}
		if personaModel, err := rt.createModelFromEntry(entry); err == nil {
			selectedModel = personaModel
		} else {
			slog.Warn("persona model override warning", "error", err)
		}
	}

	// Use persona-specific system prompt when a persona is active.
	sysPrompt := rt.opts.SystemPrompt
	sysBlocks := rt.systemPromptBlocks
	if prep.personaSystemPrompt != "" {
		sysPrompt = prep.personaSystemPrompt
		sysBlocks = prep.personaPromptBlocks
	}

	// Build tool definitions, filtering out persona-disallowed tools.
	toolDefs := availableTools(rt.registry, prep.toolWhitelist)
	if len(prep.personaDisallowed) > 0 {
		filtered := make([]model.ToolDefinition, 0, len(toolDefs))
		for _, td := range toolDefs {
			if _, blocked := prep.personaDisallowed[canonicalToolName(td.Name)]; !blocked {
				filtered = append(filtered, td)
			}
		}
		toolDefs = filtered
	}

	modelAdapter := &conversationModel{
		base:               selectedModel,
		history:            prep.history,
		prompt:             prep.prompt,
		contentBlocks:      prep.contentBlocks,
		trimmer:            rt.newTrimmer(),
		tools:              toolDefs,
		systemPrompt:       sysPrompt,
		systemPromptBlocks: sysBlocks,
		outputSchema:       effectiveOutputSchema(prep.normalized.OutputSchema, rt.opts.OutputSchema),
		outputMode:         effectiveOutputSchemaMode(prep.normalized.OutputSchemaMode, rt.opts.OutputSchemaMode),
		rulesLoader:        rt.rulesLoader,
		enableCache:        enableCache,
		maxOutputTokens:    rt.opts.MaxOutputTokens,
		hooks:              hookAdapter,
		recorder:           prep.recorder,
		compactor:          rt.compactor,
		sessionID:          prep.normalized.SessionID,
		detectedLanguage:   prep.detectedLanguage,
	}

	toolExec := &runtimeToolExecutor{
		executor:           rt.executor,
		hooks:              hookAdapter,
		history:            prep.history,
		allow:              prep.toolWhitelist,
		root:               rt.sbRoot,
		host:               "localhost",
		sessionID:          prep.normalized.SessionID,
		yolo:               rt.opts.DangerouslySkipPermissions,
		permissionResolver: buildPermissionResolver(hookAdapter, rt.opts.PermissionRequestHandler, rt.opts.ApprovalQueue, rt.opts.ApprovalApprover, rt.opts.ApprovalWhitelistTTL, rt.opts.ApprovalWait),
	}

	chainItems := make([]middleware.Middleware, 0, 3+len(rt.opts.Middleware)+len(extras))
	chainItems = append(chainItems, newSafetyMiddleware())
	chainItems = append(chainItems, newSubdirHintsMiddleware(rt.sbRoot))
	chainItems = append(chainItems, middleware.NewErrorClassifier())
	if rt.memoryStore != nil {
		chainItems = append(chainItems, middleware.NewMemoryNudge(middleware.MemoryNudgeConfig{
			Store:       rt.memoryStore,
			EveryNTurns: 5,
		}))
	}
	if len(rt.opts.Middleware) > 0 {
		chainItems = append(chainItems, rt.opts.Middleware...)
	}
	if len(extras) > 0 {
		chainItems = append(chainItems, extras...)
	}
	chain := middleware.NewChain(chainItems, middleware.WithTimeout(rt.opts.MiddlewareTimeout))

	logger.Info("agent run starting",
		"session_id", prep.normalized.SessionID,
		"model_tier", string(selectedTier),
		"middleware_count", len(chainItems),
		"max_iterations", rt.opts.MaxIterations,
	)
	agentStart := time.Now()

	// Resolve the canonical model name for budget tracking. Empty when the
	// provider doesn't implement ModelNamer; in that case MaxBudgetUSD is
	// inert (the agent guard requires a name to look up pricing).
	budgetModelName := ""
	if namer, ok := selectedModel.(model.ModelNamer); ok {
		budgetModelName = namer.ModelName()
	}
	// Per-run iteration cap: subagents and other internal call sites can
	// override the runtime-wide default by setting maxIterationsOverride on
	// the preparedRun. Zero falls back to rt.opts.MaxIterations.
	maxIters := rt.opts.MaxIterations
	if prep.maxIterationsOverride != 0 {
		maxIters = prep.maxIterationsOverride
	}
	ag, err := agent.New(modelAdapter, toolExec, agent.Options{
		MaxIterations: maxIters,
		Timeout:       rt.opts.Timeout,
		Middleware:    chain,
		MaxBudgetUSD:  rt.opts.MaxBudgetUSD,
		MaxTokens:     rt.opts.MaxTokens,
		ModelName:     budgetModelName,
	})
	if err != nil {
		return runResult{}, err
	}

	agentCtx := agent.NewContext()
	if sessionID := strings.TrimSpace(prep.normalized.SessionID); sessionID != "" {
		agentCtx.Values["session_id"] = sessionID
	}
	// Propagate RequestID through agent context for distributed tracing
	if requestID := strings.TrimSpace(prep.normalized.RequestID); requestID != "" {
		agentCtx.Values["request_id"] = requestID
	}
	if len(prep.normalized.ForceSkills) > 0 {
		agentCtx.Values["request.force_skills"] = append([]string(nil), prep.normalized.ForceSkills...)
	}
	if rt.skReg != nil {
		agentCtx.Values["skills.registry"] = rt.skReg
	}
	out, err := ag.Run(prep.ctx, agentCtx)
	if err != nil {
		logger.Error("agent run failed", "session_id", prep.normalized.SessionID, "error", err, "duration_ms", time.Since(agentStart).Milliseconds())
		return runResult{}, err
	}
	logger.Info("agent run completed",
		"session_id", prep.normalized.SessionID,
		"duration_ms", time.Since(agentStart).Milliseconds(),
		"stop_reason", modelAdapter.stopReason,
		"input_tokens", modelAdapter.usage.InputTokens,
		"output_tokens", modelAdapter.usage.OutputTokens,
	)
	result := runResult{output: out, usage: modelAdapter.usage, reason: modelAdapter.stopReason}
	result = rt.applyOutputSchema(prep.ctx, selectedModel, prep.history, modelAdapter.outputSchema, modelAdapter.outputMode, result)
	if rt.tokens != nil && rt.tokens.IsEnabled() {
		modelName := ""
		if namer, ok := selectedModel.(model.ModelNamer); ok {
			modelName = namer.ModelName()
		}
		stats := tokenStatsFromUsage(result.usage, modelName, prep.normalized.SessionID, prep.normalized.RequestID)
		rt.tokens.Record(stats)
		payload := coreevents.TokenUsagePayload{
			InputTokens:   stats.InputTokens,
			OutputTokens:  stats.OutputTokens,
			TotalTokens:   stats.TotalTokens,
			CacheCreation: stats.CacheCreation,
			CacheRead:     stats.CacheRead,
			Model:         stats.Model,
			SessionID:     stats.SessionID,
			RequestID:     stats.RequestID,
		}
		if rt.hooks != nil {
			//nolint:errcheck // token usage events are non-critical notifications
			rt.hooks.Publish(coreevents.Event{
				Type:      coreevents.TokenUsage,
				SessionID: stats.SessionID,
				RequestID: stats.RequestID,
				Payload:   payload,
			})
		}
		if prep.recorder != nil {
			prep.recorder.Record(coreevents.Event{
				Type:      coreevents.TokenUsage,
				SessionID: stats.SessionID,
				RequestID: stats.RequestID,
				Payload:   payload,
			})
		}
	}
	// Async skill learning from completed tasks.
	if rt.skillLearner != nil && result.reason == "end_turn" && result.output != nil {
		out := result.output
		var toolSummaries []skills.ToolCallSummary
		for _, tc := range out.ToolCalls {
			toolSummaries = append(toolSummaries, skills.ToolCallSummary{
				Name:   tc.Name,
				Params: truncateString(fmt.Sprintf("%v", tc.Input), 60),
			})
		}
		learnerInput := skills.LearningInput{
			SessionID: prep.normalized.SessionID,
			Prompt:    prep.normalized.Prompt,
			Output:    out.Content,
			ToolCalls: toolSummaries,
			TurnCount: int(result.usage.InputTokens+result.usage.OutputTokens) / 1000, // rough proxy
			Success:   true,
		}
		go func() {
			if err := rt.skillLearner.Learn(learnerInput); err != nil {
				logger.Warn("skill learner", "error", err)
			}
		}()
	}

	return result, nil
}

func (rt *Runtime) buildResponse(prep preparedRun, result runResult) *Response {
	events := []coreevents.Event(nil)
	if prep.recorder != nil {
		events = prep.recorder.Drain()
	}
	settings := rt.Settings()
	resp := &Response{
		Mode:            prep.mode,
		RequestID:       prep.normalized.RequestID,
		Result:          convertRunResult(result),
		CommandResults:  prep.commandResults,
		SkillResults:    prep.skillResults,
		Subagent:        prep.subagentResult,
		HookEvents:      events,
		ProjectConfig:   settings,
		Settings:        settings,
		SandboxSnapshot: rt.sandboxReport(),
		Tags:            maps.Clone(prep.normalized.Tags),
	}
	return resp
}

func (rt *Runtime) sandboxReport() SandboxReport {
	report := snapshotSandbox(rt.sandbox)

	var roots []string
	if root := strings.TrimSpace(rt.sbRoot); root != "" {
		roots = append(roots, root)
	}
	report.Roots = normalizeStrings(roots)

	allowed := make([]string, 0, len(rt.opts.Sandbox.AllowedPaths))
	for _, path := range rt.opts.Sandbox.AllowedPaths {
		if clean := strings.TrimSpace(path); clean != "" {
			allowed = append(allowed, clean)
		}
	}
	for _, path := range additionalSandboxPaths(rt.settings) {
		if clean := strings.TrimSpace(path); clean != "" {
			allowed = append(allowed, clean)
		}
	}
	report.AllowedPaths = normalizeStrings(allowed)

	domains := rt.opts.Sandbox.NetworkAllow
	if len(domains) == 0 {
		domains = defaultNetworkAllowList()
	}
	var cleanedDomains []string
	for _, domain := range domains {
		if host := strings.TrimSpace(domain); host != "" {
			cleanedDomains = append(cleanedDomains, host)
		}
	}
	report.AllowedDomains = normalizeStrings(cleanedDomains)
	return report
}

func convertRunResult(res runResult) *Result {
	if res.output == nil {
		return nil
	}
	toolCalls := make([]model.ToolCall, len(res.output.ToolCalls))
	for i, call := range res.output.ToolCalls {
		toolCalls[i] = model.ToolCall{Name: call.Name, Arguments: call.Input}
	}
	// StopReason precedence:
	//   1. Agent-level structured reason (max_budget, max_tokens, repeat_loop,
	//      aborted_*, max_iterations) — these are decisions the loop owns and
	//      should not be hidden by a "stop" coming back from the model.
	//   2. Otherwise, the conversation model's reason (the provider's own
	//      "end_turn" / "stop" / "tool_use" string) — preserves back-compat
	//      with callers that key off these well-known values.
	stopReason := res.reason
	if agentReason := string(res.output.StopReason); agentReason != "" && agentReason != string(agent.StopReasonCompleted) {
		stopReason = agentReason
	} else if stopReason == "" && agentReason != "" {
		stopReason = agentReason
	}
	return &Result{
		Output:     res.output.Content,
		ToolCalls:  toolCalls,
		Usage:      res.usage,
		StopReason: stopReason,
	}
}

func (rt *Runtime) executeCommands(ctx context.Context, prompt string, req *Request) ([]CommandExecution, string, error) {
	if rt.cmdExec == nil {
		return nil, prompt, nil
	}
	invocations, err := commands.Parse(prompt)
	if err != nil {
		if errors.Is(err, commands.ErrNoCommand) {
			return nil, prompt, nil
		}
		return nil, "", err
	}
	cleanPrompt := removeCommandLines(prompt, invocations)
	results, err := rt.cmdExec.Execute(ctx, invocations)
	if err != nil {
		return nil, "", err
	}
	execs := make([]CommandExecution, 0, len(results))
	for _, res := range results {
		def := definitionSnapshot(rt.cmdExec, res.Command)
		execs = append(execs, CommandExecution{Definition: def, Result: res})
		cleanPrompt = applyPromptMetadata(cleanPrompt, res.Metadata)
		mergeTags(req, res.Metadata)
		applyCommandMetadata(req, res.Metadata)
	}
	return execs, cleanPrompt, nil
}

func (rt *Runtime) executeSkills(ctx context.Context, prompt string, activation skills.ActivationContext, req *Request) ([]SkillExecution, string, error) {
	if rt.skReg == nil {
		return nil, prompt, nil
	}
	matches := rt.skReg.Match(activation)
	forced := orderedForcedSkills(rt.skReg, req.ForceSkills)
	matches = append(matches, forced...)
	if len(matches) == 0 {
		return nil, prompt, nil
	}
	prefix := ""
	execs := make([]SkillExecution, 0, len(matches))
	seen := map[string]struct{}{}
	for _, match := range matches {
		skill := match.Skill
		if skill == nil {
			continue
		}
		name := skill.Definition().Name
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		logging.From(ctx).Info("executing skill", "name", name, "score", match.Score, "reason", match.Reason)
		execStart := time.Now()
		res, err := skill.Execute(ctx, activation)
		execDuration := time.Since(execStart)
		execs = append(execs, SkillExecution{Definition: skill.Definition(), Result: res, Err: err, MatchReason: match.Reason})
		if rt.skillTracker != nil {
			rec := skills.SkillActivationRecord{
				Skill:      name,
				Scope:      skill.Definition().Metadata["skill.scope"],
				Source:     skills.ParseSource(match.Reason),
				Score:      match.Score,
				SessionID:  req.SessionID,
				Success:    err == nil,
				DurationMs: execDuration.Milliseconds(),
				Timestamp:  execStart,
			}
			if err != nil {
				rec.Error = err.Error()
			}
			if outMap, ok := res.Output.(map[string]string); ok {
				if body, ok := outMap["body"]; ok {
					rec.TokenUsage = len(body) / 4
				}
			}
			rt.skillTracker.Record(rec)
		}
		if err != nil {
			logging.From(ctx).Error("skill execution failed", "name", name, "error", err)
			return execs, "", err
		}
		prefix = combinePrompt(prefix, res.Output)
		activation.Metadata = mergeMetadata(activation.Metadata, res.Metadata)
		mergeTags(req, res.Metadata)
		applyCommandMetadata(req, res.Metadata)
	}
	prompt = prependPrompt(prompt, prefix)
	prompt = applyPromptMetadata(prompt, activation.Metadata)
	return execs, prompt, nil
}
