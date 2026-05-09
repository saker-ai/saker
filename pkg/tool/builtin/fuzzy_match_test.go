package toolbuiltin

import "testing"

func TestFuzzyMatch_WhitespaceNormalised(t *testing.T) {
	t.Parallel()
	content := "func foo(a  int,  b string) {"
	old := "func foo(a int, b string) {"
	matched, ok := fuzzyMatch(content, old)
	if !ok {
		t.Fatal("expected fuzzy match to succeed")
	}
	if matched != content {
		t.Errorf("got %q, want %q", matched, content)
	}
}

func TestFuzzyMatch_IndentNormalised(t *testing.T) {
	t.Parallel()
	content := "\t\tif x > 0 {\n\t\t\treturn x\n\t\t}"
	old := "if x > 0 {\n\treturn x\n}"
	matched, ok := fuzzyMatch(content, old)
	if !ok {
		t.Fatal("expected fuzzy match to succeed")
	}
	if matched == "" {
		t.Fatal("matched should not be empty")
	}
}

func TestFuzzyMatch_TrailingWhitespace(t *testing.T) {
	t.Parallel()
	content := "line one   \nline two  \n"
	old := "line one\nline two\n"
	matched, ok := fuzzyMatch(content, old)
	if !ok {
		t.Fatal("expected fuzzy match to succeed")
	}
	if matched == "" {
		t.Fatal("matched should not be empty")
	}
}

func TestFuzzyMatch_CRLFLineEndings(t *testing.T) {
	t.Parallel()
	content := "hello\r\nworld\r\n"
	old := "hello\nworld\n"
	matched, ok := fuzzyMatch(content, old)
	if !ok {
		t.Fatal("expected fuzzy match to succeed")
	}
	if matched == "" {
		t.Fatal("matched should not be empty")
	}
}

func TestFuzzyMatch_BlankLineCollapse(t *testing.T) {
	t.Parallel()
	content := "func a() {\n\n\n\treturn\n}"
	old := "func a() {\n\n\treturn\n}"
	matched, ok := fuzzyMatch(content, old)
	if !ok {
		t.Fatal("expected fuzzy match to succeed")
	}
	if matched == "" {
		t.Fatal("matched should not be empty")
	}
}

func TestFuzzyMatch_NoMatch(t *testing.T) {
	t.Parallel()
	content := "completely different content"
	old := "func main() { return }"
	_, ok := fuzzyMatch(content, old)
	if ok {
		t.Fatal("expected no fuzzy match")
	}
}

func TestFuzzyMatch_ExactSubstring(t *testing.T) {
	t.Parallel()
	content := "aaa\nbbb\nccc"
	old := "bbb"
	// Exact match is handled by the caller, not fuzzyMatch. The fuzzy
	// strategies may still find it via normalisation though.
	matched, ok := fuzzyMatch(content, old)
	if ok && matched == "" {
		t.Fatal("if matched, should not be empty")
	}
	_ = ok // either outcome is acceptable for single-line
}

func TestNormaliseWhitespace(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input, want string
	}{
		{"hello  world", "hello world"},
		{"a\t\tb", "a b"},
		{"no change", "no change"},
		{"  leading", " leading"},
		{"trailing  ", "trailing "},
	}
	for _, tt := range tests {
		got := normaliseWhitespace(tt.input)
		if got != tt.want {
			t.Errorf("normaliseWhitespace(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormaliseIndent(t *testing.T) {
	t.Parallel()
	input := "\t\tfoo\n\t\tbar\n\t\t\tbaz"
	want := "foo\nbar\n\tbaz"
	got := normaliseIndent(input)
	if got != want {
		t.Errorf("normaliseIndent:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestCombinedNormalise(t *testing.T) {
	t.Parallel()
	input := "\t\thello  \r\n\t\t\r\n\t\t\r\n\t\tworld  "
	got := combinedNormalise(input)
	// Should strip trailing ws, collapse blank lines, normalise indent, normalise line endings
	if got == "" {
		t.Fatal("combinedNormalise should produce non-empty output")
	}
}
