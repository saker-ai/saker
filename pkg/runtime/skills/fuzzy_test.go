package skills

import (
	"strings"
	"testing"
)

func TestFuzzyPatchExactMatch(t *testing.T) {
	t.Parallel()
	content := "hello world\nfoo bar\nbaz"
	result := FuzzyPatch(content, "foo bar", "replaced", false)
	if !result.Applied {
		t.Fatalf("expected applied, got error: %v", result.Error)
	}
	if !strings.Contains(result.Preview, "replaced") {
		t.Fatalf("expected replaced content, got %q", result.Preview)
	}
}

func TestFuzzyPatchMultipleExact(t *testing.T) {
	t.Parallel()
	content := "foo\nfoo\nbar"
	result := FuzzyPatch(content, "foo", "baz", false)
	if result.Applied {
		t.Fatal("expected failure for multiple matches without replace_all")
	}
	if result.Matches != 2 {
		t.Fatalf("expected 2 matches, got %d", result.Matches)
	}
}

func TestFuzzyPatchReplaceAll(t *testing.T) {
	t.Parallel()
	content := "foo\nfoo\nbar"
	result := FuzzyPatch(content, "foo", "baz", true)
	if !result.Applied {
		t.Fatalf("expected applied with replace_all, got error: %v", result.Error)
	}
	if result.Matches != 2 {
		t.Fatalf("expected 2 matches, got %d", result.Matches)
	}
}

func TestFuzzyPatchWhitespaceNormalization(t *testing.T) {
	t.Parallel()
	content := "  hello   world  \n  foo  bar  "
	result := FuzzyPatch(content, "hello world", "replaced", false)
	if !result.Applied {
		t.Fatalf("expected fuzzy match with whitespace normalization, got error: %v", result.Error)
	}
}

func TestFuzzyPatchIndentation(t *testing.T) {
	t.Parallel()
	content := "\t\tif x > 0 {\n\t\t\treturn true\n\t\t}"
	result := FuzzyPatch(content, "if x > 0 {\n  return true\n}", "if x > 0 {\n  return false\n}", false)
	if !result.Applied {
		t.Fatalf("expected fuzzy match with indentation, got error: %v", result.Error)
	}
	if !strings.Contains(result.Preview, "return false") {
		t.Fatalf("expected replacement applied, got %q", result.Preview)
	}
}

func TestFuzzyPatchNoMatch(t *testing.T) {
	t.Parallel()
	content := "hello world"
	result := FuzzyPatch(content, "nonexistent", "replacement", false)
	if result.Applied {
		t.Fatal("expected no match")
	}
	if result.Error == nil {
		t.Fatal("expected error for no match")
	}
}

func TestFuzzyPatchEmptyOldText(t *testing.T) {
	t.Parallel()
	result := FuzzyPatch("content", "", "new", false)
	if result.Error == nil {
		t.Fatal("expected error for empty old_text")
	}
}

func TestFuzzyPatchIdentical(t *testing.T) {
	t.Parallel()
	result := FuzzyPatch("content", "same", "same", false)
	if result.Error == nil {
		t.Fatal("expected error for identical old/new")
	}
}

func TestFuzzyPatchPreservesIndent(t *testing.T) {
	t.Parallel()
	content := "    line1\n    line2\n    line3"
	result := FuzzyPatch(content, "line2", "replaced", false)
	if !result.Applied {
		t.Fatalf("expected applied, got error: %v", result.Error)
	}
	if !strings.Contains(result.Preview, "    replaced") {
		t.Fatalf("expected preserved indent, got %q", result.Preview)
	}
}
