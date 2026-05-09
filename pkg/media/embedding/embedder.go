// Package embedding defines the Embedder interface and adapts aigo embed engines
// for use with saker media indexing and search.
package embedding

import (
	"context"
	"fmt"
	"os"

	"github.com/godeps/aigo/engine/embed"
	alibabacloudEmbed "github.com/godeps/aigo/engine/embed/alibabacloud"
	"github.com/godeps/aigo/engine/embed/gemini"
	"github.com/godeps/aigo/engine/embed/jina"
	"github.com/godeps/aigo/engine/embed/openai"
	"github.com/godeps/aigo/engine/embed/voyage"
)

// Embedder converts video chunks or text queries into dense vector embeddings.
type Embedder interface {
	// EmbedVideo embeds a video chunk file and returns its vector.
	EmbedVideo(ctx context.Context, chunkPath string) ([]float32, error)

	// EmbedQuery embeds a text query and returns its vector.
	EmbedQuery(ctx context.Context, query string) ([]float32, error)

	// Dimensions returns the embedding dimensionality.
	Dimensions() int
}

// Config holds configuration for creating an embedder.
type Config struct {
	// Backend selects the embedding provider ("gemini", "openai", "voyage", "jina", "aliyun").
	Backend string `json:"backend"`
	// APIKey for API-based backends.
	APIKey string `json:"api_key,omitempty"`
	// Model name override.
	Model string `json:"model,omitempty"`
	// Dimensions override for the output embedding size.
	Dims int `json:"dimensions,omitempty"`
	// RPM (requests per minute) rate limit.
	RPM int `json:"rpm,omitempty"`
	// BaseURL override for the API endpoint.
	BaseURL string `json:"base_url,omitempty"`
}

// backendEnvKeys maps backend names to their expected environment variable.
// Order defines auto-detection priority (first match wins).
var backendRegistry = []struct {
	name   string
	envKey string
}{
	{"aliyun", "DASHSCOPE_API_KEY"},
	{"gemini", "GEMINI_API_KEY"},
	{"openai", "OPENAI_API_KEY"},
	{"voyage", "VOYAGE_API_KEY"},
	{"jina", "JINA_API_KEY"},
}

// DetectBackend returns the first backend whose API key is set in the
// environment. Returns "" if none is found.
func DetectBackend() string {
	for _, b := range backendRegistry {
		if os.Getenv(b.envKey) != "" {
			return b.name
		}
	}
	return ""
}

// BackendInfo describes a supported embedding backend and its availability.
type BackendInfo struct {
	Name      string `json:"name"`
	EnvKey    string `json:"env_key"`
	Available bool   `json:"available"`
}

// AllBackends returns all supported backends with availability status.
func AllBackends() []BackendInfo {
	out := make([]BackendInfo, len(backendRegistry))
	for i, b := range backendRegistry {
		out[i] = BackendInfo{
			Name:      b.name,
			EnvKey:    b.envKey,
			Available: os.Getenv(b.envKey) != "",
		}
	}
	return out
}

// AvailableBackends returns all backends whose API key is set.
func AvailableBackends() []string {
	var out []string
	for _, b := range backendRegistry {
		if os.Getenv(b.envKey) != "" {
			out = append(out, b.name)
		}
	}
	return out
}

// NewEmbedder creates an Embedder from config, backed by an aigo embed engine.
// If Backend is empty, auto-detects from environment API keys.
func NewEmbedder(cfg Config) (Embedder, error) {
	backend := cfg.Backend
	if backend == "" {
		backend = DetectBackend()
	}
	if backend == "" {
		return nil, fmt.Errorf("embedding: no backend specified and no API key found in environment (set one of: DASHSCOPE_API_KEY, GEMINI_API_KEY, OPENAI_API_KEY, VOYAGE_API_KEY, JINA_API_KEY)")
	}

	var engine embed.EmbedEngine
	var err error

	switch backend {
	case "gemini":
		engine, err = gemini.New(gemini.Config{
			APIKey:     cfg.APIKey,
			Model:      cfg.Model,
			Dimensions: cfg.Dims,
			RPM:        cfg.RPM,
		})
	case "openai":
		engine, err = openai.New(openai.Config{
			APIKey:     cfg.APIKey,
			BaseURL:    cfg.BaseURL,
			Model:      cfg.Model,
			Dimensions: cfg.Dims,
			RPM:        cfg.RPM,
		})
	case "voyage":
		engine, err = voyage.New(voyage.Config{
			APIKey:     cfg.APIKey,
			BaseURL:    cfg.BaseURL,
			Model:      cfg.Model,
			Dimensions: cfg.Dims,
			RPM:        cfg.RPM,
		})
	case "jina":
		engine, err = jina.New(jina.Config{
			APIKey:     cfg.APIKey,
			BaseURL:    cfg.BaseURL,
			Model:      cfg.Model,
			Dimensions: cfg.Dims,
			RPM:        cfg.RPM,
		})
	case "aliyun", "dashscope":
		engine, err = alibabacloudEmbed.New(alibabacloudEmbed.Config{
			APIKey:     cfg.APIKey,
			BaseURL:    cfg.BaseURL,
			Model:      cfg.Model,
			Dimensions: cfg.Dims,
			RPM:        cfg.RPM,
		})
	default:
		return nil, fmt.Errorf("embedding: unknown backend %q (supported: gemini, openai, voyage, jina, aliyun)", backend)
	}

	if err != nil {
		return nil, err
	}

	return &adapter{engine: engine}, nil
}

// adapter wraps an aigo embed.EmbedEngine to implement the Embedder interface.
type adapter struct {
	engine embed.EmbedEngine
}

func (a *adapter) Dimensions() int { return a.engine.Dimensions() }

func (a *adapter) EmbedVideo(ctx context.Context, chunkPath string) ([]float32, error) {
	data, err := os.ReadFile(chunkPath)
	if err != nil {
		return nil, fmt.Errorf("read chunk: %w", err)
	}
	if len(data) < 1024 {
		return nil, fmt.Errorf("embedding: chunk too small (%d bytes)", len(data))
	}

	result, err := a.engine.Embed(ctx, embed.VideoRequest(data, "RETRIEVAL_DOCUMENT"))
	if err != nil {
		return nil, err
	}
	return result.Vector, nil
}

func (a *adapter) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	result, err := a.engine.Embed(ctx, embed.TextRequest(query, "RETRIEVAL_QUERY"))
	if err != nil {
		return nil, err
	}
	return result.Vector, nil
}
