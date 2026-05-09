package model

import "testing"

func TestClassifyPromptComplexity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		prompt   string
		expected PromptComplexity
	}{
		{"simple greeting", "hello", ComplexityStandard},
		{"simple question", "what is 2+2?", ComplexityStandard},
		{"list files", "list all go files", ComplexityStandard},
		{"refactor keyword", "refactor this function", ComplexityStrong},
		{"architecture keyword", "design the architecture for auth", ComplexityStrong},
		{"debug keyword", "debug this crash", ComplexityStrong},
		{"security keyword", "check for security vulnerabilities", ComplexityStrong},
		{"url present", "check https://example.com/api for issues", ComplexityStrong},
		{"long prompt", string(make([]byte, 600)), ComplexityStrong},
		{"case insensitive", "REFACTOR the module", ComplexityStrong},
		{"plan keyword", "create a plan for the migration", ComplexityStrong},
		{"multi-file keyword", "change across the codebase", ComplexityStrong},
		{"explain why", "explain why this deadlocks", ComplexityStrong},
		{"simple fix", "fix the typo in readme", ComplexityStandard},
		{"read file", "read main.go", ComplexityStandard},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyPromptComplexity(tt.prompt)
			if got != tt.expected {
				t.Errorf("ClassifyPromptComplexity(%q) = %q, want %q", tt.prompt, got, tt.expected)
			}
		})
	}
}

func TestContainsURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected bool
	}{
		{"no url here", false},
		{"visit https://example.com", true},
		{"check http://localhost:8080/health", true},
		{"ftp://not-http.com", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := containsURL(tt.input); got != tt.expected {
				t.Errorf("containsURL(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
