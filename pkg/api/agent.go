package api

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	acpclient "github.com/cinience/saker/pkg/acp/client"
	"github.com/cinience/saker/pkg/config"
	corehooks "github.com/cinience/saker/pkg/core/hooks"
	"github.com/cinience/saker/pkg/logging"
	"github.com/cinience/saker/pkg/memory"
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

