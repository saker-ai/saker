package persona

import (
	"fmt"
	"strings"
)

// PromptSection represents a named prompt block that can be injected into the system prompt.
type PromptSection struct {
	Name      string
	Content   string
	Cacheable bool
}

// BuildPromptSections generates system prompt sections from a resolved persona profile.
func BuildPromptSections(p *Profile, projectRoot string) []PromptSection {
	if p == nil {
		return nil
	}
	var sections []PromptSection

	if id := buildIdentityBlock(p); id != "" {
		sections = append(sections, PromptSection{
			Name:      "persona.identity",
			Content:   id,
			Cacheable: true,
		})
	}

	if soul := p.ResolvedSoul(projectRoot); soul != "" {
		sections = append(sections, PromptSection{
			Name:      "persona.soul",
			Content:   "# Soul\n\n" + soul,
			Cacheable: true,
		})
	}

	if instr := p.ResolvedInstructions(projectRoot); instr != "" {
		sections = append(sections, PromptSection{
			Name:      "persona.instructions",
			Content:   "# Instructions\n\n" + instr,
			Cacheable: true,
		})
	}

	return sections
}

func buildIdentityBlock(p *Profile) string {
	if p.Name == "" && p.Description == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("# Identity\n\n")

	if p.Emoji != "" {
		sb.WriteString(fmt.Sprintf("You are %s %s", p.Emoji, p.Name))
	} else if p.Name != "" {
		sb.WriteString(fmt.Sprintf("You are %s", p.Name))
	}

	if p.Description != "" {
		if p.Name != "" {
			sb.WriteString(fmt.Sprintf(" — %s", p.Description))
		} else {
			sb.WriteString(p.Description)
		}
	}
	sb.WriteString(".")

	if p.Vibe != "" {
		sb.WriteString(fmt.Sprintf("\nVibe: %s", p.Vibe))
	}

	return sb.String()
}
