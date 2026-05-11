package api

import (
	"context"

	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/runtime/commands"
	"github.com/cinience/saker/pkg/runtime/skills"
	"github.com/cinience/saker/pkg/runtime/subagents"
)

// SkillRegistration wires runtime skill definitions + handlers.
type SkillRegistration struct {
	Definition skills.Definition
	Handler    skills.Handler
}

// CommandRegistration wires slash command definitions + handlers.
type CommandRegistration struct {
	Definition commands.Definition
	Handler    commands.Handler
}

// SubagentRegistration wires runtime subagents into the dispatcher.
type SubagentRegistration struct {
	Definition subagents.Definition
	Handler    subagents.Handler
}

// ModelFactory allows callers to supply arbitrary model implementations.
type ModelFactory interface {
	Model(ctx context.Context) (model.Model, error)
}

// ModelFactoryFunc turns a function into a ModelFactory.
type ModelFactoryFunc func(context.Context) (model.Model, error)

// Model implements ModelFactory.
func (fn ModelFactoryFunc) Model(ctx context.Context) (model.Model, error) {
	if fn == nil {
		return nil, ErrMissingModel
	}
	return fn(ctx)
}

// DefaultSubagentDefinitions exposes the built-in subagent type catalog so
// callers can seed api.Options.Subagents or extend the metadata when wiring
// custom handlers.
func DefaultSubagentDefinitions() []subagents.Definition {
	return subagents.BuiltinDefinitions()
}
