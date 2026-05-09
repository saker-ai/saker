package im

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cinience/saker/pkg/api"
	"github.com/godeps/goim"
)

func TestIMConfigTool_Name(t *testing.T) {
	tool := NewIMBridgeTool(goim.NewIMController(""), api.Options{})
	if tool.Name() != "im_config" {
		t.Errorf("expected name 'im_config', got %q", tool.Name())
	}
}

func TestIMConfigTool_Schema(t *testing.T) {
	tool := NewIMBridgeTool(goim.NewIMController(""), api.Options{})
	s := tool.Schema()
	if s == nil {
		t.Fatal("schema is nil")
	}
	if len(s.Required) != 1 || s.Required[0] != "action" {
		t.Errorf("expected required=[action], got %v", s.Required)
	}
}

func TestIMConfigTool_ListEmpty(t *testing.T) {
	tool := NewIMBridgeTool(goim.NewIMController(""), api.Options{})
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
}

func TestIMConfigTool_SaveAndList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "channels.json")
	ctrl := goim.NewIMController(path)
	tool := NewIMBridgeTool(ctrl, api.Options{})

	// Save a channel.
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":   "save",
		"platform": "telegram",
		"credentials": map[string]interface{}{
			"token": "test-token",
		},
	})
	if err != nil {
		t.Fatalf("Execute save: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got: %s", result.Output)
	}

	// Verify file exists.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("channels.json not created: %v", err)
	}

	// List should show the channel.
	result, err = tool.Execute(context.Background(), map[string]interface{}{
		"action": "list",
	})
	if err != nil {
		t.Fatalf("Execute list: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	if result.Output == "No channels configured yet." {
		t.Error("expected channels in list output")
	}
}

func TestIMConfigTool_SaveMissingFields(t *testing.T) {
	tool := NewIMBridgeTool(goim.NewIMController(""), api.Options{})

	// Missing platform.
	result, _ := tool.Execute(context.Background(), map[string]interface{}{
		"action":      "save",
		"credentials": map[string]interface{}{"token": "x"},
	})
	if result.Success {
		t.Error("expected failure for missing platform")
	}

	// Missing credentials.
	result, _ = tool.Execute(context.Background(), map[string]interface{}{
		"action":   "save",
		"platform": "telegram",
	})
	if result.Success {
		t.Error("expected failure for missing credentials")
	}
}

func TestIMConfigTool_Delete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "channels.json")
	ctrl := goim.NewIMController(path)
	tool := NewIMBridgeTool(ctrl, api.Options{})

	// Save first.
	tool.Execute(context.Background(), map[string]interface{}{
		"action":      "save",
		"platform":    "telegram",
		"credentials": map[string]interface{}{"token": "x"},
	})

	// Delete.
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action":   "delete",
		"platform": "telegram",
	})
	if err != nil {
		t.Fatalf("Execute delete: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got: %s", result.Output)
	}

	// Delete non-existent.
	result, _ = tool.Execute(context.Background(), map[string]interface{}{
		"action":   "delete",
		"platform": "telegram",
	})
	if result.Success {
		t.Error("expected failure for non-existent channel")
	}
}

func TestIMConfigTool_UnknownAction(t *testing.T) {
	tool := NewIMBridgeTool(goim.NewIMController(""), api.Options{})
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "restart",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for unknown action")
	}
}
