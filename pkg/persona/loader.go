package persona

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	personaFileName = "PERSONA.md"
	soulFileName    = "SOUL.md"
)

// LoadFromDir scans a personas directory (e.g. .saker/personas/) and loads
// all persona profiles from subdirectories containing PERSONA.md.
func LoadFromDir(dir string) ([]Profile, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("persona: resolve dir: %w", err)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("persona: read dir: %w", err)
	}

	var profiles []Profile
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		personaPath := filepath.Join(abs, de.Name(), personaFileName)
		if _, err := os.Stat(personaPath); os.IsNotExist(err) {
			continue
		}
		p, err := loadPersonaFile(personaPath, de.Name())
		if err != nil {
			continue // skip unparseable
		}
		// If soul is empty, try standalone SOUL.md
		if p.Soul == "" && p.SoulFile == "" {
			soulPath := filepath.Join(abs, de.Name(), soulFileName)
			if data, err := os.ReadFile(soulPath); err == nil {
				p.Soul = strings.TrimSpace(string(data))
			}
		}
		profiles = append(profiles, p)
	}
	return profiles, nil
}

// loadPersonaFile parses a PERSONA.md file with YAML frontmatter.
// The body after frontmatter is used as the soul text.
func loadPersonaFile(path, defaultID string) (Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}

	content := string(data)
	p := Profile{ID: defaultID}

	if !strings.HasPrefix(content, "---\n") {
		p.Soul = strings.TrimSpace(content)
		return p, nil
	}

	rest := content[4:]
	endIdx := strings.Index(rest, "\n---\n")
	if endIdx < 0 {
		p.Soul = strings.TrimSpace(content)
		return p, nil
	}

	frontmatter := rest[:endIdx]
	body := rest[endIdx+5:]
	p.Soul = strings.TrimSpace(body)

	scanner := bufio.NewScanner(strings.NewReader(frontmatter))
	for scanner.Scan() {
		line := scanner.Text()
		key, val := parseYAMLLine(line)
		if key == "" {
			continue
		}
		switch key {
		case "id":
			p.ID = val
		case "name":
			p.Name = val
		case "description":
			p.Description = val
		case "emoji":
			p.Emoji = strings.Trim(val, "\"'")
		case "avatar":
			p.Avatar = val
		case "creature":
			p.Creature = val
		case "vibe":
			p.Vibe = val
		case "theme":
			p.Theme = val
		case "soulFile", "soulfile":
			p.SoulFile = val
		case "instructFile", "instructfile":
			p.InstructFile = val
		case "model":
			p.Model = val
		case "thinkingLevel", "thinkinglevel":
			p.ThinkingLevel = val
		case "language":
			p.Language = val
		case "inherit":
			p.Inherit = val
		case "enabledTools", "enabledtools":
			p.EnabledTools = parseYAMLList(val)
		case "disallowedTools", "disallowedtools":
			p.DisallowedTools = parseYAMLList(val)
		case "enabledSkills", "enabledskills":
			p.EnabledSkills = parseYAMLList(val)
		case "disabledSkills", "disabledskills":
			p.DisabledSkills = parseYAMLList(val)
		case "mcpServers", "mcpservers":
			p.MCPServers = parseYAMLList(val)
		}
	}
	return p, nil
}

func parseYAMLLine(line string) (key, value string) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", ""
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	return key, value
}

// parseYAMLList handles simple inline YAML arrays: [a, b, c]
func parseYAMLList(val string) []string {
	val = strings.TrimSpace(val)
	if val == "" || val == "[]" {
		return nil
	}
	val = strings.TrimPrefix(val, "[")
	val = strings.TrimSuffix(val, "]")
	parts := strings.Split(val, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "\"'")
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
