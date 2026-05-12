package config

import (
	"errors"
	"strings"
)

// validator.go is the public entrypoint that fans Settings validation across
// the per-block validators in sibling files:
//   - validator_permissions.go: permissions / rules / tool name+pattern
//   - validator_hooks.go: hooks, sandbox, bash/tool output, MCP, port range
//   - validator_misc.go: status line, personas, force-login, storage, aigo,
//     failover, web auth, CORS

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

	// bifrost (semantic cache + telemetry)
	errs = append(errs, validateBifrostConfig(s.Bifrost)...)

	// governance (saker virtual keys)
	errs = append(errs, validateGovernanceConfig(s.Governance)...)

	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}
