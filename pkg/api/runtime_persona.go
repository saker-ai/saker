package api

import (
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/saker-ai/saker/pkg/config"
	"github.com/saker-ai/saker/pkg/persona"
)

// initPersonas builds a persona registry and router from file-based personas
// and settings configuration. Returns (nil, nil) when no personas are defined.
func initPersonas(opts Options, settings *config.Settings) (*persona.Registry, *persona.Router) {
	registry := persona.NewRegistry()
	var bindings []persona.ChannelBinding
	fallback := opts.DefaultPersona

	// Load file-based personas from PersonasDir.
	personasDir := strings.TrimSpace(opts.PersonasDir)
	if personasDir == "" {
		personasDir = filepath.Join(opts.ConfigRoot, "personas")
	} else if !filepath.IsAbs(personasDir) {
		personasDir = filepath.Join(opts.ProjectRoot, personasDir)
	}

	profiles, err := persona.LoadFromDir(personasDir)
	if err != nil {
		slog.Warn("persona loader warning", "error", err)
	}
	for _, p := range profiles {
		if regErr := registry.Register(p); regErr != nil {
			slog.Warn("persona register warning", "error", regErr)
		}
		bindings = append(bindings, p.Channels...)
	}

	// Load from settings.json personas config.
	if settings != nil && settings.Personas != nil {
		if settings.Personas.Default != "" && fallback == "" {
			fallback = settings.Personas.Default
		}
		for id, sp := range settings.Personas.Profiles {
			p := configProfileToPersona(id, sp)
			if regErr := registry.Register(p); regErr != nil {
				slog.Warn("persona register warning", "error", regErr)
			}
		}
		for _, route := range settings.Personas.Routes {
			bindings = append(bindings, persona.ChannelBinding{
				Channel:   route.Channel,
				Peer:      route.Peer,
				PersonaID: route.Persona,
				Priority:  route.Priority,
			})
		}
	}

	if registry.Len() == 0 {
		return nil, nil
	}

	router := persona.NewRouter(bindings, fallback)
	return registry, router
}

// configProfileToPersona converts a settings PersonaProfile to persona.Profile.
func configProfileToPersona(id string, sp config.PersonaProfile) persona.Profile {
	return persona.Profile{
		ID:              id,
		Name:            sp.Name,
		Description:     sp.Description,
		Emoji:           sp.Emoji,
		Soul:            sp.Soul,
		SoulFile:        sp.SoulFile,
		Instructions:    sp.Instructions,
		InstructFile:    sp.InstructFile,
		Model:           sp.Model,
		Language:        sp.Language,
		EnabledTools:    sp.EnabledTools,
		DisallowedTools: sp.DisallowedTools,
		Inherit:         sp.Inherit,
	}
}

// resolveRequestPersona determines which persona to use for a request.
// Priority: explicit tag > channel routing > default fallback.
func resolveRequestPersona(req Request, registry *persona.Registry, router *persona.Router) *persona.Profile {
	if registry == nil {
		return nil
	}

	// 1. Explicit persona tag in request.
	if id, ok := req.Tags["persona"]; ok && id != "" {
		if p, found := registry.Get(id); found {
			return p
		}
	}

	// 2. Channel-based routing.
	if router != nil && len(req.Channels) > 0 {
		id := router.Resolve(persona.RouteContext{
			Channels: req.Channels,
			User:     req.User,
		})
		if id != "" {
			if p, found := registry.Get(id); found {
				return p
			}
		}
	}

	// 3. Default fallback from router.
	if router != nil {
		if fb := router.Fallback(); fb != "" {
			if p, found := registry.Get(fb); found {
				return p
			}
		}
	}

	return nil
}

// buildPersonaSystemPrompt appends persona prompt sections to the base system prompt.
// When registry is provided, soul/instructions are read from cache instead of disk.
func buildPersonaSystemPrompt(basePrompt string, baseBlocks []string, p *persona.Profile, projectRoot string, registry *persona.Registry) (string, []string) {
	// Use cached soul/instructions when available.
	if registry != nil {
		soul, instr := registry.ResolvedSoulCached(p.ID, projectRoot)
		if soul != "" && p.Soul == "" {
			p.Soul = soul
		}
		if instr != "" && p.Instructions == "" {
			p.Instructions = instr
		}
	}
	sections := persona.BuildPromptSections(p, projectRoot)
	if len(sections) == 0 {
		return basePrompt, baseBlocks
	}

	var personaText strings.Builder
	for _, s := range sections {
		if personaText.Len() > 0 {
			personaText.WriteString("\n\n")
		}
		personaText.WriteString(s.Content)
	}

	prompt := basePrompt + "\n\n" + personaText.String()

	blocks := make([]string, len(baseBlocks))
	copy(blocks, baseBlocks)
	if len(blocks) > 0 {
		blocks[len(blocks)-1] += "\n\n" + personaText.String()
	}

	return prompt, blocks
}
