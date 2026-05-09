package security

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// LeakAction defines what to do when a secret leak is detected.
type LeakAction int

const (
	// LeakBlock rejects the output entirely.
	LeakBlock LeakAction = iota
	// LeakRedact replaces the secret with [REDACTED].
	LeakRedact
	// LeakWarn logs a warning but allows the output.
	LeakWarn
)

func (a LeakAction) String() string {
	switch a {
	case LeakBlock:
		return "block"
	case LeakRedact:
		return "redact"
	case LeakWarn:
		return "warn"
	default:
		return "unknown"
	}
}

// LeakSeverity indicates how critical a detected leak is.
type LeakSeverity int

const (
	LeakSeverityLow LeakSeverity = iota
	LeakSeverityMedium
	LeakSeverityHigh
	LeakSeverityCritical
)

func (s LeakSeverity) String() string {
	switch s {
	case LeakSeverityLow:
		return "low"
	case LeakSeverityMedium:
		return "medium"
	case LeakSeverityHigh:
		return "high"
	case LeakSeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// LeakPattern defines a single secret detection pattern.
type LeakPattern struct {
	Name     string
	Prefix   string // fast prefix check before regex
	Regex    *regexp.Regexp
	Severity LeakSeverity
	Action   LeakAction
}

// LeakFinding represents a detected secret in content.
type LeakFinding struct {
	PatternName   string
	Severity      LeakSeverity
	Action        LeakAction
	Start         int
	End           int
	MaskedPreview string
}

// LeakScanResult holds the result of scanning content for leaks.
type LeakScanResult struct {
	Findings        []LeakFinding
	ShouldBlock     bool
	RedactedContent string // non-empty only when redaction was applied
}

// IsClean returns true if no leaks were detected.
func (r *LeakScanResult) IsClean() bool {
	return len(r.Findings) == 0
}

// LeakDetector scans content for secret leaks using prefix matching + regex validation.
type LeakDetector struct {
	patterns []LeakPattern
}

var defaultPatternsOnce sync.Once
var defaultPatternsCache []LeakPattern

func getDefaultLeakPatterns() []LeakPattern {
	defaultPatternsOnce.Do(func() {
		defaultPatternsCache = defaultLeakPatterns()
	})
	return defaultPatternsCache
}

// NewLeakDetector creates a detector with default patterns covering 20+ secret types.
func NewLeakDetector() *LeakDetector {
	return &LeakDetector{patterns: getDefaultLeakPatterns()}
}

// NewLeakDetectorWithPatterns creates a detector with custom patterns.
func NewLeakDetectorWithPatterns(patterns []LeakPattern) *LeakDetector {
	return &LeakDetector{patterns: patterns}
}

// Scan checks content for secret leaks and returns all findings.
func (d *LeakDetector) Scan(content string) *LeakScanResult {
	result := &LeakScanResult{}
	var redactions []redactRange
	// Track matched ranges to deduplicate overlapping patterns
	// (e.g., openai sk- pattern should not also match sk-ant- keys).
	var matched []redactRange

	for i := range d.patterns {
		p := &d.patterns[i]

		// Fast prefix check: skip regex if prefix not present.
		if p.Prefix != "" && !strings.Contains(content, p.Prefix) {
			continue
		}

		for _, loc := range p.Regex.FindAllStringIndex(content, -1) {
			// Skip if this range is already covered by a previous pattern.
			if rangeOverlaps(matched, loc[0], loc[1]) {
				continue
			}
			matched = append(matched, redactRange{loc[0], loc[1]})

			secret := content[loc[0]:loc[1]]
			finding := LeakFinding{
				PatternName:   p.Name,
				Severity:      p.Severity,
				Action:        p.Action,
				Start:         loc[0],
				End:           loc[1],
				MaskedPreview: maskSecret(secret),
			}
			result.Findings = append(result.Findings, finding)

			if p.Action == LeakBlock {
				result.ShouldBlock = true
			}
			if p.Action == LeakRedact {
				redactions = append(redactions, redactRange{loc[0], loc[1]})
			}
		}
	}

	if len(redactions) > 0 {
		sort.Slice(redactions, func(i, j int) bool { return redactions[i].start < redactions[j].start })
		redactions = mergeRanges(redactions)
		result.RedactedContent = applyRedactions(content, redactions)
	}
	return result
}

// ScanAndClean scans content and returns the cleaned version.
// Returns an error if the content should be blocked.
func (d *LeakDetector) ScanAndClean(content string) (string, []LeakFinding, error) {
	result := d.Scan(content)
	if result.ShouldBlock {
		for _, f := range result.Findings {
			if f.Action == LeakBlock {
				return "", result.Findings, fmt.Errorf("security: secret leak blocked: pattern %q matched %q", f.PatternName, f.MaskedPreview)
			}
		}
	}
	if result.RedactedContent != "" {
		return result.RedactedContent, result.Findings, nil
	}
	return content, result.Findings, nil
}

// PatternCount returns the number of registered patterns.
func (d *LeakDetector) PatternCount() int {
	return len(d.patterns)
}

// maskSecret shows first 4 and last 4 characters, masks the middle.
func maskSecret(secret string) string {
	runes := []rune(secret)
	n := len(runes)
	if n <= 8 {
		return strings.Repeat("*", n)
	}
	middle := n - 8
	if middle > 8 {
		middle = 8
	}
	return string(runes[:4]) + strings.Repeat("*", middle) + string(runes[n-4:])
}

type redactRange struct{ start, end int }

func rangeOverlaps(ranges []redactRange, start, end int) bool {
	for _, r := range ranges {
		if start < r.end && end > r.start {
			return true
		}
	}
	return false
}

func mergeRanges(ranges []redactRange) []redactRange {
	if len(ranges) <= 1 {
		return ranges
	}
	merged := []redactRange{ranges[0]}
	for _, r := range ranges[1:] {
		last := &merged[len(merged)-1]
		if r.start <= last.end {
			if r.end > last.end {
				last.end = r.end
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

func applyRedactions(content string, ranges []redactRange) string {
	var b strings.Builder
	b.Grow(len(content))
	lastEnd := 0
	for _, r := range ranges {
		if r.start > lastEnd {
			b.WriteString(content[lastEnd:r.start])
		}
		b.WriteString("[REDACTED]")
		lastEnd = r.end
	}
	if lastEnd < len(content) {
		b.WriteString(content[lastEnd:])
	}
	return b.String()
}

func defaultLeakPatterns() []LeakPattern {
	return []LeakPattern{
		// Anthropic patterns MUST come before openai_api_key so the more-specific
		// prefix match fires first. The openai pattern excludes sk-ant- and sk-or-.
		{
			Name:     "anthropic_api_key",
			Prefix:   "sk-ant-api",
			Regex:    regexp.MustCompile(`sk-ant-api[a-zA-Z0-9_-]{90,}`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "anthropic_oauth_token",
			Prefix:   "sk-ant-oat",
			Regex:    regexp.MustCompile(`\bsk-ant-oat\d{2}-[a-zA-Z0-9_-]{50,}`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "openai_api_key",
			Prefix:   "sk-",
			Regex:    regexp.MustCompile(`\bsk-(?:proj-)?[a-zA-Z0-9]{20,}`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "aws_access_key",
			Prefix:   "AKIA",
			Regex:    regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "github_token",
			Prefix:   "gh",
			Regex:    regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{36,}`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "github_fine_grained_pat",
			Prefix:   "github_pat_",
			Regex:    regexp.MustCompile(`github_pat_[a-zA-Z0-9]{22}_[a-zA-Z0-9]{59}`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "stripe_api_key",
			Prefix:   "sk_",
			Regex:    regexp.MustCompile(`sk_(?:live|test)_[a-zA-Z0-9]{24,}`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "openrouter_api_key",
			Prefix:   "sk-or-v1-",
			Regex:    regexp.MustCompile(`\bsk-or-v1-[a-fA-F0-9]{40,}`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "slack_token",
			Prefix:   "xox",
			Regex:    regexp.MustCompile(`xox[baprs]-[0-9a-zA-Z-]{10,}`),
			Severity: LeakSeverityHigh,
			Action:   LeakBlock,
		},
		{
			Name:     "google_api_key",
			Prefix:   "AIza",
			Regex:    regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`),
			Severity: LeakSeverityHigh,
			Action:   LeakBlock,
		},
		{
			Name:     "twilio_api_key",
			Prefix:   "SK",
			Regex:    regexp.MustCompile(`\bSK[a-fA-F0-9]{32}\b`),
			Severity: LeakSeverityHigh,
			Action:   LeakBlock,
		},
		{
			Name:     "sendgrid_api_key",
			Prefix:   "SG.",
			Regex:    regexp.MustCompile(`SG\.[a-zA-Z0-9_-]{22}\.[a-zA-Z0-9_-]{43}`),
			Severity: LeakSeverityHigh,
			Action:   LeakBlock,
		},
		{
			Name:     "nearai_session",
			Prefix:   "sess_",
			Regex:    regexp.MustCompile(`sess_[a-zA-Z0-9]{32,}`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "groq_api_key",
			Prefix:   "gsk_",
			Regex:    regexp.MustCompile(`\bgsk_[A-Za-z0-9]{30,}`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "telegram_bot_token",
			Prefix:   ":AA",
			Regex:    regexp.MustCompile(`\b\d{8,12}:AA[A-Za-z0-9_-]{30,}\b`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "pem_private_key",
			Prefix:   "-----BEGIN",
			Regex:    regexp.MustCompile(`-----BEGIN\s+(?:RSA\s+)?PRIVATE\s+KEY-----`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "ssh_private_key",
			Prefix:   "-----BEGIN",
			Regex:    regexp.MustCompile(`-----BEGIN\s+(?:OPENSSH|EC|DSA)\s+PRIVATE\s+KEY-----`),
			Severity: LeakSeverityCritical,
			Action:   LeakBlock,
		},
		{
			Name:     "bearer_token",
			Prefix:   "Bearer",
			Regex:    regexp.MustCompile(`Bearer\s+[a-zA-Z0-9_-]{20,}`),
			Severity: LeakSeverityHigh,
			Action:   LeakRedact,
		},
		{
			Name:     "auth_header",
			Prefix:   "",
			Regex:    regexp.MustCompile(`(?i)authorization:\s*[a-zA-Z]+\s+[a-zA-Z0-9_-]{20,}`),
			Severity: LeakSeverityHigh,
			Action:   LeakRedact,
		},
		{
			Name:     "high_entropy_hex",
			Prefix:   "",
			Regex:    regexp.MustCompile(`\b[a-fA-F0-9]{64}\b`),
			Severity: LeakSeverityMedium,
			Action:   LeakWarn,
		},
	}
}
