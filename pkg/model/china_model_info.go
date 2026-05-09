package model

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed data/china_model_catalog.json
var chinaModelRawData []byte

type ChinaModelPricePoint struct {
	InputPerMillion          float64 `json:"input_per_million"`
	OutputPerMillion         float64 `json:"output_per_million"`
	OutputThinkingPerMillion float64 `json:"output_thinking_per_million"`
	CacheHitPerMillion       float64 `json:"cache_hit_per_million"`
	CacheReadPerMillion      float64 `json:"cache_read_per_million"`
	CacheWritePerMillion     float64 `json:"cache_write_per_million"`
}

type ChinaModelPriceTier struct {
	MinInputTokens           int     `json:"min_input_tokens"`
	MaxInputTokens           int     `json:"max_input_tokens"`
	InputPerMillion          float64 `json:"input_per_million"`
	OutputPerMillion         float64 `json:"output_per_million"`
	OutputThinkingPerMillion float64 `json:"output_thinking_per_million"`
}

// ChinaModelRegionalPricing holds per-region overrides for default and tiered pricing.
type ChinaModelRegionalPricing struct {
	Default ChinaModelPricePoint  `json:"default"`
	Tiers   []ChinaModelPriceTier `json:"tiers"`
}

type ChinaModelPricing struct {
	Currency string                               `json:"currency"`
	Unit     string                               `json:"unit"`
	Default  ChinaModelPricePoint                 `json:"default"`
	Modes    map[string]ChinaModelPricePoint      `json:"modes"`
	Tiers    []ChinaModelPriceTier                `json:"tiers"`
	Regional map[string]ChinaModelRegionalPricing `json:"regional"`
}

type ChinaModelInfo struct {
	Name                    string            `json:"name"`
	Vendor                  string            `json:"vendor"`
	Provider                string            `json:"provider"`
	Family                  string            `json:"family"`
	Region                  string            `json:"region"`
	BaseLiteLLMKey          string            `json:"base_litellm_key"`
	Aliases                 []string          `json:"aliases"`
	Mode                    string            `json:"mode"`
	MaxInputTokens          int               `json:"max_input_tokens"`
	MaxOutputTokens         int               `json:"max_output_tokens"`
	SupportsVision          bool              `json:"supports_vision"`
	SupportsFunctionCalling bool              `json:"supports_function_calling"`
	SupportsPromptCaching   bool              `json:"supports_prompt_caching"`
	SupportsResponseSchema  bool              `json:"supports_response_schema"`
	SupportsReasoning       bool              `json:"supports_reasoning"`
	Pricing                 ChinaModelPricing `json:"pricing"`
	SourceURL               string            `json:"source_url"`
	SourceVerifiedAt        string            `json:"source_verified_at"`
	Notes                   string            `json:"notes"`
}

var (
	chinaModelOnce   sync.Once
	chinaModels      map[string]ChinaModelInfo
	chinaModelLookup map[string]string
)

func ensureChinaModelsLoaded() {
	chinaModelOnce.Do(func() {
		var raw map[string]ChinaModelInfo
		if err := json.Unmarshal(chinaModelRawData, &raw); err != nil {
			chinaModels = make(map[string]ChinaModelInfo)
			chinaModelLookup = make(map[string]string)
			return
		}

		chinaModels = raw
		chinaModelLookup = make(map[string]string, len(raw)*3)
		for key, info := range raw {
			registerChinaModelLookup(key, key)
			registerChinaModelLookup(info.Name, key)
			registerChinaModelLookup(info.BaseLiteLLMKey, key)
			for _, alias := range info.Aliases {
				registerChinaModelLookup(alias, key)
			}
		}
	})
}

func registerChinaModelLookup(alias, key string) {
	normalized := normalizeChinaModelLookupKey(alias)
	if normalized == "" {
		return
	}
	chinaModelLookup[normalized] = key
}

func normalizeChinaModelLookupKey(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	return stripProviderPrefix(name)
}

func LookupChinaModelInfo(modelName string) (ChinaModelInfo, bool) {
	ensureChinaModelsLoaded()

	normalized := normalizeChinaModelLookupKey(modelName)
	if normalized == "" {
		return ChinaModelInfo{}, false
	}

	key, ok := chinaModelLookup[normalized]
	if !ok {
		return ChinaModelInfo{}, false
	}
	info, ok := chinaModels[key]
	return info, ok
}

func ChinaModelCount() int {
	ensureChinaModelsLoaded()
	return len(chinaModels)
}

// --- Region-aware pricing selection ---

var chinaModelRegion string // "china_mainland" or "international" (default: "")

// SetChinaModelRegion sets the active region for pricing lookups.
// Valid values: "china_mainland", "international".
func SetChinaModelRegion(region string) {
	chinaModelRegion = region
}

// ChinaModelRegion returns the current region setting.
func ChinaModelRegion() string {
	return chinaModelRegion
}

// DetectChinaRegionFromBaseURL returns "china_mainland" if the base URL
// points to a mainland endpoint, otherwise "international".
func DetectChinaRegionFromBaseURL(baseURL string) string {
	if strings.Contains(baseURL, "dashscope.aliyuncs.com") {
		return "china_mainland"
	}
	return "international"
}

// resolvedPricing returns the effective default pricing and tiers for the
// current region. If the region has an override in Regional, use it;
// otherwise fall back to the top-level Default/Tiers.
func (p ChinaModelPricing) resolvedPricing() (ChinaModelPricePoint, []ChinaModelPriceTier) {
	if region := chinaModelRegion; region != "" && len(p.Regional) > 0 {
		if rp, ok := p.Regional[region]; ok {
			return rp.Default, rp.Tiers
		}
	}
	return p.Default, p.Tiers
}
