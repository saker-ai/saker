// loader_validate.go: Skill name pattern, metadata required-field validation, and length checks.
package skills

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Skill names must be 1-64 characters, lowercase alphanumeric plus hyphens, and
// cannot start or end with a hyphen.
var skillNameRegexp = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

func isValidSkillName(name string) bool {
	return skillNameRegexp.MatchString(strings.TrimSpace(name))
}

func validateMetadata(meta SkillMetadata) error {
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		return errors.New("name is required")
	}
	if !skillNameRegexp.MatchString(name) {
		return fmt.Errorf("invalid name %q", meta.Name)
	}
	desc := strings.TrimSpace(meta.Description)
	if desc == "" {
		return errors.New("description is required")
	}
	if len(desc) > 1024 {
		return errors.New("description exceeds 1024 characters")
	}
	compat := strings.TrimSpace(meta.Compatibility)
	if len(compat) > 500 {
		return errors.New("compatibility exceeds 500 characters")
	}
	return nil
}
