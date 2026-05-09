package server

import "testing"

func TestIsSilentResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"exact marker", "[SILENT]", true},
		{"with leading space", "  [SILENT]", true},
		{"with trailing newline", "[SILENT]\n", true},
		{"with surrounding whitespace", " \t[SILENT] \n", true},
		{"empty string", "", false},
		{"normal response", "Everything looks good today.", false},
		{"marker embedded in text", "Status: [SILENT] nothing new", false},
		{"marker with extra content", "[SILENT] but also this", false},
		{"lowercase", "[silent]", false},
		{"partial marker", "[SILEN]", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isSilentResponse(tt.input)
			if got != tt.expected {
				t.Errorf("isSilentResponse(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
