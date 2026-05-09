package security

import (
	"regexp"
	"strings"
	"sync"
)

// InjectionSeverity indicates the severity of a detected injection attempt.
type InjectionSeverity int

const (
	InjectionSeverityLow InjectionSeverity = iota
	InjectionSeverityMedium
	InjectionSeverityHigh
	InjectionSeverityCritical
)

func (s InjectionSeverity) String() string {
	switch s {
	case InjectionSeverityLow:
		return "low"
	case InjectionSeverityMedium:
		return "medium"
	case InjectionSeverityHigh:
		return "high"
	case InjectionSeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// InjectionFinding represents a detected injection attempt.
type InjectionFinding struct {
	Pattern     string
	Severity    InjectionSeverity
	Start       int
	End         int
	Description string
}

// SanitizeResult holds the result of sanitizing tool output.
type SanitizeResult struct {
	Output      string
	Findings    []InjectionFinding
	WasModified bool
}

// stringPattern is a case-insensitive literal pattern.
type stringPattern struct {
	pattern     string
	lower       string // pre-computed lowercase
	severity    InjectionSeverity
	description string
}

// regexPattern is a regex-based pattern.
type regexPattern struct {
	name        string
	regex       *regexp.Regexp
	severity    InjectionSeverity
	description string
}

// Sanitizer detects and neutralizes prompt injection attempts in tool output.
type Sanitizer struct {
	stringPatterns []stringPattern
	regexPatterns  []regexPattern
}

var (
	defaultStringPatternsOnce  sync.Once
	defaultStringPatternsCache []stringPattern
	defaultRegexPatternsOnce   sync.Once
	defaultRegexPatternsCache  []regexPattern
)

func getDefaultStringPatterns() []stringPattern {
	defaultStringPatternsOnce.Do(func() { defaultStringPatternsCache = defaultStringPatterns() })
	return defaultStringPatternsCache
}

func getDefaultRegexPatterns() []regexPattern {
	defaultRegexPatternsOnce.Do(func() { defaultRegexPatternsCache = defaultRegexPatterns() })
	return defaultRegexPatternsCache
}

// NewSanitizer creates a sanitizer with default injection detection patterns.
func NewSanitizer() *Sanitizer {
	return &Sanitizer{
		stringPatterns: getDefaultStringPatterns(),
		regexPatterns:  getDefaultRegexPatterns(),
	}
}

// Scan detects injection patterns without modifying content.
func (s *Sanitizer) Scan(content string) []InjectionFinding {
	return s.scanContent(content)
}

// SanitizeToolOutput scans tool output for injection attempts and sanitizes if needed.
func (s *Sanitizer) SanitizeToolOutput(toolName, output string) SanitizeResult {
	findings := s.scanContent(output)

	hasCritical := false
	for _, f := range findings {
		if f.Severity == InjectionSeverityCritical {
			hasCritical = true
			break
		}
	}

	sanitized := output
	modified := false
	if hasCritical {
		sanitized = escapeContent(output)
		modified = true
	}

	return SanitizeResult{
		Output:      sanitized,
		Findings:    findings,
		WasModified: modified,
	}
}

// WrapForLLM wraps tool output in <tool_output> XML tags with boundary injection prevention.
func WrapForLLM(toolName, output string) string {
	// Escape both opening and closing tool_output tags to prevent boundary injection.
	escaped := strings.ReplaceAll(output, "</tool_output>", "</tool_output\u200b>")
	escaped = strings.ReplaceAll(escaped, "<tool_output", "<tool_output\u200b")

	// Sanitize toolName for safe XML attribute insertion.
	safeName := sanitizeXMLAttr(toolName)

	var b strings.Builder
	b.Grow(len(escaped) + len(safeName) + 60)
	b.WriteString("<tool_output tool=\"")
	b.WriteString(safeName)
	b.WriteString("\">\n")
	b.WriteString(escaped)
	b.WriteString("\n</tool_output>")
	return b.String()
}

func sanitizeXMLAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func (s *Sanitizer) scanContent(content string) []InjectionFinding {
	var findings []InjectionFinding
	lower := strings.ToLower(content)

	// String pattern matching (case-insensitive).
	for i := range s.stringPatterns {
		p := &s.stringPatterns[i]
		offset := 0
		for {
			idx := strings.Index(lower[offset:], p.lower)
			if idx < 0 {
				break
			}
			start := offset + idx
			end := start + len(p.lower)
			findings = append(findings, InjectionFinding{
				Pattern:     p.pattern,
				Severity:    p.severity,
				Start:       start,
				End:         end,
				Description: p.description,
			})
			offset = end
		}
	}

	// Regex pattern matching.
	for i := range s.regexPatterns {
		p := &s.regexPatterns[i]
		for _, loc := range p.regex.FindAllStringIndex(content, -1) {
			findings = append(findings, InjectionFinding{
				Pattern:     p.name,
				Severity:    p.severity,
				Start:       loc[0],
				End:         loc[1],
				Description: p.description,
			})
		}
	}

	return findings
}

var roleMarkerRe = regexp.MustCompile(`(?im)(^|\s)(system:|user:|assistant:)`)

// escapeContent neutralizes critical injection patterns.
func escapeContent(content string) string {
	escaped := content

	// Escape special tokens.
	escaped = strings.ReplaceAll(escaped, "<|", "\\<|")
	escaped = strings.ReplaceAll(escaped, "|>", "|\\>")
	escaped = strings.ReplaceAll(escaped, "[INST]", "\\[INST]")
	escaped = strings.ReplaceAll(escaped, "[/INST]", "\\[/INST]")

	// Remove null bytes.
	escaped = strings.ReplaceAll(escaped, "\x00", "")

	// Escape role markers anywhere (not just line-start).
	escaped = roleMarkerRe.ReplaceAllString(escaped, "${1}[ESCAPED] ${2}")

	return escaped
}

func defaultStringPatterns() []stringPattern {
	return []stringPattern{
		{"ignore all previous", "ignore all previous", InjectionSeverityCritical, "Attempt to override all previous instructions"},
		{"ignore previous", "ignore previous", InjectionSeverityHigh, "Attempt to override previous instructions"},
		{"forget everything", "forget everything", InjectionSeverityHigh, "Attempt to reset context"},
		{"disregard", "disregard", InjectionSeverityMedium, "Potential instruction override"},
		{"you are now", "you are now", InjectionSeverityHigh, "Attempt to change assistant role"},
		{"act as", "act as", InjectionSeverityMedium, "Potential role manipulation"},
		{"pretend to be", "pretend to be", InjectionSeverityMedium, "Potential role manipulation"},
		{"system:", "system:", InjectionSeverityCritical, "Attempt to inject system message"},
		{"assistant:", "assistant:", InjectionSeverityHigh, "Attempt to inject assistant response"},
		{"user:", "user:", InjectionSeverityHigh, "Attempt to inject user message"},
		{"<|", "<|", InjectionSeverityCritical, "Potential special token injection"},
		{"|>", "|>", InjectionSeverityCritical, "Potential special token injection"},
		{"[INST]", "[inst]", InjectionSeverityCritical, "Potential instruction token injection"},
		{"[/INST]", "[/inst]", InjectionSeverityCritical, "Potential instruction token injection"},
		{"new instructions", "new instructions", InjectionSeverityHigh, "Attempt to provide new instructions"},
		{"updated instructions", "updated instructions", InjectionSeverityHigh, "Attempt to update instructions"},
		{"```system", "```system", InjectionSeverityHigh, "Potential code block instruction injection"},
	}
}

func defaultRegexPatterns() []regexPattern {
	return []regexPattern{
		{
			name:        "base64_payload",
			regex:       regexp.MustCompile(`(?i)base64[:\s]+[A-Za-z0-9+/=]{50,}`),
			severity:    InjectionSeverityMedium,
			description: "Potential encoded payload",
		},
		{
			name:        "eval_call",
			regex:       regexp.MustCompile(`(?i)eval\s*\(`),
			severity:    InjectionSeverityHigh,
			description: "Potential code evaluation attempt",
		},
		{
			name:        "exec_call",
			regex:       regexp.MustCompile(`(?i)exec\s*\(`),
			severity:    InjectionSeverityHigh,
			description: "Potential code execution attempt",
		},
		{
			name:        "null_byte",
			regex:       regexp.MustCompile(`\x00`),
			severity:    InjectionSeverityCritical,
			description: "Null byte injection attempt",
		},
	}
}
