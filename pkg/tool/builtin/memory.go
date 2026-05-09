package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cinience/saker/pkg/memory"
	"github.com/cinience/saker/pkg/tool"
)

// MemorySaveTool saves a memory entry to the store.
type MemorySaveTool struct {
	store *memory.Store
}

// NewMemorySaveTool creates a tool that saves memory entries.
func NewMemorySaveTool(store *memory.Store) *MemorySaveTool {
	return &MemorySaveTool{store: store}
}

func (t *MemorySaveTool) Name() string { return "memory_save" }
func (t *MemorySaveTool) Description() string {
	return "Save a memory entry for cross-session persistence. Types: user, feedback, project, reference."
}

func (t *MemorySaveTool) Schema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Short identifier for the memory entry (e.g., user_role, project_auth)",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "One-line description used to decide relevance in future sessions",
			},
			"type": map[string]interface{}{
				"type":        "string",
				"description": "Memory type: user, feedback, project, or reference",
				"enum":        []string{"user", "feedback", "project", "reference"},
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The memory content to persist",
			},
		},
		Required: []string{"name", "type", "content"},
	}
}

func (t *MemorySaveTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	if t.store == nil {
		return &tool.ToolResult{Success: false, Output: "memory store not configured"}, nil
	}

	name, _ := params["name"].(string)
	desc, _ := params["description"].(string)
	typ, _ := params["type"].(string)
	content, _ := params["content"].(string)

	entry := memory.Entry{
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(desc),
		Type:        memory.MemoryType(typ),
		Content:     strings.TrimSpace(content),
	}

	if err := t.store.Save(entry); err != nil {
		return &tool.ToolResult{Success: false, Output: fmt.Sprintf("failed to save: %v", err)}, nil
	}

	return &tool.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Memory '%s' saved (type: %s)", entry.Name, entry.Type),
	}, nil
}

// MemoryReadTool reads memory entries from the store.
type MemoryReadTool struct {
	store *memory.Store
}

// NewMemoryReadTool creates a tool that reads memory entries.
func NewMemoryReadTool(store *memory.Store) *MemoryReadTool {
	return &MemoryReadTool{store: store}
}

func (t *MemoryReadTool) Name() string { return "memory_read" }
func (t *MemoryReadTool) Description() string {
	return "Read memory entries. Without a name, returns the memory index. With a name, returns a specific entry."
}

func (t *MemoryReadTool) Schema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Name of a specific memory entry to read. If omitted, returns the index.",
			},
		},
	}
}

func (t *MemoryReadTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
	if t.store == nil {
		return &tool.ToolResult{Success: false, Output: "memory store not configured"}, nil
	}

	name, _ := params["name"].(string)
	name = strings.TrimSpace(name)

	if name == "" {
		index, err := t.store.LoadIndex()
		if err != nil {
			return &tool.ToolResult{Success: false, Output: fmt.Sprintf("failed to load index: %v", err)}, nil
		}
		if index == "" {
			return &tool.ToolResult{Success: true, Output: "No memory entries found."}, nil
		}
		return &tool.ToolResult{Success: true, Output: index}, nil
	}

	entry, err := t.store.Load(name)
	if err != nil {
		return &tool.ToolResult{Success: false, Output: fmt.Sprintf("failed to load '%s': %v", name, err)}, nil
	}

	data, _ := json.MarshalIndent(map[string]string{
		"name":        entry.Name,
		"description": entry.Description,
		"type":        string(entry.Type),
		"content":     entry.Content,
	}, "", "  ")
	return &tool.ToolResult{Success: true, Output: string(data)}, nil
}
