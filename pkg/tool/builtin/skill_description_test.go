package toolbuiltin

import (
	"strings"
	"testing"

	"github.com/saker-ai/saker/pkg/runtime/skills"
)

func TestBuildSkillDescriptionEscapesAndDefaults(t *testing.T) {
	empty := buildSkillDescription(nil, 0)
	if !strings.Contains(empty, "</available_skills>") {
		t.Fatalf("expected closing tag for empty skills, got %q", empty)
	}

	defs := []skills.Definition{
		{Name: "", Description: "", UserInvocable: true, Metadata: map[string]string{}},
		{Name: "xml&skill", Description: "use <xml>", UserInvocable: true, Metadata: map[string]string{"location": "path/to.xml"}},
	}
	desc := buildSkillDescription(defs, 0)
	if !strings.Contains(desc, "unknown") {
		t.Fatalf("missing default name fallback: %q", desc)
	}
	if !strings.Contains(desc, "&lt;xml&gt;") || !strings.Contains(desc, "&amp;") {
		t.Fatalf("expected XML escaping, got %q", desc)
	}
}

func TestGetCharBudget(t *testing.T) {
	if got := getCharBudget(0); got != defaultCharBudget {
		t.Fatalf("expected default budget %d, got %d", defaultCharBudget, got)
	}
	// 200k tokens → 200000 * 4 * 0.01 = 8000
	if got := getCharBudget(200_000); got != 8000 {
		t.Fatalf("expected 8000, got %d", got)
	}
	// 30720 tokens (DashScope) → 30720 * 4 * 0.01 = 1228
	if got := getCharBudget(30_720); got != 1228 {
		t.Fatalf("expected 1228, got %d", got)
	}
}

func TestFormatSkillsWithinBudgetTier1Full(t *testing.T) {
	defs := []skills.Definition{
		{Name: "pdf", Description: "Parse PDF files"},
		{Name: "xlsx", Description: "Parse Excel files"},
	}
	// Large budget — should get full descriptions.
	result := formatSkillsWithinBudget(defs, 200_000)
	if !strings.Contains(result, "Parse PDF files") {
		t.Fatalf("expected full description, got %q", result)
	}
	if !strings.Contains(result, "Parse Excel files") {
		t.Fatalf("expected full description, got %q", result)
	}
}

func TestFormatSkillsWithinBudgetTier2Truncated(t *testing.T) {
	// Create skills with long descriptions to exceed a small budget but not
	// trigger names-only mode. We need: budget > nameOverhead + N*minDescLength.
	var defs []skills.Definition
	for i := 0; i < 5; i++ {
		defs = append(defs, skills.Definition{
			Name:        "sk",
			Description: strings.Repeat("A very long description. ", 10), // ~250 chars
		})
	}
	// 5 entries × (56 overhead + 2 name + ~250 desc) ≈ 1540 full chars.
	// Budget at 5000 tokens → 5000*4*0.01 = 200 chars — too small, would be names-only.
	// Budget at 20000 tokens → 20000*4*0.01 = 800 chars — overhead ≈ 5*(56+2)=290, remaining=510, 510/5=102 > 20.
	result := formatSkillsWithinBudget(defs, 20_000)
	// Descriptions should be present but truncated (contain "…").
	if !strings.Contains(result, "…") {
		t.Fatalf("expected truncated descriptions with …, got %q", result)
	}
	// Should still have <description> tags.
	if !strings.Contains(result, "<description>") {
		t.Fatalf("expected description tags in tier 2, got %q", result)
	}
}

func TestFormatSkillsWithinBudgetTier3NamesOnly(t *testing.T) {
	// Create many skills to force names-only mode with an extremely small budget.
	var defs []skills.Definition
	for i := 0; i < 50; i++ {
		defs = append(defs, skills.Definition{
			Name:        strings.Repeat("x", 10),
			Description: strings.Repeat("Long description text. ", 10),
		})
	}
	// Extremely small context window.
	result := formatSkillsWithinBudget(defs, 1_000)
	// Should have name tags but no description tags.
	if !strings.Contains(result, "<name>") {
		t.Fatalf("expected name tags, got %q", result)
	}
	if strings.Contains(result, "<description>") {
		t.Fatalf("expected no description tags in names-only mode, got %q", result)
	}
}

func TestCompactDescription(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"empty", "", 100, ""},
		{"single line", "hello world", 100, "hello world"},
		{"multi line", "line1\nline2\nline3\nline4", 100, "line1 line2 line3"},
		{"skip blanks", "line1\n\nline2", 100, "line1 line2"},
		{"truncate", "a very long description that exceeds the limit", 20, "a very long descrip…"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := compactDescription(tc.input, tc.maxLen)
			if got != tc.want {
				t.Fatalf("compactDescription(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.want)
			}
		})
	}
}

func TestSetContextWindow(t *testing.T) {
	st := NewSkillTool(skills.NewRegistry(), nil)
	st.SetContextWindow(30720)
	if st.contextWindowTokens != 30720 {
		t.Fatalf("expected 30720, got %d", st.contextWindowTokens)
	}
	// nil receiver should not panic.
	var nilTool *SkillTool
	nilTool.SetContextWindow(100)
}
