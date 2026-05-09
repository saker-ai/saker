package toolbuiltin

import (
	"context"
	"testing"
)

func TestBrowserTool_NameAndSchema(t *testing.T) {
	bt := &BrowserTool{} // no allocator — just test metadata
	if bt.Name() != "browser" {
		t.Fatalf("expected name 'browser', got %q", bt.Name())
	}
	if bt.Description() == "" {
		t.Fatal("expected non-empty description")
	}
	schema := bt.Schema()
	if schema == nil {
		t.Fatal("expected non-nil schema")
	}
	if schema.Type != "object" {
		t.Fatalf("expected object schema, got %q", schema.Type)
	}
	if _, ok := schema.Properties["action"]; !ok {
		t.Fatal("schema missing 'action' property")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "action" {
		t.Fatalf("expected Required=[action], got %v", schema.Required)
	}
}

func TestBrowserTool_Execute_MissingAction(t *testing.T) {
	bt := &BrowserTool{}
	_, err := bt.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing action")
	}
}

func TestBrowserTool_Execute_UnknownAction(t *testing.T) {
	bt := &BrowserTool{}
	_, err := bt.Execute(context.Background(), map[string]any{"action": "fly"})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestValidateBrowserURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://example.com", true},
		{"http://localhost:8080", true},
		{"HTTP://EXAMPLE.COM", true},
		{"file:///etc/passwd", false},
		{"data:text/html,<h1>hi</h1>", false},
		{"javascript:alert(1)", false},
		{"ftp://files.example.com", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			err := validateBrowserURL(tc.url)
			if tc.ok && err != nil {
				t.Errorf("expected valid URL %q, got error: %v", tc.url, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("expected invalid URL %q to be rejected", tc.url)
			}
		})
	}
}

func TestBrowserTool_Navigate_MissingURL(t *testing.T) {
	bt := &BrowserTool{}
	_, err := bt.navigate(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing url")
	}
}

func TestBrowserTool_Navigate_BlockedURL(t *testing.T) {
	bt := &BrowserTool{}
	_, err := bt.navigate(context.Background(), map[string]any{"url": "file:///etc/passwd"})
	if err == nil {
		t.Fatal("expected error for file:// URL")
	}
}

func TestBrowserTool_Click_MissingSelector(t *testing.T) {
	bt := &BrowserTool{}
	_, err := bt.click(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing selector")
	}
}

func TestBrowserTool_Type_MissingParams(t *testing.T) {
	bt := &BrowserTool{}
	_, err := bt.typeText(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing selector")
	}
	_, err = bt.typeText(context.Background(), map[string]any{"selector": "#input"})
	if err == nil {
		t.Fatal("expected error for missing text")
	}
}

func TestBrowserTool_Evaluate_MissingExpression(t *testing.T) {
	bt := &BrowserTool{}
	_, err := bt.evaluate(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing expression")
	}
}

func TestBrowserTool_Close_NilSafe(t *testing.T) {
	bt := &BrowserTool{}
	bt.Close() // should not panic
}
