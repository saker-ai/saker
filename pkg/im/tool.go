package im

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/tool"
	"github.com/godeps/goim"
)

// IMBridgeTool implements tool.Tool so the LLM agent can manage IM channel
// credentials through natural conversation in TUI mode.
//
// In TUI mode this tool only manages ~/.saker/channels.json (save/list/delete).
// In gateway mode (--gateway flag) the controller handles actual bridge startup.
type IMBridgeTool struct {
	ctrl *goim.IMController
	opts api.Options

	mu sync.Mutex
	rt *api.Runtime
}

// NewIMBridgeTool creates a tool wrapping the given controller.
func NewIMBridgeTool(ctrl *goim.IMController, opts api.Options) *IMBridgeTool {
	return &IMBridgeTool{ctrl: ctrl, opts: opts}
}

// SetRuntime injects the runtime reference after creation.
func (t *IMBridgeTool) SetRuntime(rt *api.Runtime) {
	t.mu.Lock()
	t.rt = rt
	t.mu.Unlock()
}

func (t *IMBridgeTool) Name() string { return "im_config" }

func (t *IMBridgeTool) Description() string {
	return "Manage IM channel credentials in ~/.saker/channels.json. " +
		"Use action 'save' to store credentials, 'list' to view configured channels, " +
		"'delete' to remove a channel. To actually run the IM bridge, use 'saker --gateway <platform>'."
}

var imConfigSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"action": map[string]interface{}{
			"type":        "string",
			"description": "The action to perform: save, list, or delete",
			"enum":        []interface{}{"save", "list", "delete"},
		},
		"platform": map[string]interface{}{
			"type":        "string",
			"description": "IM platform name (required for save/delete). Options: telegram, feishu, discord, slack, dingtalk, wecom, qq, qqbot, line, weixin",
		},
		"credentials": map[string]interface{}{
			"type": "object",
			"description": "Platform-specific credentials (required for save). Each platform requires different fields:\n" +
				"- telegram: {\"token\": \"...\"}\n" +
				"- feishu: {\"app_id\": \"...\", \"app_secret\": \"...\"}\n" +
				"- discord: {\"token\": \"...\"}\n" +
				"- slack: {\"bot_token\": \"...\", \"app_token\": \"...\"}\n" +
				"- dingtalk: {\"client_id\": \"...\", \"client_secret\": \"...\"}\n" +
				"- wecom: {\"corp_id\": \"...\", \"corp_secret\": \"...\", \"agent_id\": \"...\"}\n" +
				"- qq: {\"ws_url\": \"...\"} (optional)\n" +
				"- qqbot: {\"app_id\": \"...\", \"app_secret\": \"...\"}\n" +
				"- line: {\"channel_secret\": \"...\", \"channel_token\": \"...\"}\n" +
				"- weixin: {\"token\": \"...\"}",
		},
		"allow_from": map[string]interface{}{
			"type":        "string",
			"description": "Comma-separated user IDs allowed to use the bot. Empty means allow all.",
		},
	},
	Required: []string{"action"},
}

func (t *IMBridgeTool) Schema() *tool.JSONSchema {
	return imConfigSchema
}

func (t *IMBridgeTool) Execute(_ context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	action, _ := params["action"].(string)

	switch action {
	case "list":
		return t.listChannels()

	case "save":
		platform, _ := params["platform"].(string)
		if platform == "" {
			return &tool.ToolResult{Success: false, Output: "platform is required for save action."}, nil
		}
		creds, _ := params["credentials"].(map[string]interface{})
		if len(creds) == 0 {
			return &tool.ToolResult{Success: false, Output: "credentials are required for save action."}, nil
		}
		allowFrom, _ := params["allow_from"].(string)

		opts := make(map[string]any, len(creds)+1)
		for k, v := range creds {
			opts[k] = v
		}
		if allowFrom != "" {
			opts["allow_from"] = allowFrom
		}

		if err := t.ctrl.SaveChannel(platform, opts); err != nil {
			return &tool.ToolResult{Success: false, Output: fmt.Sprintf("Failed to save: %v", err)}, nil
		}
		return &tool.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("Channel %q saved to ~/.saker/channels.json. Run 'saker --gateway %s' to start the bridge.", platform, platform),
		}, nil

	case "delete":
		platform, _ := params["platform"].(string)
		if platform == "" {
			return &tool.ToolResult{Success: false, Output: "platform is required for delete action."}, nil
		}
		if err := t.ctrl.DeleteChannel(platform); err != nil {
			return &tool.ToolResult{Success: false, Output: fmt.Sprintf("Failed to delete: %v", err)}, nil
		}
		return &tool.ToolResult{
			Success: true,
			Output:  fmt.Sprintf("Channel %q removed from channels.json.", platform),
		}, nil

	default:
		return &tool.ToolResult{
			Success: false,
			Output:  fmt.Sprintf("Unknown action %q. Use 'save', 'list', or 'delete'.", action),
		}, nil
	}
}

func (t *IMBridgeTool) listChannels() (*tool.ToolResult, error) {
	if t.ctrl.ChannelsPath == "" {
		return &tool.ToolResult{Success: true, Output: "No channels.json configured."}, nil
	}

	chCfg, err := goim.LoadChannelsJSON(t.ctrl.ChannelsPath)
	if err != nil {
		return &tool.ToolResult{Success: false, Output: fmt.Sprintf("Failed to load channels.json: %v", err)}, nil
	}

	if len(chCfg.Channels) == 0 {
		return &tool.ToolResult{Success: true, Output: "No channels configured yet."}, nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Configured channels (%s):\n", t.ctrl.ChannelsPath))
	for name, opts := range chCfg.Channels {
		enabled := true
		if v, ok := opts["enabled"]; ok {
			if eb, isBool := v.(bool); isBool {
				enabled = eb
			}
		}
		status := "enabled"
		if !enabled {
			status = "disabled"
		}
		// Mask credential values for security.
		fields := make([]string, 0, len(opts))
		for k := range opts {
			if k == "enabled" {
				continue
			}
			fields = append(fields, k)
		}
		b.WriteString(fmt.Sprintf("  - %s [%s] (fields: %s)\n", name, status, strings.Join(fields, ", ")))
	}
	b.WriteString(fmt.Sprintf("\nTo start: saker --gateway <platform>"))

	// Also show home dir for context.
	if home, err := os.UserHomeDir(); err == nil {
		_ = home // path already shown above
	}

	return &tool.ToolResult{Success: true, Output: b.String()}, nil
}
