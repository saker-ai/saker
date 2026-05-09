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
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/godeps/aigo"
	"github.com/godeps/aigo/engine"
	"github.com/godeps/aigo/engine/alibabacloud"
	"github.com/godeps/aigo/engine/ark"
	"github.com/godeps/aigo/engine/comfydeploy"
	"github.com/godeps/aigo/engine/comfyui"
	"github.com/godeps/aigo/engine/elevenlabs"
	"github.com/godeps/aigo/engine/fal"
	"github.com/godeps/aigo/engine/flux"
	"github.com/godeps/aigo/engine/gemini"
	"github.com/godeps/aigo/engine/google"
	"github.com/godeps/aigo/engine/gpt4o"
	"github.com/godeps/aigo/engine/hailuo"
	"github.com/godeps/aigo/engine/hedra"
	"github.com/godeps/aigo/engine/ideogram"
	"github.com/godeps/aigo/engine/jimeng"
	"github.com/godeps/aigo/engine/kling"
	"github.com/godeps/aigo/engine/liblib"
	"github.com/godeps/aigo/engine/luma"
	"github.com/godeps/aigo/engine/meshy"
	"github.com/godeps/aigo/engine/midjourney"
	"github.com/godeps/aigo/engine/minimax"
	"github.com/godeps/aigo/engine/newapi"
	"github.com/godeps/aigo/engine/openai"
	"github.com/godeps/aigo/engine/openrouter"
	"github.com/godeps/aigo/engine/pika"
	"github.com/godeps/aigo/engine/recraft"
	"github.com/godeps/aigo/engine/replicate"
	"github.com/godeps/aigo/engine/runninghub"
	"github.com/godeps/aigo/engine/runway"
	"github.com/godeps/aigo/engine/stability"
	"github.com/godeps/aigo/engine/suno"
	"github.com/godeps/aigo/engine/volcvoice"
	"github.com/godeps/aigo/tooldef"

	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/security"
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

// envProvider describes a default provider activated by an environment variable.
type envProvider struct {
	envKey   string
	name     string
	provider config.AigoProvider
	routes   func() map[string][]string  // capability → engine refs
	schema   func() []engine.ConfigField // config fields required by this provider
}

// ProviderInfo describes a provider with its config schema and available models.
type ProviderInfo struct {
	Name        string               `json:"name"`
	DisplayName engine.DisplayName   `json:"displayName"`
	Fields      []engine.ConfigField `json:"fields"`
	Models      map[string][]string  `json:"models"`
}

// ProviderStatus describes the connectivity status of a provider's base URL.
type ProviderStatus struct {
	Name      string `json:"name"`
	Reachable bool   `json:"reachable"`
	BaseURL   string `json:"baseUrl,omitempty"`
	CheckedAt string `json:"checkedAt"`
}

// CheckProviderConnectivity checks whether each provider's base URL is reachable
// via an HTTP HEAD request (timeout 5s). Providers without a base URL are skipped.
func CheckProviderConnectivity(providers map[string]config.AigoProvider) []ProviderStatus {
	type result struct {
		idx    int
		status ProviderStatus
	}

	// Build work items: resolve base URL from config or schema env var.
	type workItem struct {
		name    string
		baseURL string
	}
	var items []workItem
	for name, p := range providers {
		base := expandEnv(p.BaseURL)
		if base == "" {
			// Try to find baseUrl env var from schema
			for _, ep := range envProviders {
				if ep.name == name || ep.name == p.Type {
					if ep.schema != nil {
						for _, f := range ep.schema() {
							if f.Key == "baseUrl" && f.EnvVar != "" {
								base = os.Getenv(f.EnvVar)
							} else if f.Key == "baseUrl" && f.Default != "" {
								base = f.Default
							}
						}
					}
					break
				}
			}
		}
		if base == "" {
			continue
		}
		items = append(items, workItem{name: name, baseURL: base})
	}

	results := make([]ProviderStatus, len(items))
	var wg sync.WaitGroup
	client := &http.Client{Timeout: 5 * time.Second}

	for i, item := range items {
		wg.Add(1)
		go func(idx int, w workItem) {
			defer wg.Done()
			ps := ProviderStatus{
				Name:      w.name,
				BaseURL:   w.baseURL,
				CheckedAt: time.Now().UTC().Format(time.RFC3339),
			}
			resp, err := client.Head(w.baseURL)
			if err == nil {
				resp.Body.Close()
				ps.Reachable = true
			}
			results[idx] = ps
		}(i, item)
	}
	wg.Wait()
	return results
}

// prefixRoutes converts a ModelsByCapability() result to routing refs with a provider prefix.
func prefixRoutes(prefix string, caps map[string][]string) map[string][]string {
	routes := map[string][]string{}
	for cap, models := range caps {
		for _, m := range models {
			routes[cap] = append(routes[cap], prefix+"/"+m)
		}
	}
	return routes
}

// AvailableModels returns all known models across all providers, grouped by
// provider and capability. Used by the web UI to populate model selection dropdowns.
func AvailableModels() map[string]map[string][]string {
	result := map[string]map[string][]string{}
	for _, ep := range envProviders {
		// Only include providers whose env var is actually set.
		if ep.envKey != "" && os.Getenv(ep.envKey) == "" {
			continue
		}
		routes := ep.routes()
		if len(routes) == 0 {
			continue
		}
		caps := map[string][]string{}
		for cap, refs := range routes {
			for _, ref := range refs {
				caps[cap] = append(caps[cap], ref)
			}
		}
		result[ep.name] = caps
	}
	return result
}

// envProviders maps environment variable names to default provider configurations.
var envProviders = []envProvider{
	// --- Image Generation ---
	{
		envKey:   "DASHSCOPE_API_KEY",
		name:     "alibabacloud",
		provider: config.AigoProvider{Type: "alibabacloud", APIKey: "${DASHSCOPE_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("alibabacloud", alibabacloud.ModelsByCapability()) },
		schema:   alibabacloud.ConfigSchema,
	},
	{
		envKey:   "OPENAI_API_KEY",
		name:     "openai",
		provider: config.AigoProvider{Type: "openai", APIKey: "${OPENAI_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("openai", openai.ModelsByCapability()) },
		schema:   openai.ConfigSchema,
	},
	{
		envKey:   "GOOGLE_API_KEY",
		name:     "google",
		provider: config.AigoProvider{Type: "google", APIKey: "${GOOGLE_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("google", google.ModelsByCapability()) },
		schema:   google.ConfigSchema,
	},
	{
		envKey:   "FLUX_API_KEY",
		name:     "flux",
		provider: config.AigoProvider{Type: "flux", APIKey: "${FLUX_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("flux", flux.ModelsByCapability()) },
		schema:   flux.ConfigSchema,
	},
	{
		envKey:   "STABILITY_API_KEY",
		name:     "stability",
		provider: config.AigoProvider{Type: "stability", APIKey: "${STABILITY_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("stability", stability.ModelsByCapability()) },
		schema:   stability.ConfigSchema,
	},
	{
		envKey:   "IDEOGRAM_API_KEY",
		name:     "ideogram",
		provider: config.AigoProvider{Type: "ideogram", APIKey: "${IDEOGRAM_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("ideogram", ideogram.ModelsByCapability()) },
		schema:   ideogram.ConfigSchema,
	},
	{
		envKey:   "RECRAFT_API_KEY",
		name:     "recraft",
		provider: config.AigoProvider{Type: "recraft", APIKey: "${RECRAFT_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("recraft", recraft.ModelsByCapability()) },
		schema:   recraft.ConfigSchema,
	},
	{
		envKey:   "MIDJOURNEY_API_KEY",
		name:     "midjourney",
		provider: config.AigoProvider{Type: "midjourney", APIKey: "${MIDJOURNEY_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("midjourney", midjourney.ModelsByCapability()) },
		schema:   midjourney.ConfigSchema,
	},
	{
		envKey:   "JIMENG_API_KEY",
		name:     "jimeng",
		provider: config.AigoProvider{Type: "jimeng", APIKey: "${JIMENG_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("jimeng", jimeng.ModelsByCapability()) },
		schema:   jimeng.ConfigSchema,
	},
	{
		envKey:   "LIBLIB_ACCESS_KEY",
		name:     "liblib",
		provider: config.AigoProvider{Type: "liblib", APIKey: "${LIBLIB_ACCESS_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("liblib", liblib.ModelsByCapability()) },
		schema:   liblib.ConfigSchema,
	},
	{
		envKey:   "ARK_API_KEY",
		name:     "ark",
		provider: config.AigoProvider{Type: "ark", APIKey: "${ARK_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("ark", ark.ModelsByCapability()) },
		schema:   ark.ConfigSchema,
	},
	// --- Video Generation ---
	{
		envKey:   "KLING_API_KEY",
		name:     "kling",
		provider: config.AigoProvider{Type: "kling", APIKey: "${KLING_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("kling", kling.ModelsByCapability()) },
		schema:   kling.ConfigSchema,
	},
	{
		envKey:   "HAILUO_API_KEY",
		name:     "hailuo",
		provider: config.AigoProvider{Type: "hailuo", APIKey: "${HAILUO_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("hailuo", hailuo.ModelsByCapability()) },
		schema:   hailuo.ConfigSchema,
	},
	{
		envKey:   "LUMA_API_KEY",
		name:     "luma",
		provider: config.AigoProvider{Type: "luma", APIKey: "${LUMA_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("luma", luma.ModelsByCapability()) },
		schema:   luma.ConfigSchema,
	},
	{
		envKey:   "RUNWAY_API_KEY",
		name:     "runway",
		provider: config.AigoProvider{Type: "runway", APIKey: "${RUNWAY_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("runway", runway.ModelsByCapability()) },
		schema:   runway.ConfigSchema,
	},
	{
		envKey:   "PIKA_API_KEY",
		name:     "pika",
		provider: config.AigoProvider{Type: "pika", APIKey: "${PIKA_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("pika", pika.ModelsByCapability()) },
		schema:   pika.ConfigSchema,
	},
	{
		envKey:   "HEDRA_API_KEY",
		name:     "hedra",
		provider: config.AigoProvider{Type: "hedra", APIKey: "${HEDRA_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("hedra", hedra.ModelsByCapability()) },
		schema:   hedra.ConfigSchema,
	},
	// --- Audio / Music ---
	{
		envKey:   "ELEVENLABS_API_KEY",
		name:     "elevenlabs",
		provider: config.AigoProvider{Type: "elevenlabs", APIKey: "${ELEVENLABS_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("elevenlabs", elevenlabs.ModelsByCapability()) },
		schema:   elevenlabs.ConfigSchema,
	},
	{
		envKey:   "MINIMAX_API_KEY",
		name:     "minimax",
		provider: config.AigoProvider{Type: "minimax", APIKey: "${MINIMAX_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("minimax", minimax.ModelsByCapability()) },
		schema:   minimax.ConfigSchema,
	},
	{
		envKey:   "SUNO_API_KEY",
		name:     "suno",
		provider: config.AigoProvider{Type: "suno", APIKey: "${SUNO_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("suno", suno.ModelsByCapability()) },
		schema:   suno.ConfigSchema,
	},
	{
		envKey:   "VOLC_SPEECH_ACCESS_TOKEN",
		name:     "volcvoice",
		provider: config.AigoProvider{Type: "volcvoice", APIKey: "${VOLC_SPEECH_ACCESS_TOKEN}"},
		routes:   func() map[string][]string { return prefixRoutes("volcvoice", volcvoice.ModelsByCapability()) },
		schema:   volcvoice.ConfigSchema,
	},
	// --- 3D Generation ---
	{
		envKey:   "MESHY_API_KEY",
		name:     "meshy",
		provider: config.AigoProvider{Type: "meshy", APIKey: "${MESHY_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("meshy", meshy.ModelsByCapability()) },
		schema:   meshy.ConfigSchema,
	},
	// --- Multi-Modal Understanding ---
	{
		envKey:   "GEMINI_API_KEY",
		name:     "gemini",
		provider: config.AigoProvider{Type: "gemini", APIKey: "${GEMINI_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("gemini", gemini.ModelsByCapability()) },
		schema:   gemini.ConfigSchema,
	},
	{
		envKey:   "OPENAI_API_KEY",
		name:     "gpt4o",
		provider: config.AigoProvider{Type: "gpt4o", APIKey: "${OPENAI_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("gpt4o", gpt4o.ModelsByCapability()) },
		schema:   gpt4o.ConfigSchema,
	},
	// --- Multi-Backend / Gateway ---
	{
		envKey:   "NEWAPI_API_KEY",
		name:     "newapi",
		provider: config.AigoProvider{Type: "newapi", APIKey: "${NEWAPI_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("newapi", newapi.ModelsByCapability()) },
		schema:   newapi.ConfigSchema,
	},
	{
		envKey:   "OPENROUTER_API_KEY",
		name:     "openrouter",
		provider: config.AigoProvider{Type: "openrouter", APIKey: "${OPENROUTER_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("openrouter", openrouter.ModelsByCapability()) },
		schema:   openrouter.ConfigSchema,
	},
	{
		envKey:   "FAL_KEY",
		name:     "fal",
		provider: config.AigoProvider{Type: "fal", APIKey: "${FAL_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("fal", fal.ModelsByCapability()) },
		schema:   fal.ConfigSchema,
	},
	{
		envKey:   "REPLICATE_API_TOKEN",
		name:     "replicate",
		provider: config.AigoProvider{Type: "replicate", APIKey: "${REPLICATE_API_TOKEN}"},
		routes:   func() map[string][]string { return prefixRoutes("replicate", replicate.ModelsByCapability()) },
		schema:   replicate.ConfigSchema,
	},
	{
		envKey:   "COMFYDEPLOY_API_KEY",
		name:     "comfydeploy",
		provider: config.AigoProvider{Type: "comfydeploy", APIKey: "${COMFYDEPLOY_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("comfydeploy", comfydeploy.ModelsByCapability()) },
		schema:   comfydeploy.ConfigSchema,
	},
	{
		envKey:   "RUNNINGHUB_API_KEY",
		name:     "runninghub",
		provider: config.AigoProvider{Type: "runninghub", APIKey: "${RUNNINGHUB_API_KEY}"},
		routes:   func() map[string][]string { return prefixRoutes("runninghub", runninghub.ModelsByCapability()) },
		schema:   runninghub.ConfigSchema,
	},
}

// AvailableProviders returns all known providers with their config schemas and models.
// Used by the web UI to dynamically render provider configuration forms.
func AvailableProviders() []ProviderInfo {
	var providers []ProviderInfo
	for _, ep := range envProviders {
		info := ProviderInfo{
			Name:        ep.name,
			DisplayName: engine.LookupDisplayName(ep.name),
			Models:      ep.routes(),
		}
		if ep.schema != nil {
			info.Fields = ep.schema()
		}
		providers = append(providers, info)
	}
	// Add comfyui (no env auto-detection, but schema is available).
	providers = append(providers, ProviderInfo{
		Name:        "comfyui",
		DisplayName: engine.LookupDisplayName("comfyui"),
		Fields:      comfyui.ConfigSchema(),
		Models:      map[string][]string{},
	})
	return providers
}

// DefaultConfigFromEnv builds an AigoConfig by detecting API key environment variables.
// Returns nil if no known API keys are found.
func DefaultConfigFromEnv() *config.AigoConfig {
	providers := map[string]config.AigoProvider{}
	routing := map[string][]string{}

	seen := map[string]bool{} // deduplicate by name (e.g. openai and gpt4o share OPENAI_API_KEY)
	for _, ep := range envProviders {
		if os.Getenv(ep.envKey) == "" {
			continue
		}
		if seen[ep.name] {
			continue
		}
		seen[ep.name] = true
		providers[ep.name] = ep.provider
		for cap, refs := range ep.routes() {
			routing[cap] = append(routing[cap], refs...)
		}
	}

	if len(providers) == 0 {
		return nil
	}
	return &config.AigoConfig{
		Providers: providers,
		Routing:   routing,
	}
}

// ConfigOption configures NewToolsFromConfig behavior.
type ConfigOption func(*configOptions)

type configOptions struct {
	dataDir string
}

// WithDataDir sets the persistence directory for aigo TaskStore (crash recovery).
// The task store file will be created at {dataDir}/aigo_tasks.json.
func WithDataDir(dir string) ConfigOption {
	return func(o *configOptions) { o.dataDir = dir }
}

// NewToolsFromConfig creates an aigo client and tools from settings.json AigoConfig.
// Returns nil if cfg is nil or has no routing.
func NewToolsFromConfig(cfg *config.AigoConfig, opts ...ConfigOption) ([]tool.Tool, error) {
	if cfg == nil || len(cfg.Routing) == 0 {
		return nil, nil
	}

	var co configOptions
	for _, o := range opts {
		o(&co)
	}

	var clientOpts []sdk.ClientOption
	if co.dataDir != "" {
		store, err := sdk.NewFileTaskStore(filepath.Join(co.dataDir, "aigo_tasks.json"))
		if err != nil {
			slog.Warn("[aigo] warning: failed to create task store", "error", err)
		} else {
			clientOpts = append(clientOpts, sdk.WithStore(store))
			slog.Info("[aigo] task store initialized", "path", fmt.Sprintf("%s/aigo_tasks.json", co.dataDir))
		}
	}

	client := sdk.NewClient(clientOpts...)
	client.Use(sdk.WithRetry(2))

	// Build set of disabled models for quick lookup.
	disabledModels := map[string]bool{}
	for pName, prov := range cfg.Providers {
		for _, m := range prov.DisabledModels {
			disabledModels[pName+"/"+m] = true
		}
	}

	// Track registered engines to avoid duplicates.
	registered := map[string]bool{}

	for capability, refs := range cfg.Routing {
		for _, ref := range refs {
			engineKey := ref // "provider-name/model"
			if registered[engineKey] {
				continue
			}

			providerName, modelName, err := ParseRef(ref)
			if err != nil {
				return nil, fmt.Errorf("aigo: invalid routing ref %q: %w", ref, err)
			}

			provider, ok := cfg.Providers[providerName]
			if !ok {
				return nil, fmt.Errorf("aigo: provider %q not found (referenced in routing %q)", providerName, ref)
			}

			eng, err := BuildEngine(provider, modelName, capability)
			if err != nil {
				return nil, fmt.Errorf("aigo: build engine for %q: %w", ref, err)
			}

			if err := client.RegisterEngine(engineKey, eng); err != nil {
				return nil, fmt.Errorf("aigo: register engine %q: %w", engineKey, err)
			}
			registered[engineKey] = true

			// Disable engine if provider or model is disabled (registered for runtime re-enable).
			if (provider.Enabled != nil && !*provider.Enabled) || disabledModels[ref] {
				client.DisableEngine(engineKey)
			}
		}
	}

	var timeout time.Duration
	if cfg.Timeout != "" {
		d, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("aigo: invalid timeout %q: %w", cfg.Timeout, err)
		}
		timeout = d
	}

	// Build tools with routing.
	defs := tooldef.AllTools()
	var tools []tool.Tool
	for _, def := range defs {
		cap := toolCapability[def.Name]
		engines := cfg.Routing[cap]
		if len(engines) == 0 {
			continue // no routing for this capability, skip tool
		}

		toolTimeout := timeout
		if toolTimeout == 0 && cap == "video" {
			toolTimeout = videoTimeout
		}
		t := &AigoTool{
			client:  client,
			def:     def,
			engines: engines,
			timeout: toolTimeout,
		}

		// Inject engine param into schema if multiple engines available.
		if len(engines) > 1 {
			t.def = injectEngineParam(def, engines)
		}

		tools = append(tools, t)
	}

	return tools, nil
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

func (t *AigoTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if t.client == nil {
		return nil, fmt.Errorf("aigo: client is nil")
	}

	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	// Validate params against schema constraints (enums, required) before calling the API.
	if err := tooldef.ValidateParams(t.def, params); err != nil {
		slog.Warn("[aigo] validation failed", "tool", t.def.Name, "error", err)
		return &tool.ToolResult{
			Success: false,
			Output:  formatInvalidParams(t.def, params, err),
		}, nil
	}

	task := buildTask(t.def.Name, params)
	slog.Debug("[aigo] task built", "tool", t.def.Name, "prompt", task.Prompt, "size", task.Size, "refs", len(task.References))

	// DryRun check: surface warnings before executing.
	engineName := stringParam(params, "engine")
	if engineName == "" && len(t.engines) > 0 {
		engineName = t.engines[0]
	}
	if engineName != "" {
		if dr, err := t.client.DryRun(engineName, task); err == nil && len(dr.Warnings) > 0 {
			slog.Debug("[aigo] dryrun warnings", "tool", t.def.Name, "warnings", dr.Warnings)
			_ = dr // warnings are informational
		}
	}

	return t.executeSync(ctx, params, task)
}

// StreamExecute implements tool.StreamingTool, emitting periodic progress
// updates for long-running operations (e.g. video generation) while the
// engine polls internally via WaitForCompletion.
func (t *AigoTool) StreamExecute(ctx context.Context, params map[string]interface{}, emit func(string, bool)) (*tool.ToolResult, error) {
	if t.client == nil {
		return nil, fmt.Errorf("aigo: client is nil")
	}

	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	if err := tooldef.ValidateParams(t.def, params); err != nil {
		return &tool.ToolResult{
			Success: false,
			Output:  formatInvalidParams(t.def, params, err),
		}, nil
	}

	task := buildTask(t.def.Name, params)

	// For slow capabilities (video), use the SDK's native progress callback
	// to surface real polling progress through the SSE pipeline.
	var opts []sdk.ExecuteOption
	cap := toolCapability[t.def.Name]
	if slowCapabilities[cap] && emit != nil {
		opts = append(opts, sdk.WithProgress(func(evt sdk.ProgressEvent) {
			switch evt.Phase {
			case "submitted":
				emit(fmt.Sprintf("[%s] task submitted", t.def.Name), false)
			case "polling":
				emit(fmt.Sprintf("[%s] generating... attempt %d, %s elapsed",
					t.def.Name, evt.Attempt, evt.Elapsed.Truncate(time.Second)), false)
			case "completed":
				emit(fmt.Sprintf("[%s] completed in %s",
					t.def.Name, evt.Elapsed.Truncate(time.Second)), false)
			}
		}))
	}

	return t.executeSync(ctx, params, task, opts...)
}

// resolveEngines returns the best engine for this execution, applying smart
// routing for video generation based on reference assets:
//   - no references        → t2v (text-to-video)
//   - 1 image reference    → i2v (image-to-video)
//   - multiple refs / video → r2v (reference-to-video, up to 5 mixed refs)
func (t *AigoTool) resolveEngines(params map[string]interface{}, task sdk.AgentTask) []string {
	if t.def.Name != "generate_video" || len(task.References) == 0 {
		return t.engines
	}

	imageCount := 0
	hasVideoRef := false
	for _, ref := range task.References {
		switch ref.Type {
		case sdk.ReferenceTypeImage:
			imageCount++
		case sdk.ReferenceTypeVideo:
			hasVideoRef = true
		}
	}

	if imageCount == 0 && !hasVideoRef {
		return t.engines
	}

	// Determine target suffix based on reference pattern.
	targetSuffix := "-i2v"
	if imageCount > 1 || hasVideoRef {
		targetSuffix = "-r2v"
	}

	// Find matching engine from the registered list.
	for _, eng := range t.engines {
		if strings.HasSuffix(eng, targetSuffix) {
			slog.Info("[aigo] smart route", "tool", t.def.Name, "images", imageCount, "has_video", hasVideoRef, "engine", eng)
			return []string{eng}
		}
	}
	// Fallback: any engine with reference support.
	for _, eng := range t.engines {
		if strings.Contains(eng, "-i2v") || strings.Contains(eng, "-r2v") {
			slog.Info("[aigo] smart route fallback", "tool", t.def.Name, "images", imageCount, "has_video", hasVideoRef, "engine", eng)
			return []string{eng}
		}
	}
	return t.engines
}

func (t *AigoTool) executeSync(ctx context.Context, params map[string]interface{}, task sdk.AgentTask, opts ...sdk.ExecuteOption) (*tool.ToolResult, error) {
	start := time.Now()
	engines := t.resolveEngines(params, task)

	// If caller specified an engine, try it directly.
	if eng := stringParam(params, "engine"); eng != "" {
		slog.Info("[aigo] calling engine", "tool", t.def.Name, "engine", eng)
		result, err := t.client.ExecuteTask(ctx, eng, task, opts...)
		if err != nil {
			slog.Error("[aigo] engine FAILED", "tool", t.def.Name, "engine", eng, "elapsed", time.Since(start), "error", err)
			return nil, fmt.Errorf("aigo %s (engine %s): %w", t.def.Name, eng, err)
		}
		slog.Info("[aigo] engine OK", "tool", t.def.Name, "engine", eng, "elapsed", time.Since(start), "result_len", len(result.Value))
		tr, terr := toToolResult(result, t.def.Name)
		if terr != nil {
			slog.Error("[aigo] engine INVALID", "tool", t.def.Name, "engine", eng, "elapsed", time.Since(start), "error", terr)
			return nil, terr
		}
		return tr, nil
	}

	// Single engine: direct call.
	if len(engines) == 1 {
		slog.Info("[aigo] calling single engine", "tool", t.def.Name, "engine", engines[0])
		result, err := t.client.ExecuteTask(ctx, engines[0], task, opts...)
		if err != nil {
			slog.Error("[aigo] engine FAILED", "tool", t.def.Name, "engine", engines[0], "elapsed", time.Since(start), "error", err)
			return nil, fmt.Errorf("aigo %s: %w", t.def.Name, err)
		}
		slog.Info("[aigo] engine OK", "tool", t.def.Name, "engine", engines[0], "elapsed", time.Since(start), "result_len", len(result.Value))
		tr, terr := toToolResult(result, t.def.Name)
		if terr != nil {
			slog.Error("[aigo] engine INVALID", "tool", t.def.Name, "engine", engines[0], "elapsed", time.Since(start), "error", terr)
			return nil, terr
		}
		return tr, nil
	}

	// Multiple engines: fallback.
	slog.Info("[aigo] calling with fallback engines", "tool", t.def.Name, "engines", engines)
	fr, err := t.client.ExecuteTaskWithFallback(ctx, engines, task, opts...)
	if err != nil {
		slog.Error("[aigo] fallback FAILED", "tool", t.def.Name, "elapsed", time.Since(start), "error", err)
		return nil, fmt.Errorf("aigo %s: %w", t.def.Name, err)
	}
	slog.Info("[aigo] fallback OK", "tool", t.def.Name, "elapsed", time.Since(start), "engine", fr.Engine, "result_len", len(fr.Output.Value))
	tr, terr := toToolResult(fr.Output, t.def.Name)
	if terr != nil {
		slog.Error("[aigo] fallback INVALID", "tool", t.def.Name, "elapsed", time.Since(start), "engine", fr.Engine, "error", terr)
		return nil, terr
	}
	return tr, nil
}

func toToolResult(result sdk.Result, toolName string) (*tool.ToolResult, error) {
	cap := toolCapability[toolName]

	// Backstop for the "engine returned task_id instead of URL" bug class:
	// for media-producing tools, refuse a non-URL value loud-and-fast so the
	// agent loop sees a real error rather than the canvas silently writing a
	// UUID into <video src=>. The legitimate fix lives in the engine factory
	// (WaitForCompletion); this is the last line of defense.
	if mediaCapabilities[cap] && !isMediaURL(result.Value) {
		return nil, fmt.Errorf(
			"aigo %s: engine returned %q which is not a media URL — likely an unresumed async task. "+
				"Configure waitForCompletion=true on the provider, or wire a Resumer for the engine.",
			toolName, result.Value)
	}

	tr := &tool.ToolResult{
		Success: true,
		Output:  result.Value,
	}
	meta := map[string]interface{}{}
	if result.Metadata != nil {
		for k, v := range result.Metadata {
			meta[k] = v
		}
	}
	switch cap {
	case "image", "image_edit":
		meta["media_type"] = "image"
	case "video", "video_edit":
		meta["media_type"] = "video"
	case "tts", "music":
		meta["media_type"] = "audio"
	case "3d":
		meta["media_type"] = "3d"
	case "asr":
		meta["media_type"] = "text"
	}
	if result.Value != "" {
		meta["media_url"] = result.Value
	}
	if len(meta) > 0 {
		tr.Structured = meta
	}
	return tr, nil
}

// ParseRef splits "provider-name/model" into provider and model.
func ParseRef(ref string) (provider, model string, err error) {
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected format provider/model, got %q", ref)
	}
	return parts[0], parts[1], nil
}

// expandEnv replaces ${VAR} references with environment variable values.
func expandEnv(s string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	return os.Expand(s, os.Getenv)
}

// BuildEngine creates an aigo engine.Engine from a provider config and model name.
// It delegates to the factory system registered by each engine package's init().
//
// capability is the aigo capability key the engine will be wired to ("video",
// "image", "3d", ...). It is used to derive a smart default for
// WaitForCompletion when the user did not configure it explicitly: slow
// capabilities (video/video_edit/3d) auto-poll, otherwise the engine's own
// per-model default applies.
func BuildEngine(p config.AigoProvider, model, capability string) (engine.Engine, error) {
	apiKey := security.ResolveEnv(expandEnv(p.APIKey))
	baseURL := expandEnv(p.BaseURL)

	providerType := strings.ToLower(p.Type)
	// Handle alias.
	if providerType == "aliyun" {
		providerType = "alibabacloud"
	}

	factory, ok := engine.GetFactory(providerType)
	if !ok {
		return nil, fmt.Errorf("unknown provider type %q", p.Type)
	}

	// Expand env vars in metadata values.
	metadata := make(map[string]string, len(p.Metadata))
	for k, v := range p.Metadata {
		metadata[k] = expandEnv(v)
	}

	// Resolve WaitForCompletion: explicit user config wins; otherwise turn it
	// on for slow capabilities so video/video_edit/3d don't silently leak a
	// task_id into mediaUrl. The engine's own factory may further refine this
	// (e.g. alibabacloud upgrades async-only models even for image_edit).
	wait := p.WaitForCompletion
	if wait == nil {
		if slowCapabilities[capability] {
			v := true
			wait = &v
		}
	}

	var pollInterval time.Duration
	if p.PollInterval != "" {
		d, err := time.ParseDuration(p.PollInterval)
		if err != nil {
			return nil, fmt.Errorf("aigo: invalid pollInterval %q: %w", p.PollInterval, err)
		}
		pollInterval = d
	}

	return factory(engine.EngineConfig{
		Provider:          providerType,
		Model:             model,
		APIKey:            apiKey,
		BaseURL:           baseURL,
		Metadata:          metadata,
		WaitForCompletion: wait,
		PollInterval:      pollInterval,
	})
}

// injectEngineParam adds an optional "engine" parameter to a tooldef for multi-engine routing.
func injectEngineParam(def tooldef.ToolDef, engines []string) tooldef.ToolDef {
	clone := def
	clone.Parameters = tooldef.Schema{
		Type:       def.Parameters.Type,
		Required:   def.Parameters.Required,
		Properties: make(map[string]tooldef.Schema, len(def.Parameters.Properties)+1),
	}
	for k, v := range def.Parameters.Properties {
		clone.Parameters.Properties[k] = v
	}
	clone.Parameters.Properties["engine"] = tooldef.Schema{
		Type:        "string",
		Description: "Engine to use (optional, defaults to first available with fallback)",
		Enum:        engines,
	}
	return clone
}

// buildTask converts tool params into an aigo.AgentTask based on the tool name.
func buildTask(toolName string, params map[string]interface{}) sdk.AgentTask {
	switch toolName {
	case "generate_image":
		return buildImageTask(params)
	case "generate_video":
		return buildVideoTask(params)
	case "text_to_speech":
		return buildTTSTask(params)
	case "generate_music":
		return buildMusicTask(params)
	case "design_voice":
		return buildVoiceDesignTask(params)
	case "edit_image":
		return buildEditImageTask(params)
	case "edit_video":
		return buildEditVideoTask(params)
	case "transcribe_audio":
		return buildTranscribeTask(params)
	default:
		return sdk.AgentTask{Prompt: stringParam(params, "prompt")}
	}
}

// cameraAnglePrompts maps camera_angle enum values to natural English descriptions
// for prompt prepending when the provider doesn't support camera_angle natively.
var cameraAnglePrompts = map[string]string{
	"front":      "front view",
	"side":       "side view",
	"back":       "rear view",
	"top-down":   "bird's eye view",
	"low-angle":  "low angle shot",
	"high-angle": "high angle shot",
	"45-degree":  "three-quarter view",
	"close-up":   "extreme close-up",
}

func buildImageTask(p map[string]interface{}) sdk.AgentTask {
	task := sdk.AgentTask{
		Prompt:         stringParam(p, "prompt"),
		NegativePrompt: stringParam(p, "negative_prompt"),
		Size:           stringParam(p, "size"),
		Width:          intParam(p, "width"),
		Height:         intParam(p, "height"),
	}

	structured := &sdk.AgentTaskStructured{
		ImageSize:        stringParam(p, "size"),
		ImageAspectRatio: stringParam(p, "aspect_ratio"),
		ImageResolution:  stringParam(p, "resolution"),
		ImageCameraAngle: stringParam(p, "camera_angle"),
	}
	if structured.ImageAspectRatio != "" || structured.ImageResolution != "" || structured.ImageCameraAngle != "" {
		task.Structured = structured
	}

	// Prepend camera angle to prompt for providers that don't support it natively.
	if angle := stringParam(p, "camera_angle"); angle != "" {
		if desc, ok := cameraAnglePrompts[angle]; ok {
			task.Prompt = desc + ", " + task.Prompt
		} else {
			task.Prompt = angle + " shot, " + task.Prompt
		}
	}

	// Reference images for image-to-image generation.
	seenRefs := make(map[string]struct{})
	for _, ref := range stringSliceParam(p, "reference_images") {
		appendReferenceAsset(&task, seenRefs, sdk.ReferenceTypeImage, ref)
	}
	if ref := stringParam(p, "reference_image"); ref != "" {
		appendReferenceAsset(&task, seenRefs, sdk.ReferenceTypeImage, ref)
	}

	return task
}

func buildVideoTask(p map[string]interface{}) sdk.AgentTask {
	task := sdk.AgentTask{
		Prompt:   stringParam(p, "prompt"),
		Duration: intParam(p, "duration"),
		Size:     stringParam(p, "size"),
	}

	// Structured video options.
	structured := &sdk.AgentTaskStructured{
		VideoDuration:    intParam(p, "duration"),
		VideoSize:        stringParam(p, "size"),
		VideoAspectRatio: stringParam(p, "aspect_ratio"),
		VideoResolution:  stringParam(p, "resolution"),
	}
	if v, ok := p["audio"]; ok {
		if b, ok := v.(bool); ok {
			structured.VideoAudio = &b
		}
	}
	if v, ok := p["watermark"]; ok {
		if b, ok := v.(bool); ok {
			structured.VideoWatermark = &b
		}
	}
	task.Structured = structured

	// Reference assets: image and/or video.
	seenRefs := make(map[string]struct{})
	for _, ref := range stringSliceParam(p, "reference_images") {
		appendReferenceAsset(&task, seenRefs, sdk.ReferenceTypeImage, ref)
	}
	if ref := stringParam(p, "reference_image"); ref != "" {
		appendReferenceAsset(&task, seenRefs, sdk.ReferenceTypeImage, ref)
	}
	if ref := stringParam(p, "reference_video"); ref != "" {
		appendReferenceAsset(&task, seenRefs, sdk.ReferenceTypeVideo, ref)
	}
	return task
}

func appendReferenceAsset(task *sdk.AgentTask, seen map[string]struct{}, refType sdk.ReferenceType, raw string) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return
	}
	key := string(refType) + ":" + ref
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	task.References = append(task.References, sdk.ReferenceAsset{Type: refType, URL: resolveLocalRef(ref)})
}

func stringSliceParam(p map[string]interface{}, key string) []string {
	raw, ok := p[key]
	if !ok || raw == nil {
		return nil
	}

	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func buildTTSTask(p map[string]interface{}) sdk.AgentTask {
	return sdk.AgentTask{
		Prompt: stringParam(p, "text"),
		TTS: &sdk.TTSOptions{
			Voice:        stringParam(p, "voice"),
			LanguageType: stringParam(p, "language"),
			Instructions: stringParam(p, "instructions"),
		},
	}
}

func buildMusicTask(p map[string]interface{}) sdk.AgentTask {
	prompt := stringParam(p, "prompt")
	if prompt == "" {
		prompt = stringParam(p, "text")
	}
	return sdk.AgentTask{
		Prompt: prompt,
	}
}

func buildVoiceDesignTask(p map[string]interface{}) sdk.AgentTask {
	return sdk.AgentTask{
		Prompt: stringParam(p, "voice_prompt"),
		VoiceDesign: &sdk.VoiceDesignOptions{
			VoicePrompt:   stringParam(p, "voice_prompt"),
			PreviewText:   stringParam(p, "preview_text"),
			TargetModel:   stringParam(p, "target_model"),
			PreferredName: stringParam(p, "preferred_name"),
			Language:      stringParam(p, "language"),
		},
	}
}

func buildEditImageTask(p map[string]interface{}) sdk.AgentTask {
	task := sdk.AgentTask{
		Prompt: stringParam(p, "prompt"),
		Size:   stringParam(p, "size"),
	}
	if url := stringParam(p, "image_url"); url != "" {
		task.References = []sdk.ReferenceAsset{{Type: sdk.ReferenceTypeImage, URL: resolveLocalRef(url)}}
	}
	return task
}

func buildEditVideoTask(p map[string]interface{}) sdk.AgentTask {
	task := sdk.AgentTask{
		Prompt:   stringParam(p, "prompt"),
		Duration: intParam(p, "duration"),
		Size:     stringParam(p, "size"),
	}
	if url := stringParam(p, "video_url"); url != "" {
		task.References = append(task.References, sdk.ReferenceAsset{Type: sdk.ReferenceTypeVideo, URL: resolveLocalRef(url)})
	}
	if url := stringParam(p, "reference_image"); url != "" {
		task.References = append(task.References, sdk.ReferenceAsset{Type: sdk.ReferenceTypeImage, URL: resolveLocalRef(url)})
	}
	return task
}

// resolveLocalRef converts a local /api/files/ path to a base64 data URI
// so that external APIs (e.g. aliyun) can consume it. Public URLs and
// data URIs are returned as-is.
func resolveLocalRef(rawURL string) string {
	if !strings.HasPrefix(rawURL, "/api/files/") {
		return rawURL
	}

	// /api/files/home/vipas/.../foo.png → /home/vipas/.../foo.png
	trimmed := strings.TrimPrefix(rawURL, "/api/files/")
	decoded, err := url.PathUnescape(trimmed)
	if err != nil {
		decoded = trimmed
	}
	diskPath := "/" + decoded

	data, err := os.ReadFile(diskPath)
	if err != nil {
		slog.Warn("[aigo] resolveLocalRef: cannot read file", "path", diskPath, "error", err)
		return rawURL // fall back to original URL
	}

	mimeType := mime.TypeByExtension(filepath.Ext(diskPath))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)
	slog.Info("[aigo] resolveLocalRef: converted file to data URI", "path", diskPath, "mime", mimeType, "size", len(data))
	return dataURI
}

func buildTranscribeTask(p map[string]interface{}) sdk.AgentTask {
	prompt := stringParam(p, "audio_url")
	if lang := stringParam(p, "language"); lang != "" {
		prompt += " language=" + lang
	}
	if f := stringParam(p, "response_format"); f != "" {
		prompt += " format=" + f
	}
	return sdk.AgentTask{Prompt: prompt}
}

// convertSchema converts a tooldef.Schema to a tool.JSONSchema.
func convertSchema(s tooldef.Schema) *tool.JSONSchema {
	js := &tool.JSONSchema{
		Type:     s.Type,
		Required: s.Required,
	}

	if len(s.Enum) > 0 {
		enums := make([]interface{}, len(s.Enum))
		for i, v := range s.Enum {
			enums[i] = v
		}
		js.Enum = enums
	}

	if s.Items != nil {
		js.Items = convertSchema(*s.Items)
	}

	if len(s.Properties) > 0 {
		props := make(map[string]interface{}, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = schemaToMap(v)
		}
		js.Properties = props
	}

	return js
}

// schemaToMap converts a tooldef.Schema to a map for embedding in JSONSchema.Properties.
func schemaToMap(s tooldef.Schema) map[string]interface{} {
	m := map[string]interface{}{
		"type": s.Type,
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		enums := make([]interface{}, len(s.Enum))
		for i, v := range s.Enum {
			enums[i] = v
		}
		m["enum"] = enums
	}
	if s.Default != "" {
		m["default"] = s.Default
	}
	if len(s.Properties) > 0 {
		props := make(map[string]interface{}, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = schemaToMap(v)
		}
		m["properties"] = props
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	if s.Items != nil {
		m["items"] = schemaToMap(*s.Items)
	}
	return m
}

func stringParam(p map[string]interface{}, key string) string {
	v, _ := p[key].(string)
	return v
}

func intParam(p map[string]interface{}, key string) int {
	switch v := p[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}

// formatInvalidParams enriches the bare "parameter X is required" error
// from tooldef.ValidateParams with two extra hints — the schema's required
// field list and the keys the model actually passed — so a confused model
// has concrete information to self-correct on its next iteration.
//
// Background: in the eddaff17 thread incident the model emitted a
// generate_image call with no prompt; the bare error gave it nothing to
// hook onto and the next iteration produced garbage instead of a fixed
// call. Surfacing the schema in-band is cheap and gives the model the
// context to recover without user intervention.
func formatInvalidParams(def tooldef.ToolDef, params map[string]interface{}, err error) string {
	provided := make([]string, 0, len(params))
	for k := range params {
		provided = append(provided, k)
	}
	sort.Strings(provided)

	required := append([]string(nil), def.Parameters.Required...)
	sort.Strings(required)

	out := fmt.Sprintf(
		"Invalid parameters for tool %q: %s.\n  required: [%s]\n  provided: [%s]\n  hint: re-emit the call with all required fields populated.",
		def.Name,
		err,
		strings.Join(required, ", "),
		strings.Join(provided, ", "),
	)
	// Special case: zero parameters delivered, but the schema clearly
	// requires some. This is the eddaff17 fingerprint — usually means the
	// upstream API proxy stripped tool_use.input rather than the model
	// emitting a literally empty call. The extra note steers a confused
	// model toward retrying the WHOLE call, not just adding one field.
	if len(provided) == 0 && len(required) > 0 {
		out += "\n  note: tool was called with no parameters at all — if this was unintentional," +
			" the API proxy may have dropped tool_use.input. Re-emit the entire call with every" +
			" required field populated, do not simply patch one missing field."
	}
	return out
}
