package env

import (
	"regexp"
	"strings"
)

// nameCleaner matches any character that is not alphanumeric, underscore, or hyphen.
var nameCleaner = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// SanitizeName normalizes a string for use as a container, VM, or session name.
// It trims whitespace, replaces disallowed characters with hyphens,
// truncates to 32 characters, and lowercases the result.
// Returns "session" if the input is empty or produces an empty result.
func SanitizeName(in string) string {
	in = strings.TrimSpace(in)
	if in == "" {
		return "session"
	}
	clean := nameCleaner.ReplaceAllString(in, "-")
	clean = strings.Trim(clean, "-_")
	if clean == "" {
		return "session"
	}
	if len(clean) > 32 {
		clean = clean[:32]
	}
	return strings.ToLower(clean)
}
