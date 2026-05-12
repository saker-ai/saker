package config

// This file provides pure, allocation-safe merge helpers for Settings.
// All functions return new objects and never mutate inputs.
//
// The merge implementation is split across sibling files to keep each file
// focused and within the 600-LOC cap:
//   - settings_merge.go (this file): top-level MergeSettings + cloneSettings
//   - settings_merge_collections.go: string slice / map / MCP rule helpers
//   - settings_merge_hooks.go:       PermissionsConfig, HooksConfig,
//                                    SandboxConfig (+Network), Bash/Tool
//                                    output, MCP, StatusLine merge & clone
//   - settings_merge_storage.go:     FailoverConfig, WebAuthConfig,
//                                    StorageConfig (+OSFS/Embedded/S3),
//                                    AigoConfig merge & clone

// MergeSettings deep-merges two Settings structs (lower <- higher) and returns a new instance.
// - Scalars: higher non-zero values override lower.
// - *bool / *int pointers: higher non-nil overrides lower.
// - Maps: merged per key with higher entries overriding.
// - []string: concatenated with de-duplication, preserving order.
// - Nested structs: merged recursively.
func MergeSettings(lower, higher *Settings) *Settings {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneSettings(higher)
	}
	if higher == nil {
		return cloneSettings(lower)
	}

	result := cloneSettings(lower)

	if higher.APIKeyHelper != "" {
		result.APIKeyHelper = higher.APIKeyHelper
	}
	if higher.CleanupPeriodDays != nil {
		result.CleanupPeriodDays = intPtr(*higher.CleanupPeriodDays)
	}
	result.CompanyAnnouncements = mergeStringSlices(lower.CompanyAnnouncements, higher.CompanyAnnouncements)
	result.Env = mergeMaps(lower.Env, higher.Env)
	if higher.IncludeCoAuthoredBy != nil {
		result.IncludeCoAuthoredBy = boolPtr(*higher.IncludeCoAuthoredBy)
	}
	result.Permissions = mergePermissions(lower.Permissions, higher.Permissions)
	result.DisallowedTools = mergeStringSlices(lower.DisallowedTools, higher.DisallowedTools)
	result.Hooks = mergeHooks(lower.Hooks, higher.Hooks)
	if higher.DisableAllHooks != nil {
		result.DisableAllHooks = boolPtr(*higher.DisableAllHooks)
	}
	if higher.Model != "" {
		result.Model = higher.Model
	}
	result.MCP = mergeMCPConfig(lower.MCP, higher.MCP)
	result.LegacyMCPServers = mergeStringSlices(lower.LegacyMCPServers, higher.LegacyMCPServers)
	result.StatusLine = mergeStatusLine(lower.StatusLine, higher.StatusLine)
	if higher.OutputStyle != "" {
		result.OutputStyle = higher.OutputStyle
	}
	if higher.ForceLoginMethod != "" {
		result.ForceLoginMethod = higher.ForceLoginMethod
	}
	if higher.ForceLoginOrgUUID != "" {
		result.ForceLoginOrgUUID = higher.ForceLoginOrgUUID
	}
	result.Sandbox = mergeSandbox(lower.Sandbox, higher.Sandbox)
	result.BashOutput = mergeBashOutput(lower.BashOutput, higher.BashOutput)
	result.ToolOutput = mergeToolOutput(lower.ToolOutput, higher.ToolOutput)
	result.AllowedMcpServers = mergeMCPServerRules(lower.AllowedMcpServers, higher.AllowedMcpServers)
	result.DeniedMcpServers = mergeMCPServerRules(lower.DeniedMcpServers, higher.DeniedMcpServers)
	if higher.AWSAuthRefresh != "" {
		result.AWSAuthRefresh = higher.AWSAuthRefresh
	}
	if higher.AWSCredentialExport != "" {
		result.AWSCredentialExport = higher.AWSCredentialExport
	}
	if higher.RespectGitignore != nil {
		result.RespectGitignore = boolPtr(*higher.RespectGitignore)
	}
	if higher.WebAuth != nil {
		result.WebAuth = cloneWebAuth(higher.WebAuth)
	}
	if higher.Aigo != nil {
		result.Aigo = cloneAigoConfig(higher.Aigo)
	}
	if higher.Storage != nil {
		result.Storage = mergeStorage(lower.Storage, higher.Storage)
	}
	if higher.ACP != nil {
		result.ACP = higher.ACP
	}
	result.Failover = mergeFailover(lower.Failover, higher.Failover)
	if higher.Personas != nil {
		result.Personas = higher.Personas
	}
	if higher.UserPersonas != nil {
		result.UserPersonas = higher.UserPersonas
	}
	result.Bifrost = mergeBifrost(lower.Bifrost, higher.Bifrost)
	result.Governance = mergeGovernance(lower.Governance, higher.Governance)
	return result
}

// cloneSettings deep-copies a Settings instance so callers can safely mutate
// the result without aliasing the source. Field-level helpers live in the
// sibling _hooks / _storage / _collections files.
func cloneSettings(src *Settings) *Settings {
	if src == nil {
		return nil
	}
	out := *src
	out.CompanyAnnouncements = mergeStringSlices(nil, src.CompanyAnnouncements)
	out.Env = mergeMaps(nil, src.Env)
	out.IncludeCoAuthoredBy = cloneBoolPtr(src.IncludeCoAuthoredBy)
	out.Permissions = clonePermissions(src.Permissions)
	out.DisallowedTools = mergeStringSlices(nil, src.DisallowedTools)
	out.Hooks = cloneHooks(src.Hooks)
	out.DisableAllHooks = cloneBoolPtr(src.DisableAllHooks)
	out.StatusLine = cloneStatusLine(src.StatusLine)
	out.Sandbox = cloneSandbox(src.Sandbox)
	out.BashOutput = cloneBashOutput(src.BashOutput)
	out.ToolOutput = cloneToolOutput(src.ToolOutput)
	out.AllowedMcpServers = mergeMCPServerRules(nil, src.AllowedMcpServers)
	out.DeniedMcpServers = mergeMCPServerRules(nil, src.DeniedMcpServers)
	out.MCP = cloneMCPConfig(src.MCP)
	out.LegacyMCPServers = mergeStringSlices(nil, src.LegacyMCPServers)
	out.RespectGitignore = cloneBoolPtr(src.RespectGitignore)
	out.Failover = cloneFailover(src.Failover)
	out.WebAuth = cloneWebAuth(src.WebAuth)
	out.Aigo = cloneAigoConfig(src.Aigo)
	out.Storage = cloneStorageConfig(src.Storage)
	out.Bifrost = cloneBifrost(src.Bifrost)
	out.Governance = cloneGovernance(src.Governance)
	return &out
}
