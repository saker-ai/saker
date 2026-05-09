package eval

import "testing"

func TestExactMatcher(t *testing.T) {
	t.Parallel()
	m := ExactMatcher{}
	tests := []struct {
		expected, got string
		wantPass      bool
	}{
		{"hello", "hello", true},
		{"hello", "Hello", false},
		{"", "", true},
		{"abc", "abcd", false},
	}
	for _, tt := range tests {
		pass, _ := m.Match(tt.expected, tt.got)
		if pass != tt.wantPass {
			t.Errorf("ExactMatcher(%q, %q) = %v, want %v", tt.expected, tt.got, pass, tt.wantPass)
		}
	}
}

func TestContainsMatcher(t *testing.T) {
	t.Parallel()
	m := ContainsMatcher{}
	tests := []struct {
		expected, got string
		wantPass      bool
		minScore      float64
	}{
		{"hello", "say hello world", true, 1.0},
		{"HELLO", "say hello world", false, 0.5}, // case-insensitive partial credit
		{"xyz", "abc", false, 0.0},
	}
	for _, tt := range tests {
		pass, score := m.Match(tt.expected, tt.got)
		if pass != tt.wantPass {
			t.Errorf("ContainsMatcher(%q, %q) pass = %v, want %v", tt.expected, tt.got, pass, tt.wantPass)
		}
		if score < tt.minScore {
			t.Errorf("ContainsMatcher(%q, %q) score = %f, want >= %f", tt.expected, tt.got, score, tt.minScore)
		}
	}
}

func TestRegexMatcher(t *testing.T) {
	t.Parallel()
	m := RegexMatcher{}
	tests := []struct {
		pattern, got string
		wantPass     bool
	}{
		{`\d+`, "abc123", true},
		{`^hello$`, "hello", true},
		{`^hello$`, "hello world", false},
		{`[invalid`, "test", false}, // invalid regex
	}
	for _, tt := range tests {
		pass, _ := m.Match(tt.pattern, tt.got)
		if pass != tt.wantPass {
			t.Errorf("RegexMatcher(%q, %q) = %v, want %v", tt.pattern, tt.got, pass, tt.wantPass)
		}
	}
}

func TestPrefixMatcher(t *testing.T) {
	t.Parallel()
	m := PrefixMatcher{}
	pass, _ := m.Match("hello", "hello world")
	if !pass {
		t.Error("PrefixMatcher should match prefix")
	}
	pass, _ = m.Match("world", "hello world")
	if pass {
		t.Error("PrefixMatcher should not match non-prefix")
	}
}

func TestNotEmptyMatcher(t *testing.T) {
	t.Parallel()
	m := NotEmptyMatcher{}
	pass, _ := m.Match("", "something")
	if !pass {
		t.Error("NotEmptyMatcher should pass for non-empty")
	}
	pass, _ = m.Match("", "")
	if pass {
		t.Error("NotEmptyMatcher should fail for empty")
	}
}

func TestBoolMatcher(t *testing.T) {
	t.Parallel()
	m := BoolMatcher{}
	pass, _ := m.Match("true", "TRUE")
	if !pass {
		t.Error("BoolMatcher should match case-insensitively")
	}
	pass, _ = m.Match("true", "false")
	if pass {
		t.Error("BoolMatcher should not match different values")
	}
}
