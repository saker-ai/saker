// provider_extended.go: saker Provider types for the additional providers that
// Bifrost v1.5.8+ supports beyond Anthropic / OpenAI / DashScope. Each is a
// thin wrapper over NewBifrost: it owns the env-var resolution conventions
// and TTL caching the rest of saker expects from a Provider.
//
// Bifrost supports 23+ providers in total. Saker exposes first-class types
// for the most common ones; less common providers (Cohere, Mistral, Groq,
// XAI, Cerebras, etc.) can be reached by constructing a BifrostConfig
// directly and calling NewBifrost — the schemas.ModelProvider constants
// re-exported as BifrostProvider* are the typed entry points.
package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/security"
	"github.com/maximhq/bifrost/core/schemas"
)

// Re-exports of the provider constants for callers in pkg/api / pkg/config
// that don't otherwise import bifrost schemas. Keep names stable; saker
// configuration files reference these strings.
const (
	BifrostProviderBedrock = schemas.Bedrock
	BifrostProviderVertex  = schemas.Vertex
	BifrostProviderAzure   = schemas.Azure
	BifrostProviderOllama  = schemas.Ollama
	BifrostProviderCohere  = schemas.Cohere
	BifrostProviderMistral = schemas.Mistral
	BifrostProviderGroq    = schemas.Groq
	BifrostProviderXAI     = schemas.XAI
	BifrostProviderGemini  = schemas.Gemini
)

// providerCache is the shared TTL-caching scaffold each *Provider type embeds.
// It avoids re-implementing the double-checked-locking pattern from
// AnthropicProvider / OpenAIProvider in every new wrapper.
type providerCache struct {
	mu      sync.RWMutex
	cached  Model
	expires time.Time
}

func (c *providerCache) load(ttl time.Duration) Model {
	if ttl <= 0 {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cached == nil || time.Now().After(c.expires) {
		return nil
	}
	return c.cached
}

func (c *providerCache) store(m Model, ttl time.Duration) {
	if ttl <= 0 || m == nil {
		return
	}
	c.cached = m
	c.expires = time.Now().Add(ttl)
}

// OllamaProvider wires a local Ollama server through Bifrost. Ollama is
// typically keyless — only BaseURL is required, defaulting to the standard
// localhost port when unset.
type OllamaProvider struct {
	BaseURL     string
	ModelName   string
	MaxTokens   int
	Temperature *float64
	System      string
	CacheTTL    time.Duration
	providerCache
}

// Model implements Provider.
func (p *OllamaProvider) Model(_ context.Context) (Model, error) {
	if mdl := p.load(p.CacheTTL); mdl != nil {
		return mdl, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && (p.CacheTTL <= 0 || time.Now().Before(p.expires)) {
		return p.cached, nil
	}

	baseURL := strings.TrimSpace(p.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("OLLAMA_BASE_URL"))
	}
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	mdl, err := NewBifrost(BifrostConfig{
		Provider:    schemas.Ollama,
		ModelName:   strings.TrimSpace(p.ModelName),
		BaseURL:     baseURL,
		MaxTokens:   p.MaxTokens,
		Temperature: p.Temperature,
		System:      p.System,
		OllamaKeyConfig: &schemas.OllamaKeyConfig{
			URL: schemas.EnvVar{Val: baseURL},
		},
	})
	if err != nil {
		return nil, err
	}
	p.store(mdl, p.CacheTTL)
	return mdl, nil
}

// BedrockProvider wires AWS Bedrock through Bifrost. Auth has three modes:
//   - explicit: AccessKey + SecretKey (+ optional SessionToken)
//   - IAM role: leave AccessKey/SecretKey empty (Bifrost reads ambient AWS
//     credentials via the standard SDK chain)
//   - role assumption: set RoleARN (+ optional ExternalID)
//
// Region is read from p.Region first, then AWS_REGION / AWS_DEFAULT_REGION.
type BedrockProvider struct {
	AccessKey    string // empty triggers IAM-role auth
	SecretKey    string
	SessionToken string
	Region       string
	RoleARN      string
	ExternalID   string
	ModelName    string
	MaxTokens    int
	Temperature  *float64
	System       string
	CacheTTL     time.Duration
	providerCache
}

// Model implements Provider.
func (p *BedrockProvider) Model(_ context.Context) (Model, error) {
	if mdl := p.load(p.CacheTTL); mdl != nil {
		return mdl, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && (p.CacheTTL <= 0 || time.Now().Before(p.expires)) {
		return p.cached, nil
	}

	region := strings.TrimSpace(p.Region)
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_REGION"))
	}
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION"))
	}
	if region == "" {
		return nil, errors.New("bedrock: region required (set Region, AWS_REGION, or AWS_DEFAULT_REGION)")
	}

	access := strings.TrimSpace(p.AccessKey)
	secret := strings.TrimSpace(p.SecretKey)
	if access == "" {
		access = strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID"))
	}
	if secret == "" {
		secret = strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY"))
	}
	session := strings.TrimSpace(p.SessionToken)
	if session == "" {
		session = strings.TrimSpace(os.Getenv("AWS_SESSION_TOKEN"))
	}

	cfg := &schemas.BedrockKeyConfig{
		AccessKey: schemas.EnvVar{Val: access},
		SecretKey: schemas.EnvVar{Val: secret},
		Region:    &schemas.EnvVar{Val: region},
	}
	if session != "" {
		cfg.SessionToken = &schemas.EnvVar{Val: session}
	}
	if role := strings.TrimSpace(p.RoleARN); role != "" {
		cfg.RoleARN = &schemas.EnvVar{Val: role}
	}
	if ext := strings.TrimSpace(p.ExternalID); ext != "" {
		cfg.ExternalID = &schemas.EnvVar{Val: ext}
	}

	mdl, err := NewBifrost(BifrostConfig{
		Provider:         schemas.Bedrock,
		ModelName:        strings.TrimSpace(p.ModelName),
		MaxTokens:        p.MaxTokens,
		Temperature:      p.Temperature,
		System:           p.System,
		BedrockKeyConfig: cfg,
	})
	if err != nil {
		return nil, err
	}
	p.store(mdl, p.CacheTTL)
	return mdl, nil
}

// VertexProvider wires Google Vertex AI through Bifrost. AuthCredentials
// is the contents of the service-account JSON; leave it empty to use
// Vertex IAM role authentication (ambient GCP credentials).
type VertexProvider struct {
	ProjectID       string
	ProjectNumber   string
	Region          string
	AuthCredentials string // service-account JSON; empty = IAM role
	ModelName       string
	MaxTokens       int
	Temperature     *float64
	System          string
	CacheTTL        time.Duration
	providerCache
}

// Model implements Provider.
func (p *VertexProvider) Model(_ context.Context) (Model, error) {
	if mdl := p.load(p.CacheTTL); mdl != nil {
		return mdl, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && (p.CacheTTL <= 0 || time.Now().Before(p.expires)) {
		return p.cached, nil
	}

	project := strings.TrimSpace(p.ProjectID)
	if project == "" {
		project = strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT"))
	}
	if project == "" {
		return nil, errors.New("vertex: project_id required (set ProjectID or GOOGLE_CLOUD_PROJECT)")
	}
	region := strings.TrimSpace(p.Region)
	if region == "" {
		region = strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_REGION"))
	}
	if region == "" {
		region = "us-central1"
	}
	creds := strings.TrimSpace(p.AuthCredentials)
	if creds == "" {
		// Allow file-path env var; saker callers may pass JSON inline or
		// rely on standard GOOGLE_APPLICATION_CREDENTIALS. Bifrost handles
		// the empty-string IAM-role fallback itself.
		creds = strings.TrimSpace(os.Getenv("VERTEX_AUTH_CREDENTIALS"))
	}

	cfg := &schemas.VertexKeyConfig{
		ProjectID:       schemas.EnvVar{Val: project},
		Region:          schemas.EnvVar{Val: region},
		AuthCredentials: schemas.EnvVar{Val: creds},
	}
	if num := strings.TrimSpace(p.ProjectNumber); num != "" {
		cfg.ProjectNumber = schemas.EnvVar{Val: num}
	}

	mdl, err := NewBifrost(BifrostConfig{
		Provider:        schemas.Vertex,
		ModelName:       strings.TrimSpace(p.ModelName),
		MaxTokens:       p.MaxTokens,
		Temperature:     p.Temperature,
		System:          p.System,
		VertexKeyConfig: cfg,
	})
	if err != nil {
		return nil, err
	}
	p.store(mdl, p.CacheTTL)
	return mdl, nil
}

// AzureProvider wires Azure OpenAI through Bifrost. Two auth modes:
//   - API key: set APIKey + Endpoint
//   - Client secret / managed identity: set ClientID + ClientSecret + TenantID
type AzureProvider struct {
	APIKey       string
	Endpoint     string // e.g. https://my-resource.openai.azure.com
	APIVersion   string // e.g. "2024-10-21"; Bifrost defaults to current GA
	ClientID     string
	ClientSecret string
	TenantID     string
	ModelName    string // Azure deployment name
	MaxTokens    int
	Temperature  *float64
	System       string
	CacheTTL     time.Duration
	providerCache
}

// Model implements Provider.
func (p *AzureProvider) Model(_ context.Context) (Model, error) {
	if mdl := p.load(p.CacheTTL); mdl != nil {
		return mdl, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && (p.CacheTTL <= 0 || time.Now().Before(p.expires)) {
		return p.cached, nil
	}

	apiKey := strings.TrimSpace(p.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("AZURE_OPENAI_API_KEY"))
	}
	endpoint := strings.TrimSpace(p.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("AZURE_OPENAI_ENDPOINT"))
	}
	if endpoint == "" {
		return nil, errors.New("azure: endpoint required (set Endpoint or AZURE_OPENAI_ENDPOINT)")
	}

	cfg := &schemas.AzureKeyConfig{
		Endpoint: schemas.EnvVar{Val: endpoint},
	}
	if v := strings.TrimSpace(p.APIVersion); v != "" {
		cfg.APIVersion = &schemas.EnvVar{Val: v}
	}
	if id := strings.TrimSpace(p.ClientID); id != "" {
		cfg.ClientID = &schemas.EnvVar{Val: id}
	}
	if cs := strings.TrimSpace(p.ClientSecret); cs != "" {
		cfg.ClientSecret = &schemas.EnvVar{Val: cs}
	}
	if tid := strings.TrimSpace(p.TenantID); tid != "" {
		cfg.TenantID = &schemas.EnvVar{Val: tid}
	}

	if apiKey == "" && cfg.ClientID == nil {
		return nil, errors.New("azure: api key or client_id/secret/tenant required")
	}

	mdl, err := NewBifrost(BifrostConfig{
		Provider:       schemas.Azure,
		ModelName:      strings.TrimSpace(p.ModelName),
		APIKey:         security.ResolveEnv(apiKey),
		MaxTokens:      p.MaxTokens,
		Temperature:    p.Temperature,
		System:         p.System,
		AzureKeyConfig: cfg,
	})
	if err != nil {
		return nil, fmt.Errorf("azure: %w", err)
	}
	p.store(mdl, p.CacheTTL)
	return mdl, nil
}
