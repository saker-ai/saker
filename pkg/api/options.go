// Package api wires the public SDK surface. The Options struct lives here so
// callers can grep for it; supporting types/helpers are split across sibling
// files (options_*.go, request_response.go) to keep this file focused on the
// user-facing knob inventory.
package api

import (
	"errors"
	"io/fs"
	"time"

	"github.com/cinience/saker/pkg/config"
	coremw "github.com/cinience/saker/pkg/core/eventmw"
	corehooks "github.com/cinience/saker/pkg/core/hooks"
	"github.com/cinience/saker/pkg/middleware"
	"github.com/cinience/saker/pkg/model"
	runtimecache "github.com/cinience/saker/pkg/runtime/cache"
	"github.com/cinience/saker/pkg/runtime/checkpoint"
	"github.com/cinience/saker/pkg/runtime/tasks"
	"github.com/cinience/saker/pkg/security"
	"github.com/cinience/saker/pkg/tool"
)

var (
	// ErrMissingModel is returned when a Runtime is created without a model factory.
	ErrMissingModel = errors.New("api: model factory is required")
	// ErrConcurrentExecution is returned when Run is called on a session that is already executing.
	ErrConcurrentExecution = errors.New("concurrent execution on same session is not allowed")
	// ErrRuntimeClosed is returned when Run is called after the Runtime has been closed.
	ErrRuntimeClosed = errors.New("api: runtime is closed")
	// ErrToolUseDenied is returned when a hook denies a tool execution.
	ErrToolUseDenied = errors.New("api: tool use denied by hook")
	// ErrToolUseRequiresApproval is returned when a tool execution requires user approval.
	ErrToolUseRequiresApproval = errors.New("api: tool use requires approval")
)

// Options configures the unified SDK runtime.
type Options struct {
	EntryPoint  EntryPoint
	Mode        ModeContext
	ProjectRoot string
	// ConfigRoot is the directory containing declarative runtime files.
	// Defaults to "<ProjectRoot>/.saker" when unset.
	ConfigRoot string
	// EmbedFS 可选的嵌入文件系统，用于支持将 .saker 目录打包到二进制
	// 当设置时，文件加载优先级为：OS 文件系统 > 嵌入 FS
	// 这允许运行时通过创建本地文件来覆盖嵌入的默认配置
	EmbedFS           fs.FS
	SettingsPath      string
	SettingsOverrides *config.Settings
	SettingsLoader    *config.SettingsLoader

	Model        model.Model
	ModelFactory ModelFactory

	// ModelPool maps tiers to model instances for cost optimization.
	// Use ModelTier constants (ModelTierLow, ModelTierMid, ModelTierHigh) as keys.
	ModelPool map[ModelTier]model.Model
	// SubagentModelMapping maps subagent type names to model tiers.
	// Keys should be lowercase subagent types: "general-purpose", "explore", "plan".
	// Subagents not in this map use the default Model.
	SubagentModelMapping map[string]ModelTier

	// ContextWindowTokens is the model's context window size in tokens.
	// Used to budget skill listing size (1% of context window in characters).
	// When 0, defaults to 8000 characters (~200k token model assumption).
	ContextWindowTokens int

	// DefaultEnableCache sets the default prompt caching behavior for all requests.
	// Individual requests can override this via Request.EnablePromptCache.
	// Prompt caching reduces costs for repeated context (system prompts, conversation history).
	DefaultEnableCache bool

	// OutputSchema constrains model text responses when the provider supports structured output.
	OutputSchema *model.ResponseFormat
	// OutputSchemaMode controls whether OutputSchema is applied inline during the
	// agent loop or via a separate post-processing pass. Empty defaults to inline.
	OutputSchemaMode OutputSchemaMode

	SystemPrompt string
	// Language sets the preferred response language (e.g., "Chinese", "English").
	// When non-empty, the agent is instructed to respond in this language.
	Language     string
	RulesEnabled *bool // nil = 默认启用，false = 禁用

	Middleware        []middleware.Middleware
	MiddlewareTimeout time.Duration
	// MaxIterations caps the agent loop. Zero falls through to the
	// surface-appropriate default chosen by withDefaults() (CLI: 30, CI: 50,
	// Platform: unlimited). Set to -1 for explicit unlimited regardless of
	// EntryPoint.
	MaxIterations int
	Timeout       time.Duration
	TokenLimit    int
	// MaxOutputTokens caps the model's per-iteration output (req.MaxTokens).
	// When 0 (default) the SDK lets the provider decide, matching pre-cap
	// behavior. Set this when you want to bound a runaway model — see the
	// runaway-generation warning emitted by conversationModel.Generate.
	MaxOutputTokens int
	// MaxBudgetUSD aborts the agent loop with StopReason "max_budget" when
	// cumulative estimated cost reaches this value (US dollars). Zero
	// disables the check. Pricing is resolved by model.EstimateCost using
	// the configured model name; unknown models silently disable the cap.
	MaxBudgetUSD float64
	// MaxTokens aborts the agent loop with StopReason "max_tokens" when
	// cumulative input+output tokens reach this value. Zero disables.
	// Distinct from MaxOutputTokens, which caps a single model call.
	MaxTokens   int
	MaxSessions int

	Tools []tool.Tool

	// TaskStore overrides the default in-memory task store used by task_* built-ins.
	// When nil, runtime creates and owns a fresh in-memory store.
	// When provided, ownership remains with the caller.
	TaskStore tasks.Store

	// EnabledBuiltinTools controls which built-in tools are registered when Options.Tools is empty.
	// - nil (default): register all built-ins to preserve current behavior
	// - empty slice: disable all built-in tools
	// - non-empty: enable only the listed built-ins (case-insensitive).
	// If Tools is non-empty, this whitelist is ignored in favor of the legacy Tools override.
	// Available built-in names include: bash, file_read, image_read, file_write, grep, glob.
	EnabledBuiltinTools []string

	// DisallowedTools is a blacklist of tool names (case-insensitive) that will not be registered.
	DisallowedTools []string

	// DisabledSkills lists skill names to exclude from activation and listing.
	DisabledSkills []string

	// CustomTools appends caller-supplied tool.Tool implementations to the selected built-ins
	// when Tools is empty. Ignored when Tools is non-empty (legacy override takes priority).
	CustomTools []tool.Tool
	MCPServers  []string

	TypedHooks     []corehooks.ShellHook
	HookMiddleware []coremw.Middleware
	HookTimeout    time.Duration

	Skills    []SkillRegistration
	Commands  []CommandRegistration
	Subagents []SubagentRegistration
	// SkillsDirs overrides skill discovery roots. Paths may be absolute, or
	// relative to ProjectRoot. When empty, defaults to "<ConfigRoot>/skills".
	SkillsDirs []string
	// SkillsRecursive controls recursive SKILL.md discovery. Nil defaults to true.
	SkillsRecursive *bool

	Sandbox SandboxOptions

	// TokenTracking enables token usage statistics collection.
	// When true, the runtime tracks input/output tokens per session and model.
	TokenTracking bool
	// TokenCallback is called synchronously after token usage is recorded.
	// Only called when TokenTracking is true. The callback should be lightweight
	// and non-blocking to avoid delaying the agent execution. If you need async
	// processing, spawn a goroutine inside the callback.
	TokenCallback TokenCallback

	// PermissionRequestHandler handles sandbox PermissionAsk decisions. Returning
	// PermissionAllow continues tool execution; PermissionDeny rejects it; PermissionAsk
	// leaves the request pending.
	PermissionRequestHandler PermissionRequestHandler
	// ApprovalQueue optionally persists permission decisions and supports session whitelists.
	ApprovalQueue *security.ApprovalQueue
	// ApprovalApprover labels approvals/denials stored in ApprovalQueue.
	ApprovalApprover string
	// ApprovalWhitelistTTL controls session whitelist duration for approvals.
	ApprovalWhitelistTTL time.Duration
	// ApprovalWait blocks tool execution until a pending approval is resolved.
	ApprovalWait bool

	// CheckpointStore persists resumable state for pipeline-backed runs.
	CheckpointStore checkpoint.Store
	// CacheStore persists step-level cached results for pipeline-backed runs.
	CacheStore runtimecache.Store

	// MemoryDir is the directory for session memory persistence.
	// When non-empty, enables memory_save/memory_read tools and system prompt injection.
	// Relative paths are resolved against ProjectRoot.
	MemoryDir string

	// CanvasDir is the directory containing per-thread canvas JSON files
	// ("{threadID}.json" written by the web/server canvas RPCs). When non-empty,
	// the canvas_get_node built-in tool is enabled and resolves files from here.
	// Server mode sets this to "<dataDir>/canvas"; CLI leaves it empty.
	CanvasDir string

	// PersonasDir overrides the persona file discovery root.
	// Defaults to "<ConfigRoot>/personas" when empty.
	PersonasDir string
	// DefaultPersona is the fallback persona ID when no routing matches.
	DefaultPersona string

	// AutoCompact enables automatic context compaction for long sessions.
	AutoCompact CompactConfig

	// OTEL configures OpenTelemetry distributed tracing.
	// Requires build tag 'otel' for actual instrumentation; otherwise no-op.
	OTEL OTELConfig

	// DangerouslySkipPermissions disables all tool whitelisting and permission
	// checks. When true, every tool call is allowed without approval.
	// Default: true (saker maximizes agent autonomy by default).
	DangerouslySkipPermissions bool

	// ACPAgents configures external ACP agent targets. When non-empty, subagent
	// spawn requests matching a registered target name are routed to an external
	// agent process via the ACP protocol instead of the in-process runner.
	ACPAgents map[string]config.ACPAgentEntry

	fsLayer *config.FS
}
