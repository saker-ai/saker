package toolbuiltin

import (
	"testing"

	"github.com/saker-ai/saker/pkg/media/describe"
)

func TestDetectConsistencyIssues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		annotations []*describe.Annotation
		wantEmpty   bool
		wantContain string
	}{
		{
			name:      "nil annotations",
			wantEmpty: true,
		},
		{
			name: "single year no conflict",
			annotations: []*describe.Annotation{
				{Text: "Event in 2024"},
				{Text: "Also 2024"},
			},
			wantEmpty: true,
		},
		{
			name: "multiple years trigger warning",
			annotations: []*describe.Annotation{
				{Text: "Year 2016 shown"},
				{Text: "Year 2018 displayed"},
			},
			wantContain: "Year inconsistency",
		},
		{
			name: "empty text fields ignored",
			annotations: []*describe.Annotation{
				{Text: ""},
				nil,
				{Text: "2020 event"},
			},
			wantEmpty: true,
		},
		{
			name: "same year in multiple segments no conflict",
			annotations: []*describe.Annotation{
				{Text: "Score: 2024"},
				{Text: "Round 2024"},
				{Text: "Final 2024"},
			},
			wantEmpty: true,
		},
		{
			name: "three different years",
			annotations: []*describe.Annotation{
				{Text: "2016 championship"},
				{Text: "2018 event"},
				{Text: "2020 broadcast"},
			},
			wantContain: "Year inconsistency",
		},
		{
			name: "no year in text",
			annotations: []*describe.Annotation{
				{Text: "fencing match between two players"},
				{Text: "score display on screen"},
			},
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := detectConsistencyIssues(tt.annotations)
			if tt.wantEmpty && result != "" {
				t.Errorf("expected empty, got %q", result)
			}
			if !tt.wantEmpty && result == "" {
				t.Error("expected non-empty result")
			}
			if tt.wantContain != "" && !contains(result, tt.wantContain) {
				t.Errorf("expected result to contain %q, got %q", tt.wantContain, result)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
