package skills

import (
	"context"
	"testing"
)

func TestDefinitionValidateInvalidChar(t *testing.T) {
	def := Definition{Name: "Bad$Name"}
	if err := def.Validate(); err == nil {
		t.Fatalf("expected validation error for invalid characters")
	}
}

func TestNormalizeDefinition(t *testing.T) {
	matcher := MatcherFunc(func(ActivationContext) MatchResult { return MatchResult{Matched: true} })
	meta := map[string]string{"key": "value"}
	def := Definition{
		Name:        "  MIXED  ",
		Description: "desc",
		Priority:    -5,
		MutexKey:    " Key ",
		Metadata:    meta,
		Matchers:    []Matcher{matcher},
	}

	norm := normalizeDefinition(def)
	if norm.Name != "mixed" {
		t.Fatalf("expected lowercase trimmed name, got %q", norm.Name)
	}
	if norm.Priority != 0 {
		t.Fatalf("expected negative priority to be clamped to 0, got %d", norm.Priority)
	}
	if norm.MutexKey != "key" {
		t.Fatalf("expected mutex key trimmed and lowercased, got %q", norm.MutexKey)
	}
	if &norm.Matchers[0] == &def.Matchers[0] {
		t.Fatalf("expected matchers slice to be copied")
	}
	if norm.Metadata["key"] != "value" {
		t.Fatalf("expected metadata copy, got %v", norm.Metadata)
	}
}

func TestSkillHandlerAccessor(t *testing.T) {
	r := NewRegistry()
	handler := HandlerFunc(func(context.Context, ActivationContext) (Result, error) { return Result{Output: "ok"}, nil })
	if err := r.Register(Definition{Name: "demo"}, handler); err != nil {
		t.Fatalf("register: %v", err)
	}
	skill, ok := r.Get("demo")
	if !ok {
		t.Fatalf("expected skill lookup to succeed")
	}
	got := skill.Handler()
	if got == nil {
		t.Fatalf("handler accessor returned nil")
	}
	if res, err := got.Execute(context.Background(), ActivationContext{Prompt: "ok"}); err != nil || res.Output != "ok" {
		t.Fatalf("unexpected handler result: %v %#v", err, res)
	}

	var nilSkill *Skill
	if nilSkill.Handler() != nil {
		t.Fatalf("nil skill should return nil handler")
	}
}

func TestNameInPrompt(t *testing.T) {
	tests := []struct {
		name   string
		skill  string
		prompt string
		want   bool
	}{
		{"full name", "aliyun-zimage-turbo", "使用aliyun-zimage-turbo生成图片", true},
		{"full name case insensitive", "aliyun-zimage-turbo", "use ALIYUN-ZIMAGE-TURBO please", true},
		{"segment only no match", "aliyun-zimage-turbo", "使用zimage skill生成图片", false},
		{"segment only no match 2", "find-skills", "show me the skills catalog", false},
		{"segment only no match 3", "writing-skills", "improve my writing please", false},
		{"no match", "aliyun-zimage-turbo", "hello world", false},
		{"empty prompt", "aliyun-zimage-turbo", "", false},
		{"empty name", "", "hello", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nameInPrompt(tt.skill, tt.prompt)
			if got != tt.want {
				t.Errorf("nameInPrompt(%q, %q) = %v, want %v", tt.skill, tt.prompt, got, tt.want)
			}
		})
	}
}

func TestEvaluateMentionedSkill(t *testing.T) {
	// Skill with no matchers but full name mentioned in prompt → "mentioned"
	skill := &Skill{definition: Definition{Name: "aliyun-zimage-turbo"}}
	ctx := ActivationContext{Prompt: "使用aliyun-zimage-turbo生成图片"}
	result, ok := evaluate(skill, ctx)
	if !ok || result.Reason != "mentioned" {
		t.Errorf("expected reason=mentioned, got reason=%q ok=%v", result.Reason, ok)
	}

	// Skill with no matchers and NOT mentioned → no match
	ctx2 := ActivationContext{Prompt: "hello world"}
	_, ok2 := evaluate(skill, ctx2)
	if ok2 {
		t.Errorf("expected no match for matcherless skill when name not in prompt, got ok=true")
	}
}

func TestNormalizeDefinitionNewFields(t *testing.T) {
	def := Definition{
		Name:             "test-skill",
		WhenToUse:        " Use for testing ",
		ArgumentHint:     " <prompt> ",
		Arguments:        []string{"prompt", "style"},
		Model:            " claude-sonnet ",
		ExecutionContext: " fork ",
		UserInvocable:    true,
		AllowedTools:     []string{"bash", "read"},
		Paths:            []string{"test/**"},
	}
	norm := normalizeDefinition(def)

	if norm.WhenToUse != "Use for testing" {
		t.Fatalf("WhenToUse not trimmed: %q", norm.WhenToUse)
	}
	if norm.ArgumentHint != "<prompt>" {
		t.Fatalf("ArgumentHint not trimmed: %q", norm.ArgumentHint)
	}
	if norm.Model != "claude-sonnet" {
		t.Fatalf("Model not trimmed: %q", norm.Model)
	}
	if norm.ExecutionContext != "fork" {
		t.Fatalf("ExecutionContext not trimmed: %q", norm.ExecutionContext)
	}
	if !norm.UserInvocable {
		t.Fatalf("UserInvocable should be true")
	}
	if len(norm.Arguments) != 2 || norm.Arguments[0] != "prompt" {
		t.Fatalf("Arguments not copied: %v", norm.Arguments)
	}
	if len(norm.AllowedTools) != 2 || norm.AllowedTools[0] != "bash" {
		t.Fatalf("AllowedTools not copied: %v", norm.AllowedTools)
	}
	if len(norm.Paths) != 1 || norm.Paths[0] != "test/**" {
		t.Fatalf("Paths not copied: %v", norm.Paths)
	}
	// Verify slices are independent copies.
	def.Arguments[0] = "changed"
	if norm.Arguments[0] == "changed" {
		t.Fatalf("Arguments slice not independent")
	}
}

func TestNormalizeDefaultExecutionContext(t *testing.T) {
	def := Definition{Name: "test"}
	norm := normalizeDefinition(def)
	if norm.ExecutionContext != "inline" {
		t.Fatalf("expected default execution context 'inline', got %q", norm.ExecutionContext)
	}
}

func TestConditionalActivationPaths(t *testing.T) {
	r := NewRegistry()
	// Skill with Paths should only match when FilePaths overlap.
	handler := HandlerFunc(func(_ context.Context, _ ActivationContext) (Result, error) {
		return Result{Output: "ok"}, nil
	})
	if err := r.Register(Definition{
		Name:  "path-skill",
		Paths: []string{"*.go"},
	}, handler); err != nil {
		t.Fatalf("register: %v", err)
	}

	// No file paths → should not match.
	matches := r.Match(ActivationContext{Prompt: "hello"})
	if len(matches) != 0 {
		t.Fatalf("expected no matches without file paths, got %d", len(matches))
	}

	// With matching file path → should match.
	matches = r.Match(ActivationContext{Prompt: "hello", FilePaths: []string{"main.go"}})
	if len(matches) != 1 {
		t.Fatalf("expected 1 match with matching file path, got %d", len(matches))
	}

	// With non-matching file path → should not match.
	matches = r.Match(ActivationContext{Prompt: "hello", FilePaths: []string{"readme.md"}})
	if len(matches) != 0 {
		t.Fatalf("expected no matches with non-matching file path, got %d", len(matches))
	}
}

func TestSubstituteArguments(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		args     string
		argNames []string
		want     string
	}{
		{"full args", "Run: $ARGUMENTS", "hello world", nil, "Run: hello world"},
		{"positional", "First: $1, Second: $2", "hello world", nil, "First: hello, Second: world"},
		{"named", "Name: ${name}, Age: ${age}", "Alice 30", []string{"name", "age"}, "Name: Alice, Age: 30"},
		{"quoted args", "Query: $1", `"hello world" test`, nil, "Query: hello world"},
		{"empty args", "Run: $ARGUMENTS", "", nil, "Run: "},
		{"empty content", "", "hello", nil, ""},
		{"no placeholders", "plain text", "hello", nil, "plain text"},
		{"mixed", "$1 does $ARGUMENTS", "alice run fast", []string{"user"}, "alice does alice run fast"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SubstituteArguments(tt.content, tt.args, tt.argNames)
			if got != tt.want {
				t.Errorf("SubstituteArguments(%q, %q, %v) = %q, want %q", tt.content, tt.args, tt.argNames, got, tt.want)
			}
		})
	}
}

func TestMatchesPaths(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		files    []string
		want     bool
	}{
		{"exact match", []string{"*.go"}, []string{"main.go"}, true},
		{"no match", []string{"*.py"}, []string{"main.go"}, false},
		{"base name match", []string{"*.go"}, []string{"/path/to/main.go"}, true},
		{"empty files", []string{"*.go"}, nil, false},
		{"empty patterns", nil, []string{"main.go"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesPaths(tt.patterns, tt.files)
			if got != tt.want {
				t.Errorf("matchesPaths(%v, %v) = %v, want %v", tt.patterns, tt.files, got, tt.want)
			}
		})
	}
}
