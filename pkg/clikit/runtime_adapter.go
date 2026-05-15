package clikit

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/middleware"
	runtimeskills "github.com/saker-ai/saker/pkg/runtime/skills"
	toolbuiltin "github.com/saker-ai/saker/pkg/tool/builtin"
)

type streamRuntime interface {
	RunStream(context.Context, api.Request) (<-chan api.StreamEvent, error)
}

type responseRuntime interface {
	Run(context.Context, api.Request) (*api.Response, error)
}

type RuntimeAdapterConfig struct {
	ProjectRoot     string
	ConfigRoot      string
	ModelName       string
	SandboxBackend  string
	SkillsDirs      []string
	SkillsRecursive *bool
	TurnRecorder    *api.ModelTurnRecorder
}

type RuntimeAdapter struct {
	runtime         streamRuntime
	projectRoot     string
	configRoot      string
	modelName       string
	sandboxBackend  string
	skillsDirs      []string
	skillsRecursive bool
	turnRecorder    *api.ModelTurnRecorder

	askMu           sync.RWMutex
	askQuestionFunc toolbuiltin.AskQuestionFunc
}

type TurnRecorder = api.ModelTurnRecorder

func NewTurnRecorder() *TurnRecorder {
	return api.NewModelTurnRecorder()
}

func newTurnRecorder() *TurnRecorder {
	return api.NewModelTurnRecorder()
}

func TurnRecorderMiddleware(recorder *TurnRecorder) middleware.Middleware {
	return api.ModelTurnRecorderMiddleware(recorder)
}

func NewRuntimeAdapter(rt streamRuntime, cfg RuntimeAdapterConfig) *RuntimeAdapter {
	recorder := cfg.TurnRecorder
	if recorder == nil {
		recorder = api.NewModelTurnRecorder()
	}
	return &RuntimeAdapter{
		runtime:         rt,
		projectRoot:     strings.TrimSpace(cfg.ProjectRoot),
		configRoot:      strings.TrimSpace(cfg.ConfigRoot),
		modelName:       strings.TrimSpace(cfg.ModelName),
		sandboxBackend:  strings.TrimSpace(cfg.SandboxBackend),
		skillsDirs:      append([]string(nil), cfg.SkillsDirs...),
		skillsRecursive: cfg.SkillsRecursive == nil || *cfg.SkillsRecursive,
		turnRecorder:    recorder,
	}
}

func (a *RuntimeAdapter) ModelName() string {
	if a == nil {
		return ""
	}
	return a.modelName
}

func (a *RuntimeAdapter) SetModel(ctx context.Context, name string) error {
	if a == nil {
		return errors.New("clikit: nil adapter")
	}
	if setter, ok := a.runtime.(interface {
		SetModel(context.Context, string) error
	}); ok {
		if err := setter.SetModel(ctx, name); err != nil {
			return err
		}
		a.modelName = name
		return nil
	}
	return errors.New("clikit: runtime does not support model switching")
}

func (a *RuntimeAdapter) SettingsRoot() string {
	if a == nil {
		return ""
	}
	return a.configRoot
}

func (a *RuntimeAdapter) SkillsRecursive() bool {
	if a == nil {
		return true
	}
	return a.skillsRecursive
}

func (a *RuntimeAdapter) SkillsDirs() []string {
	if a == nil {
		return nil
	}
	return append([]string(nil), a.skillsDirs...)
}

func (a *RuntimeAdapter) RepoRoot() string {
	if a == nil {
		return ""
	}
	return a.projectRoot
}

// SetAskQuestionFunc registers an interactive AskUserQuestion handler. Called
// by the TUI at startup so that agent runs invoked through this adapter can
// prompt the user via the bubbletea event loop. Safe to call concurrently.
// Pass nil to clear.
func (a *RuntimeAdapter) SetAskQuestionFunc(fn toolbuiltin.AskQuestionFunc) {
	if a == nil {
		return
	}
	a.askMu.Lock()
	a.askQuestionFunc = fn
	a.askMu.Unlock()
}

// withAskQuestion injects the registered AskQuestionFunc into ctx if one is
// set. When no handler is registered (legacy REPL, headless), ctx is returned
// unchanged and AskUserQuestion's tool-side guard will report "not available"
// to the LLM rather than silently succeeding.
func (a *RuntimeAdapter) withAskQuestion(ctx context.Context) context.Context {
	if a == nil {
		return ctx
	}
	a.askMu.RLock()
	fn := a.askQuestionFunc
	a.askMu.RUnlock()
	if fn == nil {
		return ctx
	}
	return toolbuiltin.WithAskQuestionFunc(ctx, fn)
}

func (a *RuntimeAdapter) RunStream(ctx context.Context, sessionID, prompt string) (<-chan api.StreamEvent, error) {
	return a.runtime.RunStream(a.withAskQuestion(ctx), api.Request{Prompt: prompt, SessionID: sessionID})
}

func (a *RuntimeAdapter) RunStreamForked(ctx context.Context, parentSessionID, sessionID, prompt string) (<-chan api.StreamEvent, error) {
	return a.runtime.RunStream(a.withAskQuestion(ctx), api.Request{
		Prompt:          prompt,
		SessionID:       sessionID,
		ParentSessionID: parentSessionID,
		Ephemeral:       true, // side sessions are temporary, skip disk persistence
	})
}

func (a *RuntimeAdapter) Run(ctx context.Context, sessionID, prompt string) (*api.Response, error) {
	withResponse, ok := a.runtime.(responseRuntime)
	if !ok {
		return nil, errors.New("clikit: runtime does not support non-streaming runs")
	}
	return withResponse.Run(a.withAskQuestion(ctx), api.Request{Prompt: prompt, SessionID: sessionID})
}

func (a *RuntimeAdapter) ModelTurnCount(sessionID string) int {
	if a == nil {
		return 0
	}
	return a.turnRecorder.Count(sessionID)
}

func (a *RuntimeAdapter) ModelTurnsSince(sessionID string, offset int) []ModelTurnStat {
	if a == nil {
		return nil
	}
	stats := a.turnRecorder.Since(sessionID, offset)
	out := make([]ModelTurnStat, 0, len(stats))
	for _, st := range stats {
		out = append(out, ModelTurnStat{
			Iteration:    st.Iteration,
			InputTokens:  st.InputTokens,
			OutputTokens: st.OutputTokens,
			TotalTokens:  st.TotalTokens,
			StopReason:   st.StopReason,
			Preview:      st.Preview,
			Timestamp:    st.Timestamp,
		})
	}
	return out
}

func (a *RuntimeAdapter) Skills() []SkillMeta {
	if a == nil {
		return nil
	}
	if withSkills, ok := a.runtime.(interface{ AvailableSkills() []api.AvailableSkill }); ok {
		src := withSkills.AvailableSkills()
		out := make([]SkillMeta, 0, len(src))
		for _, skill := range src {
			name := strings.TrimSpace(skill.Name)
			if name == "" {
				continue
			}
			out = append(out, SkillMeta{Name: name})
		}
		return out
	}
	var recursive *bool
	if a.skillsRecursive {
		recursive = boolPtr(true)
	} else {
		recursive = boolPtr(false)
	}
	regs, _ := runtimeskills.LoadFromFS(runtimeskills.LoaderOptions{
		ProjectRoot: a.projectRoot,
		ConfigRoot:  a.configRoot,
		Directories: a.skillsDirs,
		Recursive:   recursive,
	})
	out := make([]SkillMeta, 0, len(regs))
	for _, reg := range regs {
		name := strings.TrimSpace(reg.Definition.Name)
		if name == "" {
			continue
		}
		out = append(out, SkillMeta{Name: name})
	}
	return out
}

func (a *RuntimeAdapter) SandboxBackend() string {
	if a == nil {
		return ""
	}
	return a.sandboxBackend
}
