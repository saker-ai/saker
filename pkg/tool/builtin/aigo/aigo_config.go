package aigo

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	sdk "github.com/godeps/aigo"
	"github.com/godeps/aigo/tooldef"

	"github.com/saker-ai/saker/pkg/config"
	"github.com/saker-ai/saker/pkg/tool"
)

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
