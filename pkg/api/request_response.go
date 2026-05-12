package api

import (
	"maps"
	"slices"
	"strings"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/config"
	coreevents "github.com/cinience/saker/pkg/core/events"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/pipeline"
	"github.com/cinience/saker/pkg/runtime/commands"
	"github.com/cinience/saker/pkg/runtime/skills"
	"github.com/cinience/saker/pkg/runtime/subagents"
)

// ModelOverrides bundles per-request sampling parameters that override
// the runtime defaults for a single completion. All fields are pointer/
// slice types so the zero value means "no override". Set by the OpenAI
// inbound gateway to honor standard sampler fields (temperature, top_p,
// max_tokens, stop, seed) and by future SDK-as-LLM proxy callers.
//
// The runtime is free to ignore fields that the underlying provider
// adapter does not consume (e.g. ToolChoice on a model lacking
// function calling). Unknown overrides are not an error.
type ModelOverrides struct {
	Temperature       *float64 // nil = use runtime default
	TopP              *float64
	MaxTokens         *int
	Stop              []string // nil/empty = no override
	Seed              *int64
	ToolChoice        string   // "" = no override; "auto"/"none"/specific tool name
	ParallelToolCalls *bool
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
	ModelOverrides       *ModelOverrides // Optional: per-request sampling overrides
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
	Mode            ModeContext
	RequestID       string `json:"request_id,omitempty"` // UUID for distributed tracing
	Result          *Result
	Timeline        []TimelineEntry
	SkillResults    []SkillExecution
	CommandResults  []CommandExecution
	Subagent        *subagents.Result
	HookEvents      []coreevents.Event
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
