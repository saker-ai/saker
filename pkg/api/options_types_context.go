package api

import "maps"

// EntryPoint identifies the surface the runtime is being driven from.
// Defaults flow off this — see withDefaults() for the surface-specific
// MaxIterations choices.
type EntryPoint string

const (
	EntryPointCLI      EntryPoint = "cli"
	EntryPointCI       EntryPoint = "ci"
	EntryPointPlatform EntryPoint = "platform"
)

// ModelTier represents cost-based model classification for optimization.
type ModelTier string

const (
	ModelTierLow  ModelTier = "low"  // Low cost: Haiku
	ModelTierMid  ModelTier = "mid"  // Mid cost: Sonnet
	ModelTierHigh ModelTier = "high" // High cost: Opus
)

// OutputSchemaMode controls when OutputSchema is applied during an agent run.
type OutputSchemaMode string

const (
	// OutputSchemaModeInline preserves the historical behavior by forwarding
	// ResponseFormat on each agent-loop model call.
	OutputSchemaModeInline OutputSchemaMode = "inline"
	// OutputSchemaModePostProcess runs the loop without ResponseFormat and, if
	// needed, performs one extra formatting pass on the final text output.
	OutputSchemaModePostProcess OutputSchemaMode = "post_process"
)

// CLIContext captures optional metadata supplied by the CLI surface.
type CLIContext struct {
	User      string
	Workspace string
	Args      []string
	Flags     map[string]string
}

// CIContext captures CI/CD metadata for parameter matrix validation.
type CIContext struct {
	Provider string
	Pipeline string
	RunID    string
	SHA      string
	Ref      string
	Matrix   map[string]string
	Metadata map[string]string
}

// PlatformContext captures enterprise platform metadata such as org/project.
type PlatformContext struct {
	Organization string
	Project      string
	Environment  string
	Labels       map[string]string
}

// ModeContext binds an entrypoint to optional contextual metadata blocks.
type ModeContext struct {
	EntryPoint EntryPoint
	CLI        *CLIContext
	CI         *CIContext
	Platform   *PlatformContext
}

func (o Options) modeContext() ModeContext {
	mode := o.Mode
	if mode.EntryPoint == "" {
		mode.EntryPoint = o.EntryPoint
	}
	if mode.EntryPoint == "" {
		mode.EntryPoint = defaultEntrypoint
	}
	return mode
}

func freezeMode(in ModeContext) ModeContext {
	mode := in
	if mode.CLI != nil {
		cli := *mode.CLI
		if len(cli.Args) > 0 {
			cli.Args = append([]string(nil), cli.Args...)
		}
		if len(cli.Flags) > 0 {
			cli.Flags = maps.Clone(cli.Flags)
		}
		mode.CLI = &cli
	}
	if mode.CI != nil {
		ci := *mode.CI
		if len(ci.Matrix) > 0 {
			ci.Matrix = maps.Clone(ci.Matrix)
		}
		if len(ci.Metadata) > 0 {
			ci.Metadata = maps.Clone(ci.Metadata)
		}
		mode.CI = &ci
	}
	if mode.Platform != nil {
		plat := *mode.Platform
		if len(plat.Labels) > 0 {
			plat.Labels = maps.Clone(plat.Labels)
		}
		mode.Platform = &plat
	}
	return mode
}

func normalizeOutputSchemaMode(mode OutputSchemaMode) OutputSchemaMode {
	switch mode {
	case "", OutputSchemaModeInline:
		return OutputSchemaModeInline
	case OutputSchemaModePostProcess:
		return OutputSchemaModePostProcess
	default:
		return OutputSchemaModeInline
	}
}
