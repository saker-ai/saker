package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillName(t *testing.T) {
	cases := []struct {
		prompt string
		want   string
	}{
		{"Fix the login bug", "fix-the-login-bug"},
		{"  DEPLOY TO PRODUCTION  ", "deploy-to-production"},
		{"修复登录问题", "修复登录问题"},
		{"", "learned-skill"},
		{"a-b_c d", "a-b-c-d"},
	}
	for _, tc := range cases {
		t.Run(tc.prompt, func(t *testing.T) {
			got := skillName(tc.prompt)
			if got != tc.want {
				t.Errorf("skillName(%q) = %q, want %q", tc.prompt, got, tc.want)
			}
		})
	}
}

func TestExtractToolNames(t *testing.T) {
	calls := []ToolCallSummary{
		{Name: "bash"},
		{Name: "file_read"},
		{Name: "bash"}, // dup
		{Name: "grep"},
		{Name: ""},
	}
	got := extractToolNames(calls)
	if len(got) != 3 || got[0] != "bash" || got[1] != "file_read" || got[2] != "grep" {
		t.Fatalf("extractToolNames = %v", got)
	}
}

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("short", 10); got != "short" {
		t.Errorf("truncateStr short = %q", got)
	}
	if got := truncateStr("a longer string here", 12); got != "a longer ..." {
		t.Errorf("truncateStr long = %q", got)
	}
}

func TestShouldLearn(t *testing.T) {
	l := NewLearner(t.TempDir(), NewRegistry())

	cases := []struct {
		name  string
		input LearningInput
		want  bool
	}{
		{
			name: "success with enough turns and tools",
			input: LearningInput{
				Prompt:    "deploy service",
				Success:   true,
				TurnCount: 5,
				ToolCalls: []ToolCallSummary{{Name: "bash"}, {Name: "file_read"}, {Name: "grep"}},
			},
			want: true,
		},
		{
			name: "failure",
			input: LearningInput{
				Prompt:    "deploy service",
				Success:   false,
				TurnCount: 5,
				ToolCalls: []ToolCallSummary{{Name: "bash"}, {Name: "file_read"}},
			},
			want: false,
		},
		{
			name: "too few turns",
			input: LearningInput{
				Prompt:    "simple task",
				Success:   true,
				TurnCount: 1,
				ToolCalls: []ToolCallSummary{{Name: "bash"}, {Name: "file_read"}},
			},
			want: false,
		},
		{
			name: "too few tools",
			input: LearningInput{
				Prompt:    "single tool task",
				Success:   true,
				TurnCount: 5,
				ToolCalls: []ToolCallSummary{{Name: "bash"}},
			},
			want: false,
		},
		{
			name: "empty prompt",
			input: LearningInput{
				Prompt:    "",
				Success:   true,
				TurnCount: 5,
				ToolCalls: []ToolCallSummary{{Name: "a"}, {Name: "b"}},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := l.ShouldLearn(tc.input); got != tc.want {
				t.Errorf("ShouldLearn = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLearn_CreatesSkillMD(t *testing.T) {
	dir := t.TempDir()
	l := NewLearner(dir, NewRegistry())

	input := LearningInput{
		Prompt:    "Fix auth middleware",
		Output:    "Fixed the auth middleware by adding token validation",
		Success:   true,
		TurnCount: 4,
		ToolCalls: []ToolCallSummary{
			{Name: "bash", Params: "go test ./..."},
			{Name: "file_read", Params: "pkg/auth/middleware.go"},
			{Name: "file_write", Params: "pkg/auth/middleware.go"},
		},
	}

	if err := l.Learn(input); err != nil {
		t.Fatalf("Learn: %v", err)
	}

	name := skillName(input.Prompt)
	skillPath := filepath.Join(dir, name, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}

	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		t.Error("expected YAML frontmatter")
	}
	if !strings.Contains(content, "name: "+name) {
		t.Errorf("expected name in frontmatter, got:\n%s", content)
	}
	if !strings.Contains(content, "bash") {
		t.Error("expected bash in allowed-tools")
	}
	if !strings.Contains(content, "## Steps") {
		t.Error("expected Steps section")
	}
	if !strings.Contains(content, "## Result") {
		t.Error("expected Result section")
	}
}

func TestLearn_SkipsDuplicate(t *testing.T) {
	dir := t.TempDir()
	l := NewLearner(dir, NewRegistry())

	input := LearningInput{
		Prompt:    "unique task",
		Output:    "done",
		Success:   true,
		TurnCount: 3,
		ToolCalls: []ToolCallSummary{{Name: "a"}, {Name: "b"}},
	}

	if err := l.Learn(input); err != nil {
		t.Fatal(err)
	}
	// Second call should be a no-op (file already exists).
	if err := l.Learn(input); err != nil {
		t.Fatal(err)
	}
}

func TestLearn_SkipsUnworthy(t *testing.T) {
	dir := t.TempDir()
	l := NewLearner(dir, NewRegistry())

	input := LearningInput{
		Prompt:    "trivial",
		Success:   true,
		TurnCount: 1,
		ToolCalls: []ToolCallSummary{{Name: "bash"}},
	}

	if err := l.Learn(input); err != nil {
		t.Fatal(err)
	}

	// No file should be created.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected no files for unworthy task, got %d", len(entries))
	}
}

func TestBuildSkillMD(t *testing.T) {
	input := LearningInput{
		Prompt: "test task",
		Output: "all good",
		ToolCalls: []ToolCallSummary{
			{Name: "bash", Params: "echo hello"},
		},
	}
	content := buildSkillMD("test-task", input)
	if !strings.Contains(content, "name: test-task") {
		t.Error("missing name")
	}
	if !strings.Contains(content, "1. `bash`") {
		t.Error("missing step")
	}
}
