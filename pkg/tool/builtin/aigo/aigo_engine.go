package aigo

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/godeps/aigo/engine"
	"github.com/godeps/aigo/tooldef"

	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/security"
)

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