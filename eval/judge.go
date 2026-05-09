package eval

import (
	"regexp"
	"strings"
)

// Matcher determines whether an actual value satisfies the expected value.
type Matcher interface {
	// Match returns whether the match succeeded and a score in [0,1].
	Match(expected, got string) (bool, float64)
}

// ExactMatcher requires exact string equality.
type ExactMatcher struct{}

func (ExactMatcher) Match(expected, got string) (bool, float64) {
	if expected == got {
		return true, 1.0
	}
	return false, 0.0
}

// ContainsMatcher passes if got contains expected as a substring.
type ContainsMatcher struct{}

func (ContainsMatcher) Match(expected, got string) (bool, float64) {
	if strings.Contains(got, expected) {
		return true, 1.0
	}
	// Partial credit for case-insensitive match.
	if strings.Contains(strings.ToLower(got), strings.ToLower(expected)) {
		return false, 0.5
	}
	return false, 0.0
}

// RegexMatcher passes if got matches the expected regex pattern.
type RegexMatcher struct{}

func (RegexMatcher) Match(pattern, got string) (bool, float64) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, 0.0
	}
	if re.MatchString(got) {
		return true, 1.0
	}
	return false, 0.0
}

// PrefixMatcher passes if got starts with expected.
type PrefixMatcher struct{}

func (PrefixMatcher) Match(expected, got string) (bool, float64) {
	if strings.HasPrefix(got, expected) {
		return true, 1.0
	}
	return false, 0.0
}

// NotEmptyMatcher passes if got is non-empty.
type NotEmptyMatcher struct{}

func (NotEmptyMatcher) Match(_, got string) (bool, float64) {
	if got != "" {
		return true, 1.0
	}
	return false, 0.0
}

// BoolMatcher treats "true"/"false" strings.
type BoolMatcher struct{}

func (BoolMatcher) Match(expected, got string) (bool, float64) {
	e := strings.TrimSpace(strings.ToLower(expected))
	g := strings.TrimSpace(strings.ToLower(got))
	if e == g {
		return true, 1.0
	}
	return false, 0.0
}
