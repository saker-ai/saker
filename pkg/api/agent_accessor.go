package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	aigotools "github.com/cinience/saker/pkg/tool/builtin/aigo"
	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/memory"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/persona"
	"github.com/cinience/saker/pkg/sandbox"
	"github.com/cinience/saker/pkg/sessiondb"
	"github.com/cinience/saker/pkg/tool"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
)

// Config returns the last loaded project config.
func (rt *Runtime) Config() *config.Settings {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return config.MergeSettings(nil, rt.cfg)
}

// Settings exposes the merged settings.json snapshot for callers that need it.
func (rt *Runtime) Settings() *config.Settings {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return config.MergeSettings(nil, rt.settings)
}

// ToolInfo holds the name, description, and category of a registered tool.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

// ToolInfos returns info for all registered tools, sorted by name.
func (rt *Runtime) ToolInfos() []ToolInfo {
	if rt.registry == nil {
		return nil
	}
	tools := rt.registry.List()
	infos := make([]ToolInfo, len(tools))
	for i, t := range tools {
		infos[i] = ToolInfo{
			Name:        t.Name(),
			Description: t.Description(),
			Category:    rt.registry.ToolSource(t.Name()),
		}
	}
	return infos
}

// ExecuteTool directly executes a registered tool by name with the given params.
// This bypasses the agent loop and is used by the web UI for direct tool invocation.
func (rt *Runtime) ExecuteTool(ctx context.Context, name string, params map[string]any) (*tool.ToolResult, error) {
	if rt.registry == nil {
		return nil, errors.New("api: tool registry is not initialized")
	}
	t, err := rt.registry.Get(name)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	return t.Execute(ctx, params)
}

// ToolSchemaResult holds the schema and engine info for a tool.
type ToolSchemaResult struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
	Engines     []string       `json:"engines,omitempty"`
}

// ToolSchema returns the JSON schema and available engines for a registered tool.
// If an engine is specified, it attempts to merge engine-specific capabilities (e.g. models, voices).
func (rt *Runtime) ToolSchema(name string, engineName string) (*ToolSchemaResult, error) {
	if rt.registry == nil {
		return nil, errors.New("api: tool registry is not initialized")
	}
	t, err := rt.registry.Get(name)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}

	schema := t.Schema()
	schemaMap := make(map[string]any)
	if schema != nil {
		raw, err := json.Marshal(schema)
		if err == nil {
			_ = json.Unmarshal(raw, &schemaMap)
		}
	}

	result := &ToolSchemaResult{
		Name:        t.Name(),
		Description: t.Description(),
		Schema:      schemaMap,
	}

	// Extract engines and potentially merge engine-specific capabilities if it's an AigoTool.
	if at, ok := t.(*aigotools.AigoTool); ok {
		result.Engines = at.Engines()

		// If a specific engine is requested, try to get its capabilities to override schema enums
		targetEngine := engineName
		if targetEngine == "" && len(result.Engines) > 0 {
			targetEngine = result.Engines[0]
		}

		if targetEngine != "" {
			// Get capabilities for the specific engine
			cap := at.EngineCapabilities(targetEngine)
			if cap != nil {
				// Merge capabilities into schema properties (e.g. voices, models, sizes)
				if props, ok := result.Schema["properties"].(map[string]any); ok {
					if len(cap.Voices) > 0 {
						props["voice"] = map[string]any{"type": "string", "enum": cap.Voices}
					}
					if len(cap.Models) > 0 {
						props["model"] = map[string]any{"type": "string", "enum": cap.Models}
					}
					if len(cap.Sizes) > 0 {
						props["size"] = map[string]any{"type": "string", "enum": cap.Sizes}
					}
				}
			}
		}
	}

	return result, nil
}

// AigoModels returns all known aigo models grouped by provider and capability.
func (rt *Runtime) AigoModels() map[string]map[string][]string {
	return aigotools.AvailableModels()
}

// AigoProviders returns all known providers with config schemas and models.
func (rt *Runtime) AigoProviders() []aigotools.ProviderInfo {
	return aigotools.AvailableProviders()
}

// AigoProviderStatus checks connectivity of configured aigo providers.
func (rt *Runtime) AigoProviderStatus() []aigotools.ProviderStatus {
	s := rt.Settings()
	if s == nil || s.Aigo == nil {
		return nil
	}
	return aigotools.CheckProviderConnectivity(s.Aigo.Providers)
}

// PersonaRegistry returns the persona registry (may be nil if no personas configured).
func (rt *Runtime) PersonaRegistry() *persona.Registry { return rt.personaRegistry }

// ProjectRoot returns the project root directory.
func (rt *Runtime) ProjectRoot() string { return rt.opts.ProjectRoot }

func (rt *Runtime) ConfigRoot() string { return rt.opts.ConfigRoot }

// SessionDB returns the SQLite session index store (may be nil).
func (rt *Runtime) SessionDB() *sessiondb.Store { return rt.sessionDB }

// ModelName returns the name of the currently active model.
func (rt *Runtime) ModelName() string {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if namer, ok := rt.opts.Model.(model.ModelNamer); ok {
		return namer.ModelName()
	}
	return ""
}

// SetModel hot-swaps the active model at runtime.
func (rt *Runtime) SetModel(ctx context.Context, modelName string) error {
	if strings.TrimSpace(modelName) == "" {
		return fmt.Errorf("model name is required")
	}

	// Detect provider from model name prefix.
	entry := config.FailoverModelEntry{Model: modelName}
	switch {
	case strings.HasPrefix(modelName, "gpt-") || strings.HasPrefix(modelName, "o1") || strings.HasPrefix(modelName, "o3") || strings.HasPrefix(modelName, "o4"):
		entry.Provider = "openai"
	default:
		entry.Provider = "anthropic"
	}

	newModel, err := rt.createModelFromEntry(entry)
	if err != nil {
		return fmt.Errorf("switch model to %s: %w", modelName, err)
	}

	rt.mu.Lock()
	rt.opts.Model = newModel
	rt.mu.Unlock()
	return nil
}

// ReloadSettings re-reads settings from disk and updates the runtime snapshot.
func (rt *Runtime) ReloadSettings() error {
	loader := &config.SettingsLoader{
		ProjectRoot: rt.opts.ProjectRoot,
		ConfigRoot:  rt.opts.ConfigRoot,
	}
	s, err := loader.Load()
	if err != nil {
		return err
	}

	// Rebuild persona registry and router from updated settings.
	newRegistry, newRouter := initPersonas(rt.opts, s)

	rt.mu.Lock()
	rt.settings = s
	rt.cfg = config.MergeSettings(nil, s)
	rt.personaRegistry = newRegistry
	rt.personaRouter = newRouter
	rt.mu.Unlock()
	return nil
}

// Sandbox exposes the sandbox manager.
func (rt *Runtime) Sandbox() *sandbox.Manager { return rt.sandbox }

// MemoryStore returns the session memory store, or nil if not configured.
func (rt *Runtime) MemoryStore() *memory.Store { return rt.memoryStore }

// GetSessionStats returns aggregated token stats for a session.
func (rt *Runtime) GetSessionStats(sessionID string) *SessionTokenStats {
	if rt == nil || rt.tokens == nil {
		return nil
	}
	return rt.tokens.GetSessionStats(sessionID)
}

// GetTotalStats returns aggregated token stats across all sessions.
func (rt *Runtime) GetTotalStats() *SessionTokenStats {
	if rt == nil || rt.tokens == nil {
		return nil
	}
	return rt.tokens.GetTotalStats()
}

// ListMonitors returns the status of all active stream monitors.
func (rt *Runtime) ListMonitors() []toolbuiltin.MonitorInfo {
	if rt == nil || rt.streamMonitor == nil {
		return nil
	}
	return rt.streamMonitor.ListMonitors()
}

// StreamMonitorTool returns the stream monitor tool instance, or nil if not registered.
func (rt *Runtime) StreamMonitorTool() *toolbuiltin.StreamMonitorTool {
	if rt == nil {
		return nil
	}
	return rt.streamMonitor
}