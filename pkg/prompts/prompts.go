// Package prompts provides compile-time parsing of .saker directory contents
// from an embed.FS, returning ready-to-use registration structures for the SDK.
//
// Usage:
//
//	//go:embed .saker
//	var claudeFS embed.FS
//
//	builtins := prompts.Parse(claudeFS)
//	runtime, _ := api.New(ctx, api.Options{
//	    Skills:     builtins.Skills,
//	    Commands:   builtins.Commands,
//	    Subagents:  builtins.Subagents,
//	    TypedHooks: builtins.Hooks,
//	})
package prompts

import (
	"io/fs"

	corehooks "github.com/saker-ai/saker/pkg/core/hooks"
	"github.com/saker-ai/saker/pkg/runtime/commands"
	"github.com/saker-ai/saker/pkg/runtime/skills"
	"github.com/saker-ai/saker/pkg/runtime/subagents"
)

// Builtins contains all registration structures parsed from an embed.FS.
type Builtins struct {
	Skills    []SkillRegistration
	Commands  []CommandRegistration
	Subagents []SubagentRegistration
	Hooks     []corehooks.ShellHook
	Errors    []error
}

// SkillRegistration wires a skill definition to its handler.
type SkillRegistration struct {
	Definition skills.Definition
	Handler    skills.Handler
}

// CommandRegistration wires a command definition to its handler.
type CommandRegistration struct {
	Definition commands.Definition
	Handler    commands.Handler
}

// SubagentRegistration wires a subagent definition to its handler.
type SubagentRegistration struct {
	Definition subagents.Definition
	Handler    subagents.Handler
}

// ParseOptions configures the parsing behavior.
type ParseOptions struct {
	// SkillsDir is the path to skills directory (default: ".saker/skills")
	SkillsDir string
	// CommandsDir is the path to commands directory (default: ".saker/commands")
	CommandsDir string
	// SubagentsDir is the path to subagents directory (default: ".saker/agents")
	SubagentsDir string
	// HooksDir is the path to hooks directory (default: ".saker/hooks")
	HooksDir string
	// Validate enables strict validation of parsed content
	Validate bool
}

// defaultOptions returns the default parse options.
func defaultOptions() ParseOptions {
	return ParseOptions{
		SkillsDir:    ".saker/skills",
		CommandsDir:  ".saker/commands",
		SubagentsDir: ".saker/agents",
		HooksDir:     ".saker/hooks",
		Validate:     false,
	}
}

// Parse parses the .saker directory from an embed.FS and returns all
// registration structures ready for use with api.Options.
func Parse(fsys fs.FS) Builtins {
	return ParseWithOptions(fsys, defaultOptions())
}

// ParseWithOptions parses with custom directory paths and options.
func ParseWithOptions(fsys fs.FS, opts ParseOptions) Builtins {
	if opts.SkillsDir == "" {
		opts.SkillsDir = ".saker/skills"
	}
	if opts.CommandsDir == "" {
		opts.CommandsDir = ".saker/commands"
	}
	if opts.SubagentsDir == "" {
		opts.SubagentsDir = ".saker/agents"
	}
	if opts.HooksDir == "" {
		opts.HooksDir = ".saker/hooks"
	}

	var errs []error

	skillRegs, skillErrs := parseSkills(fsys, opts.SkillsDir, opts.Validate)
	errs = append(errs, skillErrs...)

	cmdRegs, cmdErrs := parseCommands(fsys, opts.CommandsDir, opts.Validate)
	errs = append(errs, cmdErrs...)

	subagentRegs, subagentErrs := parseSubagents(fsys, opts.SubagentsDir, opts.Validate)
	errs = append(errs, subagentErrs...)

	hookRegs, hookErrs := parseHooks(fsys, opts.HooksDir)
	errs = append(errs, hookErrs...)

	return Builtins{
		Skills:    skillRegs,
		Commands:  cmdRegs,
		Subagents: subagentRegs,
		Hooks:     hookRegs,
		Errors:    errs,
	}
}
