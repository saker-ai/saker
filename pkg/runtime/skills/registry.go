package skills

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrDuplicateSkill indicates an attempt to register the same name twice.
	ErrDuplicateSkill = errors.New("skills: duplicate registration")
	// ErrUnknownSkill is returned by Execute/Get when a skill is missing.
	ErrUnknownSkill = errors.New("skills: unknown skill")
)

// Definition describes a declarative skill registration entry.
type Definition struct {
	Name        string
	Description string
	Priority    int
	MutexKey    string
	// DisableAutoActivation keeps the skill available for manual invocation
	// while excluding it from automatic activation matching.
	DisableAutoActivation bool
	Metadata              map[string]string
	Matchers              []Matcher
	WhenToUse             string
	ArgumentHint          string
	Arguments             []string
	Model                 string
	ExecutionContext      string   // "inline" (default) or "fork"
	UserInvocable         bool     // default true; false hides from user-facing listings
	AllowedTools          []string // tools auto-approved during skill execution
	Paths                 []string // conditional activation glob patterns
	RelatedSkills         []string // related skill names for chaining
	RequiresTools         []string // only activate when these tools are available
	FallbackForTools      []string // hide when these tools ARE available (fallback)
}

// Validate performs cheap sanity checks before accepting a definition.
func (d Definition) Validate() error {
	name := strings.TrimSpace(d.Name)
	if name == "" {
		return errors.New("skills: name is required")
	}
	if !isValidSkillName(name) {
		return fmt.Errorf("skills: invalid name %q (must be 1-64 chars, lowercase alphanumeric + hyphens, cannot start/end with hyphen)", d.Name)
	}
	return nil
}

// Handler executes a skill.
type Handler interface {
	Execute(context.Context, ActivationContext) (Result, error)
}

// HandlerFunc adapts ordinary functions to Handler.
type HandlerFunc func(context.Context, ActivationContext) (Result, error)

// Execute implements Handler.
func (fn HandlerFunc) Execute(ctx context.Context, ac ActivationContext) (Result, error) {
	if fn == nil {
		return Result{}, errors.New("skills: handler func is nil")
	}
	return fn(ctx, ac)
}

// Result captures the output from a skill execution.
type Result struct {
	Skill    string
	Output   any
	Metadata map[string]any
}

// clone ensures internal metadata never leaks shared references.
func (r Result) clone() Result {
	if len(r.Metadata) > 0 {
		r.Metadata = maps.Clone(r.Metadata)
	}
	return r
}

// Skill represents a single registered skill.
type Skill struct {
	definition Definition
	handler    Handler
}

// Definition returns an immutable copy of the skill metadata.
func (s *Skill) Definition() Definition {
	if s == nil {
		return Definition{}
	}
	def := s.definition
	if len(def.Metadata) > 0 {
		def.Metadata = maps.Clone(def.Metadata)
	}
	def.Matchers = append([]Matcher(nil), def.Matchers...)
	return def
}

// Execute runs the skill handler.
func (s *Skill) Execute(ctx context.Context, ac ActivationContext) (Result, error) {
	if s == nil || s.handler == nil {
		return Result{}, errors.New("skills: skill is nil")
	}
	res, err := s.handler.Execute(ctx, ac)
	if err != nil {
		return Result{}, err
	}
	if res.Skill == "" {
		res.Skill = s.definition.Name
	}
	return res.clone(), nil
}

// Handler exposes the underlying skill handler for observability and testing.
func (s *Skill) Handler() Handler {
	if s == nil {
		return nil
	}
	return s.handler
}

// Registry coordinates skill registration and activation.
type Registry struct {
	mu     sync.RWMutex
	skills map[string]*Skill
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{skills: map[string]*Skill{}}
}

// ReplaceAll swaps the current registry contents with the provided registrations.
// Existing references to the registry remain valid.
func (r *Registry) ReplaceAll(entries []SkillRegistration) error {
	replacement := make(map[string]*Skill, len(entries))
	for _, entry := range entries {
		if entry.Handler == nil {
			return errors.New("skills: handler is nil")
		}
		if err := entry.Definition.Validate(); err != nil {
			return err
		}
		def := normalizeDefinition(entry.Definition)
		key := def.Name
		if _, exists := replacement[key]; exists {
			return ErrDuplicateSkill
		}
		replacement[key] = &Skill{definition: def, handler: entry.Handler}
	}

	r.mu.Lock()
	r.skills = replacement
	r.mu.Unlock()
	return nil
}

// Register adds a skill definition + handler pair.
func (r *Registry) Register(def Definition, handler Handler) error {
	if err := def.Validate(); err != nil {
		return err
	}
	if handler == nil {
		return errors.New("skills: handler is nil")
	}
	normalized := normalizeDefinition(def)
	key := normalized.Name

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.skills[key]; exists {
		return ErrDuplicateSkill
	}
	r.skills[key] = &Skill{definition: normalized, handler: handler}
	return nil
}

// Unregister removes a skill by name. Returns true if it was found and removed.
func (r *Registry) Unregister(name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.skills[key]; !exists {
		return false
	}
	delete(r.skills, key)
	return true
}

// Get fetches a skill by name.
func (r *Registry) Get(name string) (*Skill, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	r.mu.RLock()
	defer r.mu.RUnlock()
	skill, ok := r.skills[key]
	return skill, ok
}

// Execute invokes a named skill.
func (r *Registry) Execute(ctx context.Context, name string, ac ActivationContext) (Result, error) {
	skill, ok := r.Get(name)
	if !ok {
		return Result{}, ErrUnknownSkill
	}
	return skill.Execute(ctx, ac)
}

// Activation is a resolved auto-activation candidate.
type Activation struct {
	Skill  *Skill
	Score  float64
	Reason string
}

// Definition returns metadata for the activation.
func (a Activation) Definition() Definition {
	if a.Skill == nil {
		return Definition{}
	}
	return a.Skill.Definition()
}

// Match evaluates all auto-activating skills against the provided context while
// enforcing priority ordering and mutex groups.
func (r *Registry) Match(ctx ActivationContext) []Activation {
	snapshot := r.snapshot()
	var matches []Activation
	for _, skill := range snapshot {
		def := skill.definition
		if def.DisableAutoActivation {
			continue
		}
		// Conditional activation: skills with Paths only activate when matching files are present.
		if len(def.Paths) > 0 && !matchesPaths(def.Paths, ctx.FilePaths) {
			continue
		}
		// Conditional activation: requires_tools — hide unless these tools are available.
		if len(def.RequiresTools) > 0 && !toolsAvailable(def.RequiresTools, ctx.AvailableTools) {
			continue
		}
		// Conditional activation: fallback_for_tools — hide when these tools ARE available.
		if len(def.FallbackForTools) > 0 && toolsAvailable(def.FallbackForTools, ctx.AvailableTools) {
			continue
		}
		result, ok := evaluate(skill, ctx)
		if !ok {
			continue
		}
		matches = append(matches, Activation{Skill: skill, Score: result.Score, Reason: result.Reason})
	}
	if len(matches) == 0 {
		return nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		di := matches[i].Skill.definition
		dj := matches[j].Skill.definition
		if di.Priority != dj.Priority {
			return di.Priority > dj.Priority
		}
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return di.Name < dj.Name
	})

	selected := matches[:0]
	seen := map[string]struct{}{}
	for _, activation := range matches {
		key := activation.Skill.definition.MutexKey
		if key == "" {
			selected = append(selected, activation)
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		selected = append(selected, activation)
	}
	return selected
}

// List returns the registered skill definitions sorted by priority + name.
func (r *Registry) List() []Definition {
	snapshot := r.snapshot()
	defs := make([]Definition, 0, len(snapshot))
	for _, skill := range snapshot {
		defs = append(defs, skill.Definition())
	}
	sort.Slice(defs, func(i, j int) bool {
		if defs[i].Priority != defs[j].Priority {
			return defs[i].Priority > defs[j].Priority
		}
		return defs[i].Name < defs[j].Name
	})
	return defs
}

func (r *Registry) snapshot() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Skill, 0, len(r.skills))
	for _, skill := range r.skills {
		out = append(out, skill)
	}
	return out
}

func evaluate(skill *Skill, ctx ActivationContext) (MatchResult, bool) {
	if len(skill.definition.Matchers) == 0 {
		// If the user's prompt mentions the skill name (or a significant part),
		// mark as "mentioned" so it shows on canvas instead of being filtered.
		if nameInPrompt(skill.definition.Name, ctx.Prompt) {
			return MatchResult{Matched: true, Score: 0.7, Reason: "mentioned"}, true
		}
		// Skills with Paths are already filtered by Match(); if we reach here
		// the paths matched, so activate with a lower score.
		if len(skill.definition.Paths) > 0 {
			return MatchResult{Matched: true, Score: 0.5, Reason: "path"}, true
		}
		return MatchResult{}, false
	}
	var best MatchResult
	matched := false
	for _, matcher := range skill.definition.Matchers {
		if matcher == nil {
			continue
		}
		res := matcher.Match(ctx)
		if !res.Matched {
			continue
		}
		if !matched || res.BetterThan(best) {
			best = res
			matched = true
		}
	}
	return best, matched
}

// nameInPrompt checks if the full skill name appears in the prompt.
// Skills that need partial/keyword matching should declare explicit Matchers.
func nameInPrompt(name, prompt string) bool {
	if name == "" || prompt == "" {
		return false
	}
	return strings.Contains(strings.ToLower(prompt), strings.ToLower(name))
}

// matchesPaths returns true if any of the file paths match any of the glob patterns.
func matchesPaths(patterns, filePaths []string) bool {
	for _, fp := range filePaths {
		for _, pattern := range patterns {
			if matched, _ := filepath.Match(pattern, fp); matched {
				return true
			}
			// Also try matching against the base name for simple patterns.
			if matched, _ := filepath.Match(pattern, filepath.Base(fp)); matched {
				return true
			}
		}
	}
	return false
}

// toolsAvailable returns true if all required tools are present in the available set.
func toolsAvailable(required, available []string) bool {
	if len(required) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(available))
	for _, t := range available {
		set[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	for _, req := range required {
		if _, ok := set[strings.ToLower(strings.TrimSpace(req))]; !ok {
			return false
		}
	}
	return true
}

func normalizeDefinition(def Definition) Definition {
	normalized := Definition{
		Name:                  strings.ToLower(strings.TrimSpace(def.Name)),
		Description:           strings.TrimSpace(def.Description),
		Priority:              def.Priority,
		MutexKey:              strings.ToLower(strings.TrimSpace(def.MutexKey)),
		DisableAutoActivation: def.DisableAutoActivation,
		WhenToUse:             strings.TrimSpace(def.WhenToUse),
		ArgumentHint:          strings.TrimSpace(def.ArgumentHint),
		Model:                 strings.TrimSpace(def.Model),
		ExecutionContext:      strings.TrimSpace(def.ExecutionContext),
		UserInvocable:         def.UserInvocable,
	}
	if normalized.Name == "" {
		normalized.Name = strings.TrimSpace(def.Name)
	}
	if normalized.Priority < 0 {
		normalized.Priority = 0
	}
	if normalized.ExecutionContext == "" {
		normalized.ExecutionContext = "inline"
	}
	if len(def.Metadata) > 0 {
		normalized.Metadata = maps.Clone(def.Metadata)
	}
	if len(def.Matchers) > 0 {
		normalized.Matchers = append([]Matcher(nil), def.Matchers...)
	}
	if len(def.Arguments) > 0 {
		normalized.Arguments = append([]string(nil), def.Arguments...)
	}
	if len(def.AllowedTools) > 0 {
		normalized.AllowedTools = append([]string(nil), def.AllowedTools...)
	}
	if len(def.Paths) > 0 {
		normalized.Paths = append([]string(nil), def.Paths...)
	}
	if len(def.RelatedSkills) > 0 {
		normalized.RelatedSkills = append([]string(nil), def.RelatedSkills...)
	}
	if len(def.RequiresTools) > 0 {
		normalized.RequiresTools = append([]string(nil), def.RequiresTools...)
	}
	if len(def.FallbackForTools) > 0 {
		normalized.FallbackForTools = append([]string(nil), def.FallbackForTools...)
	}
	return normalized
}
