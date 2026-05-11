// Package aigo bridges the aigo multimodal media generation SDK into saker tool.Tool implementations.
//
// Auto-registration via settings.json:
//
//	{
//	  "aigo": {
//	    "providers": {
//	      "ali": {"type": "aliyun", "apiKey": "${DASHSCOPE_API_KEY}"},
//	      "openai": {"type": "openai", "apiKey": "${OPENAI_API_KEY}"}
//	    },
//	    "routing": {
//	      "image": ["ali/qwen-max-vl", "openai/dall-e-3"],
//	      "video": ["ali/wanx-video"],
//	      "tts":   ["ali/cosyvoice-v2"]
//	    }
//	  }
//	}
//
// Programmatic usage:
//
//	client := aigo.NewClient()
//	_ = client.RegisterEngine("img", aliyun.New(aliyun.Config{...}))
//	tools := aigotools.NewTools(client, "img")
package aigo

import (
	"fmt"
	"strings"
	"time"

	sdk "github.com/godeps/aigo"
	"github.com/godeps/aigo/engine"
	"github.com/godeps/aigo/tooldef"

	"github.com/cinience/saker/pkg/tool"
)

// Compile-time interface assertions.
var (
	_ tool.Tool          = (*AigoTool)(nil)
	_ tool.StreamingTool = (*AigoTool)(nil)
)

// Capability groups mapping tooldef names to routing keys.
var toolCapability = map[string]string{
	"generate_image":   "image",
	"edit_image":       "image_edit",
	"generate_video":   "video",
	"edit_video":       "video_edit",
	"text_to_speech":   "tts",
	"design_voice":     "tts",
	"transcribe_audio": "asr",
	"generate_3d":      "3d",
	"generate_music":   "music",
}

// videoTimeout is the default timeout for video generation (longer than other tools).
const videoTimeout = 35 * time.Minute

// slowCapabilities lists capabilities that benefit from progress reporting.
var slowCapabilities = map[string]bool{
	"video":      true,
	"video_edit": true,
	"3d":         true,
}

// mediaCapabilities lists capabilities whose tool results MUST be a URL or
// data-URI pointing at the rendered media. If a tool in this set returns a
// raw task_id (UUID) or empty string, an upstream engine almost certainly
// failed to poll an async task to completion — surfacing that as a tool error
// makes the agent loop retry/recover instead of silently writing the UUID
// into a canvas mediaUrl that the browser cannot play.
var mediaCapabilities = map[string]bool{
	"image":      true,
	"image_edit": true,
	"video":      true,
	"video_edit": true,
	"tts":        true,
	"music":      true,
	"3d":         true,
}

// isMediaURL reports whether v looks like a fetchable media reference.
func isMediaURL(v string) bool {
	if v == "" {
		return false
	}
	switch {
	case strings.HasPrefix(v, "http://"),
		strings.HasPrefix(v, "https://"),
		strings.HasPrefix(v, "data:"),
		strings.HasPrefix(v, "blob:"),
		strings.HasPrefix(v, "/api/files/"),
		strings.HasPrefix(v, "file://"):
		return true
	}
	return false
}

// AigoTool wraps a single aigo tooldef as an saker tool.Tool.
type AigoTool struct {
	client  *sdk.Client
	def     tooldef.ToolDef
	engines []string // ordered engine names for this tool's capability
	timeout time.Duration
}

// Option configures an AigoTool.
type Option func(*AigoTool)

// WithTimeout sets a per-execution timeout.
func WithTimeout(d time.Duration) Option {
	return func(t *AigoTool) { t.timeout = d }
}

// NewTool creates a single saker tool backed by an aigo engine.
func NewTool(client *sdk.Client, def tooldef.ToolDef, engineName string, opts ...Option) *AigoTool {
	t := &AigoTool{
		client:  client,
		def:     def,
		engines: []string{engineName},
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// NewTools returns all 6 aigo tools bound to the given engine.
func NewTools(client *sdk.Client, engineName string, opts ...Option) []tool.Tool {
	defs := tooldef.AllTools()
	tools := make([]tool.Tool, len(defs))
	for i, def := range defs {
		tools[i] = NewTool(client, def, engineName, opts...)
	}
	return tools
}

func (t *AigoTool) Name() string        { return t.def.Name }
func (t *AigoTool) Description() string { return t.def.Description }

// Engines returns the ordered list of engine names configured for this tool.
func (t *AigoTool) Engines() []string { return t.engines }

func (t *AigoTool) Schema() *tool.JSONSchema {
	return convertSchema(t.def.Parameters)
}

// Capabilities returns the engine capabilities for this tool's primary engine.
// Returns nil if the engine does not implement Describer.
func (t *AigoTool) Capabilities() *engine.Capability {
	if len(t.engines) == 0 {
		return nil
	}
	return t.EngineCapabilities(t.engines[0])
}

// EngineCapabilities returns the engine capabilities for a specific engine.
func (t *AigoTool) EngineCapabilities(name string) *engine.Capability {
	if t.client == nil {
		return nil
	}
	cap, err := t.client.EngineCapabilities(name)
	if err != nil {
		return nil
	}
	// Empty capability means Describer not implemented.
	if len(cap.MediaTypes) == 0 && len(cap.Models) == 0 {
		return nil
	}
	return &cap
}

// DryRun checks what would happen without executing, returning warnings and estimates.
func (t *AigoTool) DryRun(params map[string]interface{}) (*engine.DryRunResult, error) {
	if t.client == nil || len(t.engines) == 0 {
		return nil, fmt.Errorf("aigo: no engines configured")
	}
	task := buildTask(t.def.Name, params)
	result, err := t.client.DryRun(t.engines[0], task)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
