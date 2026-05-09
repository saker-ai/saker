package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	toolNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
)

// ValidateSettings checks the merged Settings structure for logical consistency.
// Aggregates all failures using errors.Join so callers can surface every issue at once.
func ValidateSettings(s *Settings) error {
	if s == nil {
		return errors.New("settings is nil")
	}

	var errs []error

	// model
	if strings.TrimSpace(s.Model) == "" {
		errs = append(errs, errors.New("model is required"))
	}

	// permissions
	errs = append(errs, validatePermissionsConfig(s.Permissions)...)

	// hooks
	errs = append(errs, validateHooksConfig(s.Hooks)...)

	// sandbox
	errs = append(errs, validateSandboxConfig(s.Sandbox)...)

	// bash output spooling thresholds
	errs = append(errs, validateBashOutputConfig(s.BashOutput)...)

	// tool output persistence thresholds
	errs = append(errs, validateToolOutputConfig(s.ToolOutput)...)

	// mcp
	errs = append(errs, validateMCPConfig(s.MCP, s.LegacyMCPServers)...)

	// status line
	errs = append(errs, validateStatusLineConfig(s.StatusLine)...)

	// personas
	errs = append(errs, validatePersonasConfig(s.Personas)...)

	// force login options
	errs = append(errs, validateForceLoginConfig(s.ForceLoginMethod, s.ForceLoginOrgUUID)...)

	// storage
	errs = append(errs, validateStorageConfig(s.Storage)...)

	// aigo
	errs = append(errs, validateAigoConfig(s.Aigo)...)

	// failover
	errs = append(errs, validateFailoverConfig(s.Failover)...)

	// web auth
	errs = append(errs, validateWebAuthConfig(s.WebAuth)...)

	// cors
	errs = append(errs, validateCORSConfig(s.CORS)...)

	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func validatePermissionsConfig(p *PermissionsConfig) []error {
	if p == nil {
		return nil
	}
	var errs []error

	mode := strings.TrimSpace(p.DefaultMode)
	switch mode {
	case "askBeforeRunningTools", "acceptReadOnly", "acceptEdits", "bypassPermissions":
	case "":
		errs = append(errs, errors.New("permissions.defaultMode is required"))
	default:
		errs = append(errs, fmt.Errorf("permissions.defaultMode %q is not supported", mode))
	}

	if p.DisableBypassPermissionsMode != "" && p.DisableBypassPermissionsMode != "disable" {
		errs = append(errs, fmt.Errorf("permissions.disableBypassPermissionsMode must be \"disable\", got %q", p.DisableBypassPermissionsMode))
	}

	errs = append(errs, validateRuleSlice("permissions.allow", p.Allow)...)
	errs = append(errs, validateRuleSlice("permissions.ask", p.Ask)...)
	errs = append(errs, validateRuleSlice("permissions.deny", p.Deny)...)

	for i, dir := range p.AdditionalDirectories {
		if strings.TrimSpace(dir) == "" {
			errs = append(errs, fmt.Errorf("permissions.additionalDirectories[%d] is empty", i))
		}
	}

	return errs
}

func validateRuleSlice(label string, rules []string) []error {
	var errs []error
	for i, rule := range rules {
		if err := validatePermissionRule(rule); err != nil {
			errs = append(errs, fmt.Errorf("%s[%d]: %w", label, i, err))
		}
	}
	return errs
}

// validatePermissionRule enforces the Tool(target) pattern used by allow/ask/deny.
func validatePermissionRule(rule string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return errors.New("permission rule is empty")
	}

	if !strings.Contains(rule, "(") {
		return nil
	}

	if !strings.HasSuffix(rule, ")") {
		return fmt.Errorf("permission rule %q must end with )", rule)
	}
	if strings.Count(rule, "(") != 1 || strings.Count(rule, ")") != 1 {
		return fmt.Errorf("permission rule %q must look like Tool(pattern)", rule)
	}
	open := strings.IndexRune(rule, '(')
	tool := rule[:open]
	target := rule[open+1 : len(rule)-1]
	if err := validateToolName(tool); err != nil {
		return fmt.Errorf("invalid tool name: %w", err)
	}
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("permission rule %q target is empty", rule)
	}
	return nil
}

// validateToolName ensures hooks and permission prefixes use a predictable charset.
func validateToolName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("tool name is empty")
	}
	if !toolNamePattern.MatchString(name) {
		return fmt.Errorf("tool name %q must match %s", name, toolNamePattern.String())
	}
	return nil
}

// validateToolPattern accepts literal tool names, wildcard "*", and arbitrary regex patterns.
// Selector in pkg/core/hooks compiles the provided string, so we enforce regex validity here
// while still allowing the catch-all wildcard used in configs.
func validateToolPattern(pattern string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return errors.New("tool pattern is empty")
	}
	if pattern == "*" {
		return nil
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("tool pattern %q is not a valid regexp: %w", pattern, err)
	}
	return nil
}

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

func validateStatusLineConfig(cfg *StatusLineConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	typ := strings.TrimSpace(cfg.Type)
	switch typ {
	case "command":
		if strings.TrimSpace(cfg.Command) == "" {
			errs = append(errs, errors.New("statusLine.command is required when type=command"))
		}
	case "template":
		if strings.TrimSpace(cfg.Template) == "" {
			errs = append(errs, errors.New("statusLine.template is required when type=template"))
		}
	case "":
		errs = append(errs, errors.New("statusLine.type is required"))
	default:
		errs = append(errs, fmt.Errorf("statusLine.type %q is not supported", cfg.Type))
	}
	if cfg.IntervalSeconds < 0 {
		errs = append(errs, errors.New("statusLine.intervalSeconds cannot be negative"))
	}
	if cfg.TimeoutSeconds < 0 {
		errs = append(errs, errors.New("statusLine.timeoutSeconds cannot be negative"))
	}
	return errs
}

func validatePersonasConfig(cfg *PersonasConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	for id, p := range cfg.Profiles {
		if strings.TrimSpace(id) == "" {
			errs = append(errs, errors.New("personas: profile ID cannot be empty"))
		}
		if p.Inherit != "" {
			if _, ok := cfg.Profiles[p.Inherit]; !ok {
				errs = append(errs, fmt.Errorf("personas: profile %q inherits unknown profile %q", id, p.Inherit))
			}
		}
	}
	for i, route := range cfg.Routes {
		if strings.TrimSpace(route.Channel) == "" {
			errs = append(errs, fmt.Errorf("personas: route[%d] channel is required", i))
		}
		if strings.TrimSpace(route.Persona) == "" {
			errs = append(errs, fmt.Errorf("personas: route[%d] persona is required", i))
		}
	}
	return errs
}

func validateForceLoginConfig(method, org string) []error {
	rawOrg := org
	method = strings.TrimSpace(method)
	org = strings.TrimSpace(org)
	if method == "" {
		return nil
	}

	var errs []error
	if method != "claudeai" && method != "console" {
		errs = append(errs, fmt.Errorf("forceLoginMethod must be \"claudeai\" or \"console\", got %q", method))
	}
	if rawOrg != "" && org == "" {
		errs = append(errs, errors.New("forceLoginOrgUUID cannot be blank"))
	}
	return errs
}

func validateStorageConfig(cfg *StorageConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	backend := strings.TrimSpace(cfg.Backend)
	switch backend {
	case "", "osfs", "memfs", "embedded", "s3":
	default:
		errs = append(errs, fmt.Errorf("storage.backend %q is not supported (use osfs, memfs, embedded, or s3)", backend))
	}
	if backend == "s3" {
		if cfg.S3 == nil {
			errs = append(errs, errors.New("storage.s3 is required when backend is s3"))
		} else {
			if strings.TrimSpace(cfg.S3.Bucket) == "" {
				errs = append(errs, errors.New("storage.s3.bucket is required"))
			}
			if strings.TrimSpace(cfg.S3.AccessKeyID) == "" {
				errs = append(errs, errors.New("storage.s3.accessKeyID is required"))
			}
			if strings.TrimSpace(cfg.S3.SecretAccessKey) == "" {
				errs = append(errs, errors.New("storage.s3.secretAccessKey is required"))
			}
		}
	}
	if backend == "embedded" && cfg.Embedded != nil {
		mode := strings.TrimSpace(cfg.Embedded.Mode)
		switch mode {
		case "", "external", "standalone":
		default:
			errs = append(errs, fmt.Errorf("storage.embedded.mode %q is not supported (use external or standalone)", mode))
		}
	}
	return errs
}

func validateAigoConfig(cfg *AigoConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	if len(cfg.Providers) == 0 {
		errs = append(errs, errors.New("aigo.providers is required when aigo is configured"))
	}
	for alias, p := range cfg.Providers {
		if strings.TrimSpace(alias) == "" {
			errs = append(errs, errors.New("aigo.providers: alias cannot be empty"))
			continue
		}
		if strings.TrimSpace(p.Type) == "" {
			errs = append(errs, fmt.Errorf("aigo.providers[%s].type is required", alias))
		}
		if strings.TrimSpace(p.APIKey) == "" && strings.TrimSpace(p.BaseURL) == "" {
			errs = append(errs, fmt.Errorf("aigo.providers[%s]: apiKey or baseUrl is required", alias))
		}
	}
	if cfg.Timeout != "" {
		if _, err := time.ParseDuration(cfg.Timeout); err != nil {
			errs = append(errs, fmt.Errorf("aigo.timeout %q is not a valid duration: %w", cfg.Timeout, err))
		}
	}
	return errs
}

func validateFailoverConfig(cfg *FailoverConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	if cfg.Enabled == nil || !*cfg.Enabled {
		return nil
	}
	if len(cfg.Models) == 0 {
		errs = append(errs, errors.New("failover.models is required when failover is enabled"))
	}
	for i, m := range cfg.Models {
		if strings.TrimSpace(m.Provider) == "" {
			errs = append(errs, fmt.Errorf("failover.models[%d].provider is required", i))
		}
		if strings.TrimSpace(m.Model) == "" {
			errs = append(errs, fmt.Errorf("failover.models[%d].model is required", i))
		}
	}
	if cfg.MaxRetries < 0 {
		errs = append(errs, fmt.Errorf("failover.maxRetries must be >=0, got %d", cfg.MaxRetries))
	}
	return errs
}

func validateWebAuthConfig(cfg *WebAuthConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	for i, u := range cfg.Users {
		if strings.TrimSpace(u.Username) == "" {
			errs = append(errs, fmt.Errorf("webAuth.users[%d].username is required", i))
		}
		if strings.TrimSpace(u.Password) == "" {
			errs = append(errs, fmt.Errorf("webAuth.users[%d].password is required", i))
		}
	}
	if cfg.LDAP != nil && cfg.LDAP.Enabled {
		if strings.TrimSpace(cfg.LDAP.URL) == "" {
			errs = append(errs, errors.New("webAuth.ldap.url is required when LDAP is enabled"))
		}
		if strings.TrimSpace(cfg.LDAP.BaseDN) == "" {
			errs = append(errs, errors.New("webAuth.ldap.baseDN is required when LDAP is enabled"))
		}
	}
	if cfg.OIDC != nil && cfg.OIDC.Enabled {
		if strings.TrimSpace(cfg.OIDC.Issuer) == "" {
			errs = append(errs, errors.New("webAuth.oidc.issuer is required when OIDC is enabled"))
		}
		if strings.TrimSpace(cfg.OIDC.ClientID) == "" {
			errs = append(errs, errors.New("webAuth.oidc.clientId is required when OIDC is enabled"))
		}
	}
	if cfg.RoleMapping != nil {
		role := strings.TrimSpace(cfg.RoleMapping.DefaultRole)
		switch role {
		case "", "user", "admin":
		default:
			errs = append(errs, fmt.Errorf("webAuth.roleMapping.defaultRole %q is not supported (use user or admin)", role))
		}
	}
	return errs
}

func validateCORSConfig(cfg *CORSConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	for i, origin := range cfg.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			errs = append(errs, fmt.Errorf("cors.allowedOrigins[%d] is empty", i))
			continue
		}
		if u, err := url.Parse(origin); err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, fmt.Errorf("cors.allowedOrigins[%d] %q is not a valid URL", i, origin))
		}
	}
	return errs
}
