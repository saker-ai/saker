package server

import "testing"

func TestCleanTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain text", "Project Setup Help", "Project Setup Help"},
		{"double quotes", `"Project Setup Help"`, "Project Setup Help"},
		{"single quotes", "'Project Setup Help'", "Project Setup Help"},
		{"Title: prefix", "Title: Project Setup Help", "Project Setup Help"},
		{"bold markers", "**Project Setup Help**", "Project Setup Help"},
		{"heading prefix", "## Project Setup Help", "Project Setup Help"},
		{"multiline", "Project Setup Help\nSome extra text", "Project Setup Help"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"long title truncated", string(make([]byte, 100)), string(make([]byte, titleMaxOutputLen))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := cleanTitle(tt.input)
			if got != tt.expected {
				t.Errorf("cleanTitle(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
