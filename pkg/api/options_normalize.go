package api

import (
	"maps"
	"path/filepath"
	"strings"

	"github.com/saker-ai/saker/pkg/agent"
	coremw "github.com/saker-ai/saker/pkg/core/eventmw"
	corehooks "github.com/saker-ai/saker/pkg/core/hooks"
	"github.com/saker-ai/saker/pkg/middleware"
	"github.com/saker-ai/saker/pkg/runtime/skills"
	"github.com/saker-ai/saker/pkg/tool"
)

const (
	defaultEntrypoint  = EntryPointCLI
	defaultMaxSessions = 1000
)

// withDefaults normalises entrypoint/mode, resolves project and settings paths,
// and leaves tool selection untouched: Tools stays as provided (legacy override),
// EnabledBuiltinTools/CustomTools keep their caller-supplied values for later
// registration logic (nil means register all built-ins, empty slice means none).
func (o Options) withDefaults() Options {
	if o.EntryPoint == "" {
		o.EntryPoint = defaultEntrypoint
	}
	if o.Mode.EntryPoint == "" {
		o.Mode.EntryPoint = o.EntryPoint
	}

	// 智能解析项目根目录
	if o.ProjectRoot == "" || o.ProjectRoot == "." {
		if resolved, err := ResolveProjectRoot(); err == nil {
			o.ProjectRoot = resolved
		} else {
			o.ProjectRoot = "."
		}
	}
	o.ProjectRoot = filepath.Clean(o.ProjectRoot)
	if strings.TrimSpace(o.ConfigRoot) == "" {
		o.ConfigRoot = filepath.Join(o.ProjectRoot, ".saker")
	} else if !filepath.IsAbs(o.ConfigRoot) {
		o.ConfigRoot = filepath.Join(o.ProjectRoot, o.ConfigRoot)
	}
	o.ConfigRoot = filepath.Clean(o.ConfigRoot)
	if trimmed := strings.TrimSpace(o.SettingsPath); trimmed != "" {
		if abs, err := filepath.Abs(trimmed); err == nil {
			o.SettingsPath = abs
		} else {
			o.SettingsPath = trimmed
		}
	}

	if o.Sandbox.Root == "" {
		o.Sandbox.Root = o.ProjectRoot
	}
	o.Sandbox = normalizeSandboxOptions(o.ProjectRoot, o.Sandbox)

	// 根据 EntryPoint 自动设置网络白名单默认值
	if len(o.Sandbox.NetworkAllow) == 0 {
		o.Sandbox.NetworkAllow = defaultNetworkAllowList()
	}

	if o.MaxSessions <= 0 {
		o.MaxSessions = defaultMaxSessions
	}

	// Layered MaxIterations defaults — picked per surface so a long-running
	// platform service isn't constrained by a CLI one-shot's safety cap. Set
	// MaxIterations explicitly (including -1 for unlimited) to override.
	if o.MaxIterations == 0 {
		switch o.EntryPoint {
		case EntryPointPlatform:
			// Long-running services: callers bound the run via context, budget,
			// or token caps. Iteration counting alone is the wrong signal here.
			o.MaxIterations = -1
		case EntryPointCI:
			// Headless one-shot CI runs: 50 mirrors the subagent default and
			// gives builds enough headroom for multi-step refactors.
			o.MaxIterations = 50
		default: // EntryPointCLI / unset — preserve historical behavior.
			o.MaxIterations = agent.DefaultMaxIterations
		}
	}
	return o
}

// frozen returns a defensive copy of Options so callers can safely reuse/mutate
// the original Options struct without racing against a live Runtime.
func (o Options) frozen() Options {
	o.Mode = freezeMode(o.Mode)
	o.OutputSchema = cloneResponseFormat(o.OutputSchema)
	o.OutputSchemaMode = normalizeOutputSchemaMode(o.OutputSchemaMode)

	if len(o.Middleware) > 0 {
		o.Middleware = append([]middleware.Middleware(nil), o.Middleware...)
	}
	if len(o.Tools) > 0 {
		o.Tools = append([]tool.Tool(nil), o.Tools...)
	}
	if len(o.EnabledBuiltinTools) > 0 {
		o.EnabledBuiltinTools = append([]string(nil), o.EnabledBuiltinTools...)
	}
	if len(o.DisallowedTools) > 0 {
		o.DisallowedTools = append([]string(nil), o.DisallowedTools...)
	}
	if len(o.DisabledSkills) > 0 {
		o.DisabledSkills = append([]string(nil), o.DisabledSkills...)
	}
	if len(o.CustomTools) > 0 {
		o.CustomTools = append([]tool.Tool(nil), o.CustomTools...)
	}
	if len(o.MCPServers) > 0 {
		o.MCPServers = append([]string(nil), o.MCPServers...)
	}
	if len(o.TypedHooks) > 0 {
		hooks := make([]corehooks.ShellHook, len(o.TypedHooks))
		for i, hook := range o.TypedHooks {
			hooks[i] = hook
			if len(hook.Env) > 0 {
				hooks[i].Env = maps.Clone(hook.Env)
			}
		}
		o.TypedHooks = hooks
	}
	if len(o.HookMiddleware) > 0 {
		o.HookMiddleware = append([]coremw.Middleware(nil), o.HookMiddleware...)
	}
	if len(o.Skills) > 0 {
		skillsCopy := make([]SkillRegistration, len(o.Skills))
		for i, reg := range o.Skills {
			skillsCopy[i] = reg
			def := reg.Definition
			if len(def.Metadata) > 0 {
				def.Metadata = maps.Clone(def.Metadata)
			}
			if len(def.Matchers) > 0 {
				def.Matchers = append([]skills.Matcher(nil), def.Matchers...)
			}
			skillsCopy[i].Definition = def
		}
		o.Skills = skillsCopy
	}
	if len(o.Commands) > 0 {
		o.Commands = append([]CommandRegistration(nil), o.Commands...)
	}
	if len(o.Subagents) > 0 {
		subCopy := make([]SubagentRegistration, len(o.Subagents))
		for i, reg := range o.Subagents {
			subCopy[i] = reg
			def := reg.Definition
			def.BaseContext = def.BaseContext.Clone()
			if len(def.Matchers) > 0 {
				def.Matchers = append([]skills.Matcher(nil), def.Matchers...)
			}
			subCopy[i].Definition = def
		}
		o.Subagents = subCopy
	}
	if len(o.SkillsDirs) > 0 {
		o.SkillsDirs = append([]string(nil), o.SkillsDirs...)
	}
	if o.SkillsRecursive != nil {
		v := *o.SkillsRecursive
		o.SkillsRecursive = &v
	}

	o.Sandbox = freezeSandboxOptions(o.Sandbox)

	if len(o.ModelPool) > 0 {
		o.ModelPool = maps.Clone(o.ModelPool)
	}
	if len(o.SubagentModelMapping) > 0 {
		o.SubagentModelMapping = maps.Clone(o.SubagentModelMapping)
	}

	return o
}
