package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// validator_hooks.go owns validation for hooks/sandbox/output/MCP blocks plus
// the shared port-range helper used by sandbox.network. Permission checks
// live in validator_permissions.go; everything else (status, personas,
// storage, aigo, failover, web/cors) in validator_misc.go.

func validateHooksConfig(h *HooksConfig) []error {
	if h == nil {
		return nil
	}
	var errs []error
	errs = append(errs, validateHookEntries("hooks.PreToolUse", h.PreToolUse)...)
	errs = append(errs, validateHookEntries("hooks.PostToolUse", h.PostToolUse)...)
	errs = append(errs, validateHookEntries("hooks.PostToolUseFailure", h.PostToolUseFailure)...)
	errs = append(errs, validateHookEntries("hooks.PermissionRequest", h.PermissionRequest)...)
	errs = append(errs, validateHookEntries("hooks.SessionStart", h.SessionStart)...)
	errs = append(errs, validateHookEntries("hooks.SessionEnd", h.SessionEnd)...)
	errs = append(errs, validateHookEntries("hooks.SubagentStart", h.SubagentStart)...)
	errs = append(errs, validateHookEntries("hooks.SubagentStop", h.SubagentStop)...)
	errs = append(errs, validateHookEntries("hooks.Stop", h.Stop)...)
	errs = append(errs, validateHookEntries("hooks.Notification", h.Notification)...)
	errs = append(errs, validateHookEntries("hooks.UserPromptSubmit", h.UserPromptSubmit)...)
	errs = append(errs, validateHookEntries("hooks.PreCompact", h.PreCompact)...)
	return errs
}

func validateHookEntries(label string, entries []HookMatcherEntry) []error {
	if len(entries) == 0 {
		return nil
	}
	var errs []error
	for i, entry := range entries {
		if entry.Matcher != "" && entry.Matcher != "*" {
			if err := validateToolPattern(entry.Matcher); err != nil {
				errs = append(errs, fmt.Errorf("%s[%d].matcher: %w", label, i, err))
			}
		}
		if len(entry.Hooks) == 0 {
			errs = append(errs, fmt.Errorf("%s[%d]: hooks array is empty", label, i))
			continue
		}
		for j, hook := range entry.Hooks {
			switch hook.Type {
			case "command", "":
				if strings.TrimSpace(hook.Command) == "" {
					errs = append(errs, fmt.Errorf("%s[%d].hooks[%d]: command is required for type %q", label, i, j, hook.Type))
				}
			case "prompt":
				if strings.TrimSpace(hook.Prompt) == "" {
					errs = append(errs, fmt.Errorf("%s[%d].hooks[%d]: prompt is required for type \"prompt\"", label, i, j))
				}
			case "agent":
				// agent hooks require a prompt
				if strings.TrimSpace(hook.Prompt) == "" {
					errs = append(errs, fmt.Errorf("%s[%d].hooks[%d]: prompt is required for type \"agent\"", label, i, j))
				}
			default:
				errs = append(errs, fmt.Errorf("%s[%d].hooks[%d]: unsupported type %q", label, i, j, hook.Type))
			}
			if hook.Timeout < 0 {
				errs = append(errs, fmt.Errorf("%s[%d].hooks[%d]: timeout must be >= 0", label, i, j))
			}
		}
	}
	return errs
}

func validateSandboxConfig(s *SandboxConfig) []error {
	if s == nil {
		return nil
	}
	var errs []error
	for i, cmd := range s.ExcludedCommands {
		if strings.TrimSpace(cmd) == "" {
			errs = append(errs, fmt.Errorf("sandbox.excludedCommands[%d] is empty", i))
		}
	}
	if s.Network != nil {
		if s.Network.HTTPProxyPort != nil {
			if err := validatePortRange(*s.Network.HTTPProxyPort); err != nil {
				errs = append(errs, fmt.Errorf("sandbox.network.httpProxyPort: %w", err))
			}
		}
		if s.Network.SocksProxyPort != nil {
			if err := validatePortRange(*s.Network.SocksProxyPort); err != nil {
				errs = append(errs, fmt.Errorf("sandbox.network.socksProxyPort: %w", err))
			}
		}
	}
	return errs
}

func validateBashOutputConfig(cfg *BashOutputConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	if cfg.SyncThresholdBytes != nil {
		if v := *cfg.SyncThresholdBytes; v <= 0 {
			errs = append(errs, fmt.Errorf("bashOutput.syncThresholdBytes must be >0, got %d", v))
		}
	}
	if cfg.AsyncThresholdBytes != nil {
		if v := *cfg.AsyncThresholdBytes; v <= 0 {
			errs = append(errs, fmt.Errorf("bashOutput.asyncThresholdBytes must be >0, got %d", v))
		}
	}
	return errs
}

func validateToolOutputConfig(cfg *ToolOutputConfig) []error {
	if cfg == nil {
		return nil
	}

	var errs []error
	if cfg.DefaultThresholdBytes < 0 {
		errs = append(errs, fmt.Errorf("toolOutput.defaultThresholdBytes must be >=0, got %d", cfg.DefaultThresholdBytes))
	}

	if len(cfg.PerToolThresholdBytes) == 0 {
		return errs
	}

	names := make([]string, 0, len(cfg.PerToolThresholdBytes))
	for name := range cfg.PerToolThresholdBytes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := name
		name = strings.TrimSpace(name)
		if name == "" {
			errs = append(errs, errors.New("toolOutput.perToolThresholdBytes has an empty tool name"))
			continue
		}
		if raw != name {
			errs = append(errs, fmt.Errorf("toolOutput.perToolThresholdBytes[%s] tool name must not include leading/trailing whitespace", raw))
		}
		if strings.ToLower(name) != name {
			errs = append(errs, fmt.Errorf("toolOutput.perToolThresholdBytes[%s] tool name must be lowercase", raw))
		}
		if v := cfg.PerToolThresholdBytes[raw]; v <= 0 {
			errs = append(errs, fmt.Errorf("toolOutput.perToolThresholdBytes[%s] must be >0, got %d", raw, v))
		}
	}

	return errs
}

func validateMCPConfig(cfg *MCPConfig, legacy []string) []error {
	var errs []error
	if len(legacy) > 0 {
		errs = append(errs, errors.New("mcpServers is deprecated; migrate to mcp.servers map"))
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		return errs
	}
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			errs = append(errs, errors.New("mcp.servers has an empty name"))
			continue
		}
		entry := cfg.Servers[name]
		serverType := strings.ToLower(strings.TrimSpace(entry.Type))
		if serverType == "" {
			serverType = "stdio"
		}
		if entry.TimeoutSeconds < 0 {
			errs = append(errs, fmt.Errorf("mcp.servers[%s].timeoutSeconds must be >=0", name))
		}
		if entry.ToolTimeoutSeconds < 0 {
			errs = append(errs, fmt.Errorf("mcp.servers[%s].toolTimeoutSeconds must be >=0", name))
		}
		switch serverType {
		case "stdio":
			if strings.TrimSpace(entry.Command) == "" {
				errs = append(errs, fmt.Errorf("mcp.servers[%s].command is required for type stdio", name))
			}
		case "http", "sse":
			if strings.TrimSpace(entry.URL) == "" {
				errs = append(errs, fmt.Errorf("mcp.servers[%s].url is required for type %s", name, serverType))
			}
		default:
			errs = append(errs, fmt.Errorf("mcp.servers[%s].type %q is not supported", name, entry.Type))
		}
		for k := range entry.Headers {
			if strings.TrimSpace(k) == "" {
				errs = append(errs, fmt.Errorf("mcp.servers[%s].headers contains empty key", name))
				break
			}
		}
		errs = append(errs, validateMCPToolList(name, "enabledTools", entry.EnabledTools)...)
		errs = append(errs, validateMCPToolList(name, "disabledTools", entry.DisabledTools)...)
	}
	return errs
}

func validateMCPToolList(serverName, field string, tools []string) []error {
	if len(tools) == 0 {
		return nil
	}
	seen := make(map[string]int, len(tools))
	var errs []error
	for idx, raw := range tools {
		name := strings.TrimSpace(raw)
		if name == "" {
			errs = append(errs, fmt.Errorf("mcp.servers[%s].%s[%d] cannot be empty", serverName, field, idx))
			continue
		}
		if prev, ok := seen[name]; ok {
			errs = append(errs, fmt.Errorf("mcp.servers[%s].%s[%d] duplicates entry at index %d (%q)", serverName, field, idx, prev, name))
			continue
		}
		seen[name] = idx
	}
	return errs
}

// validatePortRange expects a TCP/UDP port in the inclusive 1-65535 range.
func validatePortRange(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d out of range (1-65535)", port)
	}
	return nil
}
