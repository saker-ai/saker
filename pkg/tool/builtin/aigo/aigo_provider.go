package aigo

import (
	"net/http"
	"os"
	"sync"
	"time"

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

	"github.com/cinience/saker/pkg/config"
)

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