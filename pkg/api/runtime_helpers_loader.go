package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/config"
	"github.com/saker-ai/saker/pkg/runtime/commands"
	"github.com/saker-ai/saker/pkg/runtime/skills"
	"github.com/saker-ai/saker/pkg/runtime/subagents"
	"github.com/saker-ai/saker/pkg/sandbox"
	"github.com/saker-ai/saker/pkg/skillhub"
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
	var errs []error

	// Phase 1: remote skills (lowest priority — overridden by FS and manual).
	var remoteRegs []skills.SkillRegistration
	for _, src := range opts.RemoteSkillSources {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		client := newRemoteSkillClientAdapter(src)
		outcome := skills.LoadFromRemote(ctx, client, src)
		cancel()
		if outcome != nil {
			remoteRegs = append(remoteRegs, outcome.Registrations...)
			errs = append(errs, outcome.Errors...)
		}
	}

	// Phase 2: filesystem skills (override remote).
	loader := buildLoaderOptions(opts)
	outcome := skills.LoadOutcomeFromFS(skills.LoaderOptions{
		ProjectRoot:    loader.ProjectRoot,
		ConfigRoot:     loader.ConfigRoot,
		Directories:    loader.SkillsDirs,
		Recursive:      loader.SkillsRec,
		FS:             loader.fs,
		DisabledSkills: loader.DisabledSkills,
	})
	var fsRegs []skills.SkillRegistration
	if outcome != nil {
		fsRegs = outcome.Registrations
		errs = append(errs, outcome.Errors...)
	}

	// Phase 3: merge remote → FS → manual (later overrides earlier).
	combined := mergeSkillRegs(remoteRegs, fsRegs, &errs)
	return mergeSkillRegistrations(combined, opts.Skills, &errs), errs
}

// mergeSkillRegs merges two slices of skills.SkillRegistration. Entries in
// the second slice override entries in the first with the same name.
func mergeSkillRegs(base, override []skills.SkillRegistration, errs *[]error) []skills.SkillRegistration {
	merged := make([]skills.SkillRegistration, 0, len(base)+len(override))
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

	for _, reg := range base {
		add(reg.Definition, reg.Handler, "base")
	}
	for _, reg := range override {
		add(reg.Definition, reg.Handler, "override")
	}
	return merged
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

// --- Remote skill client adapter -------------------------------------------

// remoteSkillClientAdapter bridges skillhub.Client to skills.RemoteSkillClient.
type remoteSkillClientAdapter struct {
	client *skillhub.Client
}

func newRemoteSkillClientAdapter(src skills.RemoteSkillSource) *remoteSkillClientAdapter {
	var opts []skillhub.ClientOption
	if src.Token != "" {
		opts = append(opts, skillhub.WithToken(src.Token))
	}
	return &remoteSkillClientAdapter{
		client: skillhub.New(src.Registry, opts...),
	}
}

func (a *remoteSkillClientAdapter) GetFile(ctx context.Context, slug, version, path string) ([]byte, error) {
	return a.client.GetFile(ctx, slug, version, path)
}

func (a *remoteSkillClientAdapter) ListAllSkills(ctx context.Context, maxPages int) ([]skills.RemoteSkillMeta, error) {
	all, err := a.client.ListAllSkills(ctx, maxPages)
	if err != nil {
		return nil, err
	}
	metas := make([]skills.RemoteSkillMeta, len(all))
	for i, s := range all {
		metas[i] = skillToRemoteMeta(s)
	}
	return metas, nil
}

func (a *remoteSkillClientAdapter) GetSkill(ctx context.Context, slug string) (*skills.RemoteSkillMeta, error) {
	s, err := a.client.Get(ctx, slug)
	if err != nil {
		return nil, err
	}
	m := skillToRemoteMeta(*s)
	return &m, nil
}

func skillToRemoteMeta(s skillhub.Skill) skills.RemoteSkillMeta {
	return skills.RemoteSkillMeta{
		Slug:        s.Slug,
		DisplayName: s.DisplayName,
		Summary:     s.Summary,
		Category:    s.Category,
		Kind:        s.Kind,
		Tags:        s.Tags,
		OwnerHandle: s.OwnerHandle,
	}
}

// Compile-time interface check.
var _ skills.RemoteSkillClient = (*remoteSkillClientAdapter)(nil)
