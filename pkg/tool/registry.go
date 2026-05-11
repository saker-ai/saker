// Public Registry surface lives here; MCP integration, transport, and remote
// tool wrappers are split into the sibling registry_mcp*.go files.
package tool

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/metrics"
)

// Registry keeps the mapping between tool names and implementations.
type Registry struct {
	mu          sync.RWMutex
	tools       map[string]Tool
	sources     map[string]string // tool name → source category
	mcpSessions []*mcpSessionInfo
	validator   Validator
}

// NewRegistry creates a registry backed by the default validator.
func NewRegistry() *Registry {
	return &Registry{
		tools:     make(map[string]Tool),
		sources:   make(map[string]string),
		validator: DefaultValidator{},
	}
}

// Register inserts a tool when its name is not in use.
func (r *Registry) Register(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("tool is nil")
	}
	name := tool.Name()
	if name == "" {
		return fmt.Errorf("tool name is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %s already registered", name)
	}

	r.tools[name] = tool
	r.sources[name] = "builtin"
	return nil
}

// RegisterWithSource inserts a tool with an explicit source category.
func (r *Registry) RegisterWithSource(tool Tool, source string) error {
	if err := r.Register(tool); err != nil {
		return err
	}
	r.mu.Lock()
	r.sources[tool.Name()] = source
	r.mu.Unlock()
	return nil
}

// ToolSource returns the source category of a registered tool.
func (r *Registry) ToolSource(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if s, ok := r.sources[name]; ok {
		return s
	}
	return "builtin"
}

// Get fetches a tool by name.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool %s not found", name)
	}
	return tool, nil
}

// List produces a snapshot of all registered tools, sorted by name for stable ordering.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name() < tools[j].Name()
	})
	return tools
}

// SetValidator swaps the validator instance used before execution.
func (r *Registry) SetValidator(v Validator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.validator = v
}

// Execute runs a registered tool after optional schema validation.
func (r *Registry) Execute(ctx context.Context, name string, params map[string]interface{}) (_ *ToolResult, err error) {
	tool, err := r.Get(name)
	if err != nil {
		// "tool not registered" is a configuration error, not a tool
		// invocation outcome — don't pollute the per-tool histogram.
		return nil, err
	}

	if schema := tool.Schema(); schema != nil {
		r.mu.RLock()
		validator := r.validator
		r.mu.RUnlock()

		if validator != nil {
			if err := validator.Validate(params, schema); err != nil {
				metrics.ToolInvocationsTotal.WithLabelValues(name, metrics.StatusError).Inc()
				return nil, fmt.Errorf("tool %s validation failed: %w", name, err)
			}
		}
	}

	start := time.Now()
	result, execErr := tool.Execute(ctx, params)
	metrics.ToolInvocationsTotal.WithLabelValues(name, metrics.ClassifyErr(execErr)).Inc()
	metrics.ObserveSince(metrics.ToolDuration.WithLabelValues(name), start)
	return result, execErr
}

// Close terminates all tracked MCP sessions.
// Errors are logged and ignored to avoid masking shutdown flows.
func (r *Registry) Close() {
	r.mu.Lock()
	sessions := r.mcpSessions
	r.mcpSessions = nil
	r.mu.Unlock()

	for _, info := range sessions {
		if info == nil || info.session == nil {
			continue
		}
		if err := info.session.Close(); err != nil {
			slog.Error("tool registry: close MCP session", "error", err)
		}
	}
}
