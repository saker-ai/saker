package api

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/runtime/commands"
	"github.com/cinience/saker/pkg/runtime/skills"
	"github.com/cinience/saker/pkg/runtime/subagents"
	"github.com/cinience/saker/pkg/sandbox"
)

// runtime_helpers_loader.go owns loader option construction plus the merged
// command/skill/subagent registration pipelines. Pure conversion helpers
// live in runtime_helpers_convert.go and per-session state in
// runtime_helpers_session.go.

func registerSkills(registrations []SkillRegistration) (*skills.Registry, error) {
	reg := skills.NewRegistry()
	for _, entry := range registrations {
		if entry.Handler == nil {
			return nil, errors.New("api: skill handler is nil")
		}
		if err := reg.Register(entry.Definition, entry.Handler); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

func registerCommands(registrations []CommandRegistration) (*commands.Executor, error) {
	exec := commands.NewExecutor()
	for _, entry := range registrations {
		if entry.Handler == nil {
			return nil, errors.New("api: command handler is nil")
		}
		if err := exec.Register(entry.Definition, entry.Handler); err != nil {
			return nil, err
		}
	}
	return exec, nil
}

func registerSubagents(registrations []SubagentRegistration) (*subagents.Manager, error) {
	if len(registrations) == 0 {
		return nil, nil
	}
	mgr := subagents.NewManager()
	for _, entry := range registrations {
		if entry.Handler == nil {
			return nil, errors.New("api: subagent handler is nil")
		}
		if err := mgr.Register(entry.Definition, entry.Handler); err != nil {
			return nil, err
		}
	}
	return mgr, nil
}

type loaderOptions struct {
	ProjectRoot    string
	ConfigRoot     string
	SkillsDirs     []string
	SkillsRec      *bool
	DisabledSkills []string
	fs             *config.FS
}

func buildLoaderOptions(opts Options) loaderOptions {
	var skillsRec *bool
	if opts.SkillsRecursive != nil {
		v := *opts.SkillsRecursive
		skillsRec = &v
	}
	return loaderOptions{
		ProjectRoot:    opts.ProjectRoot,
		ConfigRoot:     opts.ConfigRoot,
		SkillsDirs:     append([]string(nil), opts.SkillsDirs...),
		SkillsRec:      skillsRec,
		DisabledSkills: append([]string(nil), opts.DisabledSkills...),
		fs:             opts.fsLayer,
	}
}

func buildCommandsExecutor(opts Options) (*commands.Executor, []error) {
	loader := buildLoaderOptions(opts)
	fsRegs, errs := commands.LoadFromFS(commands.LoaderOptions{
		ProjectRoot: loader.ProjectRoot,
		ConfigRoot:  loader.ConfigRoot,
		FS:          loader.fs,
	})

	merged := mergeCommandRegistrations(fsRegs, opts.Commands, &errs)

	exec := commands.NewExecutor()
	for _, reg := range merged {
		if err := exec.Register(reg.Definition, reg.Handler); err != nil {
			errs = append(errs, err)
		}
	}
	return exec, errs
}

func mergeCommandRegistrations(fsRegs []commands.CommandRegistration, manual []CommandRegistration, errs *[]error) []commands.CommandRegistration {
	merged := make([]commands.CommandRegistration, 0, len(fsRegs)+len(manual))
	index := map[string]int{}

	add := func(def commands.Definition, handler commands.Handler, source string) {
		key := strings.ToLower(strings.TrimSpace(def.Name))
		if key == "" {
			*errs = append(*errs, fmt.Errorf("api: command name is empty (%s)", source))
			return
		}
		if handler == nil {
			*errs = append(*errs, fmt.Errorf("api: command %s handler is nil", key))
			return
		}
		reg := commands.CommandRegistration{Definition: def, Handler: handler}
		if idx, ok := index[key]; ok {
			merged[idx] = reg // manual overrides loader
			return
		}
		index[key] = len(merged)
		merged = append(merged, reg)
	}

	for _, reg := range fsRegs {
		add(reg.Definition, reg.Handler, "loader")
	}
	for _, reg := range manual {
		add(reg.Definition, reg.Handler, "manual")
	}
	return merged
}

func buildSkillsRegistry(opts Options) (*skills.Registry, []error) {
	merged, errs := loadSkillRegistrations(opts)

	reg := skills.NewRegistry()
	for _, entry := range merged {
		if err := reg.Register(entry.Definition, entry.Handler); err != nil {
			errs = append(errs, err)
		}
	}
	return reg, errs
}

func loadSkillRegistrations(opts Options) ([]skills.SkillRegistration, []error) {
	loader := buildLoaderOptions(opts)
	outcome := skills.LoadOutcomeFromFS(skills.LoaderOptions{
		ProjectRoot:    loader.ProjectRoot,
		ConfigRoot:     loader.ConfigRoot,
		Directories:    loader.SkillsDirs,
		Recursive:      loader.SkillsRec,
		FS:             loader.fs,
		DisabledSkills: loader.DisabledSkills,
	})
	var (
		fsRegs []skills.SkillRegistration
		errs   []error
	)
	if outcome != nil {
		fsRegs = outcome.Registrations
		errs = outcome.Errors
	}
	return mergeSkillRegistrations(fsRegs, opts.Skills, &errs), errs
}

func mergeSkillRegistrations(fsRegs []skills.SkillRegistration, manual []SkillRegistration, errs *[]error) []skills.SkillRegistration {
	merged := make([]skills.SkillRegistration, 0, len(fsRegs)+len(manual))
	index := map[string]int{}

	add := func(def skills.Definition, handler skills.Handler, source string) {
		key := strings.ToLower(strings.TrimSpace(def.Name))
		if key == "" {
			*errs = append(*errs, fmt.Errorf("api: skill name is empty (%s)", source))
			return
		}
		if handler == nil {
			*errs = append(*errs, fmt.Errorf("api: skill %s handler is nil", key))
			return
		}
		reg := skills.SkillRegistration{Definition: def, Handler: handler}
		if idx, ok := index[key]; ok {
			merged[idx] = reg
			return
		}
		index[key] = len(merged)
		merged = append(merged, reg)
	}

	for _, reg := range fsRegs {
		add(reg.Definition, reg.Handler, "loader")
	}
	for _, reg := range manual {
		add(reg.Definition, reg.Handler, "manual")
	}
	return merged
}

func buildSubagentsManager(opts Options) (*subagents.Manager, []error) {
	loader := buildLoaderOptions(opts)
	projectRegs, errs := subagents.LoadFromFS(subagents.LoaderOptions{
		ProjectRoot: loader.ProjectRoot,
		ConfigRoot:  loader.ConfigRoot,
		FS:          loader.fs,
	})

	merged := mergeSubagentRegistrations(opts.Subagents, projectRegs, &errs)
	if len(merged) == 0 {
		return nil, errs
	}

	mgr := subagents.NewManager()
	for _, reg := range merged {
		if err := mgr.Register(reg.Definition, reg.Handler); err != nil {
			errs = append(errs, err)
		}
	}
	return mgr, errs
}

func mergeSubagentRegistrations(manual []SubagentRegistration, project []subagents.SubagentRegistration, errs *[]error) []subagents.SubagentRegistration {
	merged := make([]subagents.SubagentRegistration, 0, len(manual)+len(project))
	index := map[string]int{}

	add := func(def subagents.Definition, handler subagents.Handler, source string) {
		key := strings.ToLower(strings.TrimSpace(def.Name))
		if key == "" {
			*errs = append(*errs, fmt.Errorf("api: subagent name is empty (%s)", source))
			return
		}
		if handler == nil {
			*errs = append(*errs, fmt.Errorf("api: subagent %s handler is nil", key))
			return
		}
		entry := subagents.SubagentRegistration{Definition: def, Handler: handler}
		if idx, ok := index[key]; ok {
			merged[idx] = entry
			return
		}
		index[key] = len(merged)
		merged = append(merged, entry)
	}

	for _, reg := range manual {
		add(reg.Definition, reg.Handler, "manual")
	}
	for _, reg := range project {
		add(reg.Definition, reg.Handler, "project")
	}
	return merged
}

func definitionSnapshot(exec *commands.Executor, name string) commands.Definition {
	if exec == nil {
		return commands.Definition{Name: strings.ToLower(name)}
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, def := range exec.List() {
		if def.Name == lower {
			return def
		}
	}
	return commands.Definition{Name: lower}
}

func snapshotSandbox(mgr *sandbox.Manager) SandboxReport {
	if mgr == nil {
		return SandboxReport{}
	}
	return SandboxReport{ResourceLimits: mgr.Limits()}
}
