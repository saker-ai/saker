package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/agent"
	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/config"
	coremw "github.com/cinience/saker/pkg/core/eventmw"
	coreevents "github.com/cinience/saker/pkg/core/events"
	corehooks "github.com/cinience/saker/pkg/core/hooks"
	"github.com/cinience/saker/pkg/middleware"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/pipeline"
	runtimecache "github.com/cinience/saker/pkg/runtime/cache"
	"github.com/cinience/saker/pkg/runtime/checkpoint"
	"github.com/cinience/saker/pkg/runtime/commands"
	"github.com/cinience/saker/pkg/runtime/skills"
	"github.com/cinience/saker/pkg/runtime/subagents"
	"github.com/cinience/saker/pkg/runtime/tasks"
	"github.com/cinience/saker/pkg/sandbox"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
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

type EntryPoint string

const (
	EntryPointCLI      EntryPoint = "cli"
	EntryPointCI       EntryPoint = "ci"
	EntryPointPlatform EntryPoint = "platform"
	defaultEntrypoint             = EntryPointCLI
	defaultMaxSessions            = 1000
)

// ModelTier represents cost-based model classification for optimization.
type ModelTier string

const (
	ModelTierLow  ModelTier = "low"  // Low cost: Haiku
	ModelTierMid  ModelTier = "mid"  // Mid cost: Sonnet
	ModelTierHigh ModelTier = "high" // High cost: Opus
)

// OutputSchemaMode controls when OutputSchema is applied during an agent run.
type OutputSchemaMode string

const (
	// OutputSchemaModeInline preserves the historical behavior by forwarding
	// ResponseFormat on each agent-loop model call.
	OutputSchemaModeInline OutputSchemaMode = "inline"
	// OutputSchemaModePostProcess runs the loop without ResponseFormat and, if
	// needed, performs one extra formatting pass on the final text output.
	OutputSchemaModePostProcess OutputSchemaMode = "post_process"
)

// CLIContext captures optional metadata supplied by the CLI surface.
type CLIContext struct {
	User      string
	Workspace string
	Args      []string
	Flags     map[string]string
}

// CIContext captures CI/CD metadata for parameter matrix validation.
type CIContext struct {
	Provider string
	Pipeline string
	RunID    string
	SHA      string
	Ref      string
	Matrix   map[string]string
	Metadata map[string]string
}

// PlatformContext captures enterprise platform metadata such as org/project.
type PlatformContext struct {
	Organization string
	Project      string
	Environment  string
	Labels       map[string]string
}

// ModeContext binds an entrypoint to optional contextual metadata blocks.
type ModeContext struct {
	EntryPoint EntryPoint
	CLI        *CLIContext
	CI         *CIContext
	Platform   *PlatformContext
}

// SandboxOptions mirrors sandbox.Manager construction knobs exposed at the API
// layer so callers can customise filesystem/network/resource guards without
// touching lower-level packages.
type SandboxOptions struct {
	Type          string
	Root          string
	AllowedPaths  []string
	NetworkAllow  []string
	ResourceLimit sandbox.ResourceLimits
	GVisor        *sandboxenv.GVisorOptions
	Govm          *sandboxenv.GovmOptions
	Landlock      *sandboxenv.LandlockOptions
}

type GVisorOptions = sandboxenv.GVisorOptions
type GovmOptions = sandboxenv.GovmOptions
type LandlockOptions = sandboxenv.LandlockOptions
type MountSpec = sandboxenv.MountSpec

// PermissionRequest captures a permission prompt for sandbox "ask" matches.
type PermissionRequest struct {
	ToolName   string
	ToolParams map[string]any
	SessionID  string
	Rule       string
	Target     string
	Reason     string
	Approval   *security.ApprovalRecord
}

// PermissionRequestHandler lets hosts synchronously allow/deny PermissionAsk decisions.
type PermissionRequestHandler func(context.Context, PermissionRequest) (coreevents.PermissionDecisionType, error)

// SkillRegistration wires runtime skill definitions + handlers.
type SkillRegistration struct {
	Definition skills.Definition
	Handler    skills.Handler
}

// CommandRegistration wires slash command definitions + handlers.
type CommandRegistration struct {
	Definition commands.Definition
	Handler    commands.Handler
}

// SubagentRegistration wires runtime subagents into the dispatcher.
type SubagentRegistration struct {
	Definition subagents.Definition
	Handler    subagents.Handler
}

// ModelFactory allows callers to supply arbitrary model implementations.
type ModelFactory interface {
	Model(ctx context.Context) (model.Model, error)
}

// ModelFactoryFunc turns a function into a ModelFactory.
type ModelFactoryFunc func(context.Context) (model.Model, error)

// Model implements ModelFactory.
func (fn ModelFactoryFunc) Model(ctx context.Context) (model.Model, error) {
	if fn == nil {
		return nil, ErrMissingModel
	}
	return fn(ctx)
}

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
	// behaviour. Set this when you want to bound a runaway model — see the
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
	// - nil (default): register all built-ins to preserve current behaviour
	// - empty slice: disable all built-in tools
	// - non-empty: enable only the listed built-ins (case-insensitive).
	// If Tools is non-empty, this whitelist is ignored in favour of the legacy Tools override.
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
	// Default: true (saker maximises agent autonomy by default).
	DangerouslySkipPermissions bool

	// ACPAgents configures external ACP agent targets. When non-empty, subagent
	// spawn requests matching a registered target name are routed to an external
	// agent process via the ACP protocol instead of the in-process runner.
	ACPAgents map[string]config.ACPAgentEntry

	fsLayer *config.FS
}

// DefaultSubagentDefinitions exposes the built-in subagent type catalog so
// callers can seed api.Options.Subagents or extend the metadata when wiring
// custom handlers.
func DefaultSubagentDefinitions() []subagents.Definition {
	return subagents.BuiltinDefinitions()
}

// Request captures a single logical run invocation. Tags/T traits/Channels are
// forwarded to the declarative runtime layers (skills/subagents) while
// RunContext overrides the agent-level execution knobs.
type Request struct {
	Prompt               string
	ContentBlocks        []model.ContentBlock // Multimodal content; when non-empty, used alongside Prompt
	Pipeline             *pipeline.Step
	Mode                 ModeContext
	SessionID            string
	ParentSessionID      string // If set, fork parent session's history into this new session
	Ephemeral            bool   // If true, session history is not persisted to disk
	ResumeFromCheckpoint string
	RequestID            string    `json:"request_id,omitempty"` // Auto-generated UUID or user-provided
	Model                ModelTier // Optional: override model tier for this request
	EnablePromptCache    *bool     // Optional: enable prompt caching (nil uses global default)
	OutputSchema         *model.ResponseFormat
	OutputSchemaMode     OutputSchemaMode
	Traits               []string
	Tags                 map[string]string
	Channels             []string
	Metadata             map[string]any
	TargetSubagent       string
	ToolWhitelist        []string
	ForceSkills          []string
	User                 string // Authenticated username (set by server for per-user isolation)
	UserRole             string // User role: "admin" or "user"
}

// Response aggregates the final agent result together with metadata emitted
// by the unified runtime pipeline (skills/commands/hooks/etc.).
type Response struct {
	Mode           ModeContext
	RequestID      string `json:"request_id,omitempty"` // UUID for distributed tracing
	Result         *Result
	Timeline       []TimelineEntry
	SkillResults   []SkillExecution
	CommandResults []CommandExecution
	Subagent       *subagents.Result
	HookEvents     []coreevents.Event
	// Deprecated: Use Settings instead. Kept for backward compatibility.
	ProjectConfig   *config.Settings
	Settings        *config.Settings
	SandboxSnapshot SandboxReport
	Tags            map[string]string
}

// Result represents the agent execution result.
type Result struct {
	Output       string
	StopReason   string
	Usage        model.Usage
	ToolCalls    []model.ToolCall
	Artifacts    []artifact.ArtifactRef
	Lineage      artifact.LineageGraph
	Structured   any
	CheckpointID string
	Interrupted  bool
}

// SkillExecution records individual skill invocations.
type SkillExecution struct {
	Definition  skills.Definition
	Result      skills.Result
	Err         error
	MatchReason string // e.g. "always", "hit=keyword" — from skill matcher
}

// CommandExecution records slash command invocations.
type CommandExecution struct {
	Definition commands.Definition
	Result     commands.Result
	Err        error
}

// SandboxReport documents the sandbox configuration attached to the runtime.
type SandboxReport struct {
	Roots          []string
	AllowedPaths   []string
	AllowedDomains []string
	ResourceLimits sandbox.ResourceLimits
}

// WithMaxSessions caps how many parallel session histories are retained.
// Values <= 0 fall back to the default.
func WithMaxSessions(n int) func(*Options) {
	return func(o *Options) {
		if n > 0 {
			o.MaxSessions = n
		}
	}
}

// WithTokenTracking enables or disables token usage tracking.
func WithTokenTracking(enabled bool) func(*Options) {
	return func(o *Options) {
		o.TokenTracking = enabled
	}
}

// WithTokenCallback sets a callback function that is called synchronously after
// each model call with the token usage statistics. Automatically enables
// TokenTracking.
func WithTokenCallback(fn TokenCallback) func(*Options) {
	return func(o *Options) {
		o.TokenCallback = fn
		if fn != nil {
			o.TokenTracking = true
		}
	}
}

// WithAutoCompact configures automatic context compaction.
func WithAutoCompact(config CompactConfig) func(*Options) {
	return func(o *Options) {
		o.AutoCompact = config
	}
}

// WithOTEL configures OpenTelemetry distributed tracing.
// Requires build tag 'otel' for actual instrumentation; otherwise no-op.
func WithOTEL(config OTELConfig) func(*Options) {
	return func(o *Options) {
		o.OTEL = config
	}
}

func (o Options) withDefaults() Options {
	// withDefaults normalises entrypoint/mode, resolves project and settings paths,
	// and leaves tool selection untouched: Tools stays as provided (legacy override),
	// EnabledBuiltinTools/CustomTools keep their caller-supplied values for later
	// registration logic (nil means register all built-ins, empty slice means none).
	if o.EntryPoint == "" {
		o.EntryPoint = defaultEntrypoint
	}
	if o.Mode.EntryPoint == "" {
		o.Mode.EntryPoint = o.EntryPoint
	}

	// 智能解析项目根目录
	if o.ProjectRoot == "" || o.ProjectRoot == "." {
		if resolved, err := ResolveProjectRoot(); err == nil {
			o.ProjectRoot = resolved
		} else {
			o.ProjectRoot = "."
		}
	}
	o.ProjectRoot = filepath.Clean(o.ProjectRoot)
	if strings.TrimSpace(o.ConfigRoot) == "" {
		o.ConfigRoot = filepath.Join(o.ProjectRoot, ".saker")
	} else if !filepath.IsAbs(o.ConfigRoot) {
		o.ConfigRoot = filepath.Join(o.ProjectRoot, o.ConfigRoot)
	}
	o.ConfigRoot = filepath.Clean(o.ConfigRoot)
	if trimmed := strings.TrimSpace(o.SettingsPath); trimmed != "" {
		if abs, err := filepath.Abs(trimmed); err == nil {
			o.SettingsPath = abs
		} else {
			o.SettingsPath = trimmed
		}
	}

	if o.Sandbox.Root == "" {
		o.Sandbox.Root = o.ProjectRoot
	}
	o.Sandbox = normalizeSandboxOptions(o.ProjectRoot, o.Sandbox)

	// 根据 EntryPoint 自动设置网络白名单默认值
	if len(o.Sandbox.NetworkAllow) == 0 {
		o.Sandbox.NetworkAllow = defaultNetworkAllowList()
	}

	if o.MaxSessions <= 0 {
		o.MaxSessions = defaultMaxSessions
	}

	// Layered MaxIterations defaults — picked per surface so a long-running
	// platform service isn't constrained by a CLI one-shot's safety cap. Set
	// MaxIterations explicitly (including -1 for unlimited) to override.
	if o.MaxIterations == 0 {
		switch o.EntryPoint {
		case EntryPointPlatform:
			// Long-running services: callers bound the run via context, budget,
			// or token caps. Iteration counting alone is the wrong signal here.
			o.MaxIterations = -1
		case EntryPointCI:
			// Headless one-shot CI runs: 50 mirrors the subagent default and
			// gives builds enough headroom for multi-step refactors.
			o.MaxIterations = 50
		default: // EntryPointCLI / unset — preserve historical behavior.
			o.MaxIterations = agent.DefaultMaxIterations
		}
	}
	return o
}

// frozen returns a defensive copy of Options so callers can safely reuse/mutate
// the original Options struct without racing against a live Runtime.
func (o Options) frozen() Options {
	o.Mode = freezeMode(o.Mode)
	o.OutputSchema = cloneResponseFormat(o.OutputSchema)
	o.OutputSchemaMode = normalizeOutputSchemaMode(o.OutputSchemaMode)

	if len(o.Middleware) > 0 {
		o.Middleware = append([]middleware.Middleware(nil), o.Middleware...)
	}
	if len(o.Tools) > 0 {
		o.Tools = append([]tool.Tool(nil), o.Tools...)
	}
	if len(o.EnabledBuiltinTools) > 0 {
		o.EnabledBuiltinTools = append([]string(nil), o.EnabledBuiltinTools...)
	}
	if len(o.DisallowedTools) > 0 {
		o.DisallowedTools = append([]string(nil), o.DisallowedTools...)
	}
	if len(o.DisabledSkills) > 0 {
		o.DisabledSkills = append([]string(nil), o.DisabledSkills...)
	}
	if len(o.CustomTools) > 0 {
		o.CustomTools = append([]tool.Tool(nil), o.CustomTools...)
	}
	if len(o.MCPServers) > 0 {
		o.MCPServers = append([]string(nil), o.MCPServers...)
	}
	if len(o.TypedHooks) > 0 {
		hooks := make([]corehooks.ShellHook, len(o.TypedHooks))
		for i, hook := range o.TypedHooks {
			hooks[i] = hook
			if len(hook.Env) > 0 {
				hooks[i].Env = maps.Clone(hook.Env)
			}
		}
		o.TypedHooks = hooks
	}
	if len(o.HookMiddleware) > 0 {
		o.HookMiddleware = append([]coremw.Middleware(nil), o.HookMiddleware...)
	}
	if len(o.Skills) > 0 {
		skillsCopy := make([]SkillRegistration, len(o.Skills))
		for i, reg := range o.Skills {
			skillsCopy[i] = reg
			def := reg.Definition
			if len(def.Metadata) > 0 {
				def.Metadata = maps.Clone(def.Metadata)
			}
			if len(def.Matchers) > 0 {
				def.Matchers = append([]skills.Matcher(nil), def.Matchers...)
			}
			skillsCopy[i].Definition = def
		}
		o.Skills = skillsCopy
	}
	if len(o.Commands) > 0 {
		o.Commands = append([]CommandRegistration(nil), o.Commands...)
	}
	if len(o.Subagents) > 0 {
		subCopy := make([]SubagentRegistration, len(o.Subagents))
		for i, reg := range o.Subagents {
			subCopy[i] = reg
			def := reg.Definition
			def.BaseContext = def.BaseContext.Clone()
			if len(def.Matchers) > 0 {
				def.Matchers = append([]skills.Matcher(nil), def.Matchers...)
			}
			subCopy[i].Definition = def
		}
		o.Subagents = subCopy
	}
	if len(o.SkillsDirs) > 0 {
		o.SkillsDirs = append([]string(nil), o.SkillsDirs...)
	}
	if o.SkillsRecursive != nil {
		v := *o.SkillsRecursive
		o.SkillsRecursive = &v
	}

	o.Sandbox = freezeSandboxOptions(o.Sandbox)

	if len(o.ModelPool) > 0 {
		o.ModelPool = maps.Clone(o.ModelPool)
	}
	if len(o.SubagentModelMapping) > 0 {
		o.SubagentModelMapping = maps.Clone(o.SubagentModelMapping)
	}

	return o
}

func cloneResponseFormat(in *model.ResponseFormat) *model.ResponseFormat {
	if in == nil {
		return nil
	}
	out := *in
	if in.JSONSchema != nil {
		js := *in.JSONSchema
		if len(in.JSONSchema.Schema) > 0 {
			js.Schema = maps.Clone(in.JSONSchema.Schema)
		}
		out.JSONSchema = &js
	}
	return &out
}

func freezeSandboxOptions(in SandboxOptions) SandboxOptions {
	out := in
	if len(in.AllowedPaths) > 0 {
		out.AllowedPaths = append([]string(nil), in.AllowedPaths...)
	}
	if len(in.NetworkAllow) > 0 {
		out.NetworkAllow = append([]string(nil), in.NetworkAllow...)
	}
	if in.GVisor != nil {
		gv := *in.GVisor
		if len(in.GVisor.Mounts) > 0 {
			gv.Mounts = append([]sandboxenv.MountSpec(nil), in.GVisor.Mounts...)
		}
		out.GVisor = &gv
	}
	if in.Govm != nil {
		gv := *in.Govm
		if len(in.Govm.Mounts) > 0 {
			gv.Mounts = append([]sandboxenv.MountSpec(nil), in.Govm.Mounts...)
		}
		out.Govm = &gv
	}
	if in.Landlock != nil {
		ll := *in.Landlock
		if len(in.Landlock.AdditionalROPaths) > 0 {
			ll.AdditionalROPaths = append([]string(nil), in.Landlock.AdditionalROPaths...)
		}
		if len(in.Landlock.AdditionalRWPaths) > 0 {
			ll.AdditionalRWPaths = append([]string(nil), in.Landlock.AdditionalRWPaths...)
		}
		out.Landlock = &ll
	}
	return out
}

func normalizeSandboxOptions(projectRoot string, in SandboxOptions) SandboxOptions {
	out := in
	if out.Landlock != nil {
		ll := *out.Landlock
		if strings.TrimSpace(ll.HelperModeFlag) == "" {
			ll.HelperModeFlag = "--saker-landlock-helper"
		}
		if strings.TrimSpace(ll.DefaultGuestCwd) == "" {
			ll.DefaultGuestCwd = projectRoot
		}
		if ll.AutoCreateSessionWorkspace {
			if strings.TrimSpace(ll.SessionWorkspaceBase) == "" {
				ll.SessionWorkspaceBase = filepath.Join(projectRoot, "workspace")
			} else if !filepath.IsAbs(ll.SessionWorkspaceBase) {
				ll.SessionWorkspaceBase = filepath.Join(projectRoot, ll.SessionWorkspaceBase)
			}
			ll.SessionWorkspaceBase = filepath.Clean(ll.SessionWorkspaceBase)
		}
		out.Landlock = &ll
	}
	if out.GVisor != nil {
		gv := *out.GVisor
		if strings.TrimSpace(gv.HelperModeFlag) == "" {
			gv.HelperModeFlag = "--saker-gvisor-helper"
		}
		if strings.TrimSpace(gv.DefaultGuestCwd) == "" {
			gv.DefaultGuestCwd = "/workspace"
		}
		if gv.AutoCreateSessionWorkspace || len(gv.Mounts) == 0 {
			gv.AutoCreateSessionWorkspace = true
		}
		if strings.TrimSpace(gv.SessionWorkspaceBase) == "" {
			gv.SessionWorkspaceBase = filepath.Join(projectRoot, "workspace")
		} else if !filepath.IsAbs(gv.SessionWorkspaceBase) {
			gv.SessionWorkspaceBase = filepath.Join(projectRoot, gv.SessionWorkspaceBase)
		}
		gv.SessionWorkspaceBase = filepath.Clean(gv.SessionWorkspaceBase)
		if len(gv.Mounts) > 0 {
			mounts := make([]MountSpec, len(gv.Mounts))
			copy(mounts, gv.Mounts)
			gv.Mounts = mounts
		}
		out.GVisor = &gv
	}
	if out.Govm != nil {
		gv := *out.Govm
		if strings.TrimSpace(gv.DefaultGuestCwd) == "" {
			gv.DefaultGuestCwd = "/workspace"
		}
		if gv.AutoCreateSessionWorkspace || len(gv.Mounts) == 0 {
			gv.AutoCreateSessionWorkspace = true
		}
		if strings.TrimSpace(gv.SessionWorkspaceBase) == "" {
			gv.SessionWorkspaceBase = filepath.Join(projectRoot, "workspace")
		} else if !filepath.IsAbs(gv.SessionWorkspaceBase) {
			gv.SessionWorkspaceBase = filepath.Join(projectRoot, gv.SessionWorkspaceBase)
		}
		gv.SessionWorkspaceBase = filepath.Clean(gv.SessionWorkspaceBase)
		if strings.TrimSpace(gv.RuntimeHome) == "" {
			gv.RuntimeHome = filepath.Join(projectRoot, ".govm")
		} else if !filepath.IsAbs(gv.RuntimeHome) {
			gv.RuntimeHome = filepath.Join(projectRoot, gv.RuntimeHome)
		}
		gv.RuntimeHome = filepath.Clean(gv.RuntimeHome)
		if strings.TrimSpace(gv.OfflineImage) == "" && strings.TrimSpace(gv.Image) == "" {
			gv.OfflineImage = "py312-alpine"
		}
		if len(gv.Mounts) > 0 {
			mounts := make([]MountSpec, len(gv.Mounts))
			copy(mounts, gv.Mounts)
			gv.Mounts = mounts
		}
		out.Govm = &gv
	}
	return out
}

func validateSandboxOptions(projectRoot string, in SandboxOptions) error {
	cfg := normalizeSandboxOptions(projectRoot, in)
	validateMounts := func(name string, mounts []MountSpec) error {
		seen := make([]string, 0, len(mounts))
		for _, mount := range mounts {
			guest := strings.TrimSpace(mount.GuestPath)
			if guest == "" {
				return fmt.Errorf("api: %s mount guest path is required", name)
			}
			if !filepath.IsAbs(guest) {
				return fmt.Errorf("api: %s mount guest path must be absolute: %s", name, guest)
			}
			guest = filepath.Clean(guest)
			for _, existing := range seen {
				if guest == existing || strings.HasPrefix(guest+"/", existing+"/") || strings.HasPrefix(existing+"/", guest+"/") {
					return fmt.Errorf("api: overlapping %s guest mount paths: %s and %s", name, guest, existing)
				}
			}
			seen = append(seen, guest)
		}
		return nil
	}
	if cfg.GVisor != nil {
		if err := validateMounts("gvisor", cfg.GVisor.Mounts); err != nil {
			return err
		}
	}
	if cfg.Govm != nil {
		if err := validateMounts("govm", cfg.Govm.Mounts); err != nil {
			return err
		}
	}
	return nil
}

func freezeMode(in ModeContext) ModeContext {
	mode := in
	if mode.CLI != nil {
		cli := *mode.CLI
		if len(cli.Args) > 0 {
			cli.Args = append([]string(nil), cli.Args...)
		}
		if len(cli.Flags) > 0 {
			cli.Flags = maps.Clone(cli.Flags)
		}
		mode.CLI = &cli
	}
	if mode.CI != nil {
		ci := *mode.CI
		if len(ci.Matrix) > 0 {
			ci.Matrix = maps.Clone(ci.Matrix)
		}
		if len(ci.Metadata) > 0 {
			ci.Metadata = maps.Clone(ci.Metadata)
		}
		mode.CI = &ci
	}
	if mode.Platform != nil {
		plat := *mode.Platform
		if len(plat.Labels) > 0 {
			plat.Labels = maps.Clone(plat.Labels)
		}
		mode.Platform = &plat
	}
	return mode
}

// defaultNetworkAllowList returns the default local-network allow list.
func defaultNetworkAllowList() []string {
	return []string{
		"localhost",
		"127.0.0.1",
		"::1",       // IPv6 localhost
		"0.0.0.0",   // 本机所有接口
		"*.local",   // 本地域名
		"192.168.*", // 私有网段
		"10.*",      // 私有网段
		"172.16.*",  // 私有网段
	}
}

func (o Options) modeContext() ModeContext {
	mode := o.Mode
	if mode.EntryPoint == "" {
		mode.EntryPoint = o.EntryPoint
	}
	if mode.EntryPoint == "" {
		mode.EntryPoint = defaultEntrypoint
	}
	return mode
}

func (r Request) normalized(defaultMode ModeContext, fallbackSession string) Request {
	req := r
	if req.Mode.EntryPoint == "" {
		req.Mode.EntryPoint = defaultMode.EntryPoint
		req.Mode.CLI = defaultMode.CLI
		req.Mode.CI = defaultMode.CI
		req.Mode.Platform = defaultMode.Platform
	}
	if req.SessionID == "" {
		req.SessionID = strings.TrimSpace(fallbackSession)
	}
	if req.Tags == nil {
		req.Tags = map[string]string{}
	}
	if req.Metadata == nil {
		req.Metadata = map[string]any{}
	}
	if len(req.ToolWhitelist) > 0 {
		req.ToolWhitelist = normalizeStrings(req.ToolWhitelist)
	}
	if len(req.ForceSkills) > 0 {
		req.ForceSkills = append([]string(nil), req.ForceSkills...)
	}
	if len(req.ContentBlocks) > 0 {
		req.ContentBlocks = append([]model.ContentBlock(nil), req.ContentBlocks...)
	}
	if len(req.Channels) > 0 {
		req.Channels = normalizeStrings(req.Channels)
	}
	if len(req.Traits) > 0 {
		req.Traits = normalizeStrings(req.Traits)
	}
	req.OutputSchema = cloneResponseFormat(req.OutputSchema)
	return req
}

func normalizeOutputSchemaMode(mode OutputSchemaMode) OutputSchemaMode {
	switch mode {
	case "", OutputSchemaModeInline:
		return OutputSchemaModeInline
	case OutputSchemaModePostProcess:
		return OutputSchemaModePostProcess
	default:
		return OutputSchemaModeInline
	}
}

func (r Request) activationContext(prompt string) skills.ActivationContext {
	ctx := skills.ActivationContext{Prompt: prompt}
	if len(r.Channels) > 0 {
		ctx.Channels = append([]string(nil), r.Channels...)
	}
	if len(r.Traits) > 0 {
		ctx.Traits = append([]string(nil), r.Traits...)
	}
	if len(r.Tags) > 0 {
		ctx.Tags = maps.Clone(r.Tags)
	}
	if len(r.Metadata) > 0 {
		ctx.Metadata = maps.Clone(r.Metadata)
	}
	return ctx
}

// normalizeStrings clones, sorts, and deduplicates a string slice.
func normalizeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	slices.Sort(out)
	return slices.Compact(out)
}

// WithModelPool configures a pool of models indexed by tier.
func WithModelPool(pool map[ModelTier]model.Model) func(*Options) {
	return func(o *Options) {
		if pool != nil {
			o.ModelPool = pool
		}
	}
}

// WithSubagentModelMapping configures subagent-type-to-tier mappings for model selection.
// Keys should be lowercase subagent type names (e.g., "explore", "plan").
func WithSubagentModelMapping(mapping map[string]ModelTier) func(*Options) {
	return func(o *Options) {
		if mapping != nil {
			o.SubagentModelMapping = mapping
		}
	}
}

// HookRecorder mirrors the historical api hook recorder contract.
