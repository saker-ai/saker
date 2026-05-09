package toolbuiltin

import (
	"strings"
	"testing"
)

func TestHostRedirectError(t *testing.T) {
	err := &hostRedirectError{target: "http://example.com"}
	if err.Error() == "" {
		t.Fatalf("expected error string")
	}
}

func TestHtmlToMarkdownTable(t *testing.T) {
	t.Parallel()

	html := `<table><thead><tr><th>Name</th><th>Age</th></tr></thead><tbody><tr><td>Alice</td><td>30</td></tr></tbody></table>`
	md := htmlToMarkdown(html)

	if !strings.Contains(md, "| Name | Age |") {
		t.Errorf("expected header row, got: %q", md)
	}
	if !strings.Contains(md, "| --- | --- |") {
		t.Errorf("expected separator row, got: %q", md)
	}
	if !strings.Contains(md, "| Alice | 30 |") {
		t.Errorf("expected data row, got: %q", md)
	}
}

func TestHtmlToMarkdownTableNoHeader(t *testing.T) {
	t.Parallel()

	html := `<table><tbody><tr><td>A</td><td>B</td></tr></tbody></table>`
	md := htmlToMarkdown(html)

	if !strings.Contains(md, "| A | B |") {
		t.Errorf("expected data row, got: %q", md)
	}
	if strings.Contains(md, "---") {
		t.Errorf("expected no separator row for headerless table, got: %q", md)
	}
}

func TestHtmlToMarkdownBlockquote(t *testing.T) {
	t.Parallel()

	input := `<blockquote>quoted text</blockquote>`
	md := htmlToMarkdown(input)

	if !strings.Contains(md, "> quoted text") {
		t.Errorf("expected blockquote prefix '> quoted text', got: %q", md)
	}
}

func TestHtmlToMarkdownHorizontalRule(t *testing.T) {
	t.Parallel()

	html := `<p>before</p><hr/><p>after</p>`
	md := htmlToMarkdown(html)

	if !strings.Contains(md, "---") {
		t.Errorf("expected horizontal rule, got: %q", md)
	}
	if !strings.Contains(md, "before") || !strings.Contains(md, "after") {
		t.Errorf("expected surrounding content, got: %q", md)
	}
}

func TestHtmlToMarkdownDetails(t *testing.T) {
	t.Parallel()

	html := `<details><summary>Click me</summary><p>Hidden content</p></details>`
	md := htmlToMarkdown(html)

	if !strings.Contains(md, "**Click me**") {
		t.Errorf("expected bolded summary, got: %q", md)
	}
	if !strings.Contains(md, "Hidden content") {
		t.Errorf("expected details content, got: %q", md)
	}
}
