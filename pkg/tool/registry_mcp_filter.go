package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/saker-ai/saker/pkg/mcp"
)

func buildRemoteToolWrappers(session *mcp.ClientSession, serverName string, tools []*mcp.Tool, opts MCPServerOptions) ([]Tool, []string, error) {
	wrappers := make([]Tool, 0, len(tools))
	names := make([]string, 0, len(tools))
	seen := map[string]struct{}{}
	filter := newMCPToolFilter(opts.EnabledTools, opts.DisabledTools)
	for _, desc := range tools {
		if desc == nil || strings.TrimSpace(desc.Name) == "" {
			return nil, nil, fmt.Errorf("encountered MCP tool with empty name")
		}
		toolName := desc.Name
		if serverName != "" {
			toolName = fmt.Sprintf("%s__%s", serverName, desc.Name)
		}
		if !filter.allows(desc.Name, toolName) {
			continue
		}
		if _, ok := seen[toolName]; ok {
			return nil, nil, fmt.Errorf("tool %s already registered", toolName)
		}
		seen[toolName] = struct{}{}
		schema, err := convertMCPSchema(desc.InputSchema)
		if err != nil {
			return nil, nil, fmt.Errorf("parse schema for %s: %w", desc.Name, err)
		}
		wrappers = append(wrappers, &remoteTool{
			name:        toolName,
			remoteName:  desc.Name,
			description: desc.Description,
			schema:      schema,
			session:     session,
			timeout:     opts.ToolTimeout,
		})
		names = append(names, toolName)
	}
	if len(wrappers) == 0 {
		return nil, nil, fmt.Errorf("MCP server returned no tools after applying filters")
	}
	return wrappers, names, nil
}

func toNameSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

type mcpToolFilter struct {
	enabled  map[string]struct{}
	disabled map[string]struct{}
}

func newMCPToolFilter(enabled, disabled []string) mcpToolFilter {
	return mcpToolFilter{
		enabled:  normalizeMCPToolNameSet(enabled),
		disabled: normalizeMCPToolNameSet(disabled),
	}
}

func normalizeMCPToolNameSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		out[name] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (f mcpToolFilter) allows(remoteName, localName string) bool {
	if len(f.enabled) > 0 && !f.matches(f.enabled, remoteName, localName) {
		return false
	}
	if len(f.disabled) > 0 && f.matches(f.disabled, remoteName, localName) {
		return false
	}
	return true
}

func (f mcpToolFilter) matches(set map[string]struct{}, names ...string) bool {
	if len(set) == 0 {
		return false
	}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, ok := set[name]; ok {
			return true
		}
	}
	return false
}

func cloneMCPServerOptions(src MCPServerOptions) MCPServerOptions {
	out := src
	out.Headers = cloneStringMap(src.Headers)
	out.Env = cloneStringMap(src.Env)
	out.EnabledTools = append([]string(nil), src.EnabledTools...)
	out.DisabledTools = append([]string(nil), src.DisabledTools...)
	return out
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func convertMCPSchema(raw any) (*JSONSchema, error) {
	if raw == nil {
		return nil, nil
	}
	var (
		data []byte
		err  error
	)
	switch v := raw.(type) {
	case json.RawMessage:
		if len(v) == 0 {
			return nil, nil
		}
		data = v
	case []byte:
		if len(v) == 0 {
			return nil, nil
		}
		data = v
	default:
		data, err = json.Marshal(raw)
		if err != nil {
			return nil, err
		}
	}
	var schema JSONSchema
	if err := json.Unmarshal(data, &schema); err == nil && schema.Type != "" {
		return &schema, nil
	}
	var generic map[string]interface{}
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil, err
	}
	if t, ok := generic["type"].(string); ok {
		schema.Type = t
	}
	if props, ok := generic["properties"].(map[string]interface{}); ok {
		schema.Properties = props
	}
	if req, ok := generic["required"].([]interface{}); ok {
		for _, value := range req {
			if name, ok := value.(string); ok {
				schema.Required = append(schema.Required, name)
			}
		}
	}
	return &schema, nil
}
