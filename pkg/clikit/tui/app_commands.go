// app_commands.go: tea.Cmd factories for streaming runs and tool-output formatting helpers.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/clikit"
	"github.com/google/uuid"
)

// runStream starts streaming the model response in a goroutine and sends
// events back to the bubbletea program via tea.Cmd.
func (a *App) runStream(prompt string) tea.Cmd {
	return func() tea.Msg {
		runCtx, runCancel := context.WithCancel(a.ctx)
		a.runCancel = runCancel

		ctx := runCtx
		if a.cfg.TimeoutMs > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(runCtx, time.Duration(a.cfg.TimeoutMs)*time.Millisecond)
			defer cancel()
		}

		ch, err := a.cfg.Engine.RunStream(ctx, a.sessionID, prompt)
		if err != nil {
			runCancel()
			a.runCancel = nil
			return StreamErrorMsg{Err: err}
		}

		var pendingToolInput strings.Builder // accumulate tool input JSON
		for evt := range ch {
			switch evt.Type {
			case api.EventContentBlockDelta:
				if evt.Delta != nil {
					switch evt.Delta.Type {
					case "text_delta":
						a.chat.AppendStreamText(evt.Delta.Text)
					case "input_json_delta":
						// Accumulate tool input JSON for param extraction.
						var chunk string
						_ = json.Unmarshal(evt.Delta.PartialJSON, &chunk)
						pendingToolInput.WriteString(chunk)
					}
				}
			case api.EventToolExecutionStart:
				a.chat.FinishStreaming()
				params := extractToolParamsFromJSON(evt.Name, pendingToolInput.String())
				pendingToolInput.Reset()
				a.chat.AddToolCallWithParams(evt.Name, params, "pending")
				a.status.SetText(fmt.Sprintf("Tool: %s", evt.Name))
			case api.EventToolExecutionOutput:
				output := formatToolOutput(evt)
				if output != "" {
					a.chat.UpdateLastToolOutput(output)
				}
			case api.EventToolExecutionResult:
				result, isErr := formatToolResult(evt)
				if result != "" {
					a.chat.UpdateLastToolOutput(result)
				}
				if isErr {
					a.chat.UpdateLastToolStatus("error")
				} else {
					a.chat.UpdateLastToolStatus("success")
				}
				// Display inline images from tool result artifacts.
				for _, path := range extractImagePaths(evt) {
					a.chat.AddImage(path)
				}
				a.chat.StartStreaming()
				a.status.SetText("Thinking...")
			case api.EventMessageDelta:
				if evt.Usage != nil && (evt.Usage.InputTokens > 0 || evt.Usage.OutputTokens > 0) {
					a.status.AddTokens(evt.Usage.InputTokens, evt.Usage.OutputTokens)
				}
			case api.EventError:
				if evt.Output != nil {
					a.chat.AddError(fmt.Sprintf("%v", evt.Output))
				}
			}
		}

		runCancel()
		a.runCancel = nil
		return StreamDoneMsg{}
	}
}

// runBtw runs a /btw side question independently of the main stream.
// Text is written directly to the side panel overlay.
func (a *App) runBtw(question string) tea.Cmd {
	return func() tea.Msg {
		btwCtx, btwCancel := context.WithCancel(a.ctx)
		a.sidePanelCancel = btwCancel

		btwSessionID := "btw-" + uuid.NewString()
		wrappedPrompt := clikit.SideQuestionPromptWrapper + question

		// Fork from main session to inherit conversation context.
		ch, err := a.cfg.Engine.RunStreamForked(btwCtx, a.sessionID, btwSessionID, wrappedPrompt)
		if err != nil {
			btwCancel()
			return BtwErrorMsg{Err: err}
		}

		for evt := range ch {
			if evt.Type == api.EventContentBlockDelta && evt.Delta != nil && evt.Delta.Type == "text_delta" {
				if a.sidePanel != nil {
					a.sidePanel.AppendText(evt.Delta.Text)
				}
			}
		}

		btwCancel()
		return BtwDoneMsg{}
	}
}

// runIM runs an /im side question for IM bridge management.
// Text is written directly to the side panel overlay.
func (a *App) runIM(instruction string) tea.Cmd {
	return func() tea.Msg {
		imCtx, imCancel := context.WithCancel(a.ctx)
		a.sidePanelCancel = imCancel

		imSessionID := "im-" + uuid.NewString()
		wrappedPrompt := clikit.IMSidePromptWrapper + instruction

		// Fork from main session to inherit conversation context.
		ch, err := a.cfg.Engine.RunStreamForked(imCtx, a.sessionID, imSessionID, wrappedPrompt)
		if err != nil {
			imCancel()
			return IMErrorMsg{Err: err}
		}

		for evt := range ch {
			switch evt.Type {
			case api.EventContentBlockDelta:
				if evt.Delta != nil && evt.Delta.Type == "text_delta" {
					if a.sidePanel != nil {
						a.sidePanel.AppendText(evt.Delta.Text)
					}
				}
			case api.EventToolExecutionStart:
				if a.sidePanel != nil {
					a.sidePanel.AppendText(fmt.Sprintf("\n[tool: %s]\n", evt.Name))
				}
			}
		}

		imCancel()
		return IMDoneMsg{}
	}
}

// runIMFollowUp sends a follow-up message in the /im panel's session.
func (a *App) runIMFollowUp(text string) tea.Cmd {
	return func() tea.Msg {
		if a.sidePanel == nil {
			return IMDoneMsg{}
		}

		followCtx, followCancel := context.WithCancel(a.ctx)
		a.sidePanelCancel = followCancel

		sessionID := a.sidePanel.SessionID()

		ch, err := a.cfg.Engine.RunStream(followCtx, sessionID, text)
		if err != nil {
			followCancel()
			return IMErrorMsg{Err: err}
		}

		for evt := range ch {
			switch evt.Type {
			case api.EventContentBlockDelta:
				if evt.Delta != nil && evt.Delta.Type == "text_delta" {
					if a.sidePanel != nil {
						a.sidePanel.AppendText(evt.Delta.Text)
					}
				}
			case api.EventToolExecutionStart:
				if a.sidePanel != nil {
					a.sidePanel.AppendText(fmt.Sprintf("\n[tool: %s]\n", evt.Name))
				}
			}
		}

		followCancel()
		return IMDoneMsg{}
	}
}

// extractToolParamsFromJSON extracts a brief parameter summary from accumulated
// tool input JSON, following Claude Code's display pattern:
//   - Read/Write: show file path
//   - Bash: show command (truncated)
//   - Grep/Glob: show pattern
//   - Other: empty
func extractToolParamsFromJSON(toolName, inputJSON string) string {
	if inputJSON == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &m); err != nil {
		return ""
	}

	switch {
	case strings.Contains(strings.ToLower(toolName), "read"),
		strings.Contains(strings.ToLower(toolName), "write"),
		strings.Contains(strings.ToLower(toolName), "edit"):
		if fp, ok := m["file_path"].(string); ok {
			return filepath.Base(fp)
		}
	case strings.Contains(strings.ToLower(toolName), "bash"):
		if cmd, ok := m["command"].(string); ok {
			// Truncate long commands like Claude Code (max 2 lines, 160 chars).
			lines := strings.SplitN(cmd, "\n", 3)
			display := strings.Join(lines[:min(len(lines), 2)], " ")
			if len(display) > 80 {
				return display[:77] + "…"
			}
			return display
		}
	case strings.Contains(strings.ToLower(toolName), "grep"):
		if pat, ok := m["pattern"].(string); ok {
			if len(pat) > 60 {
				return pat[:57] + "…"
			}
			return pat
		}
	case strings.Contains(strings.ToLower(toolName), "glob"):
		if pat, ok := m["pattern"].(string); ok {
			return pat
		}
	}
	return ""
}

// formatToolOutput creates a brief output summary from a tool_execution_output event.
// This is the raw tool output before the final result event.
func formatToolOutput(evt api.StreamEvent) string {
	if evt.Output == nil {
		return ""
	}
	s := fmt.Sprintf("%v", evt.Output)
	return summarizeOutput(evt.Name, s)
}

// formatToolResult creates a result summary from a tool_execution_result event.
// Returns (summary, isError).
func formatToolResult(evt api.StreamEvent) (string, bool) {
	if evt.Output == nil {
		return "", false
	}
	m, ok := evt.Output.(map[string]any)
	if !ok {
		return "", false
	}

	// Check for error in metadata.
	isErr := false
	if meta, ok := m["metadata"].(map[string]any); ok {
		if errFlag, ok := meta["is_error"].(bool); ok && errFlag {
			isErr = true
		}
	}

	out, _ := m["output"].(string)
	if out == "" {
		return "", isErr
	}

	return summarizeToolResult(evt.Name, out), isErr
}

// summarizeOutput returns a short display string for raw tool output.
func summarizeOutput(toolName, output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	n := len(lines)

	name := strings.ToLower(toolName)
	switch {
	case strings.Contains(name, "bash"):
		// Show last 2 lines of bash output as preview.
		if n <= 2 {
			return truncLine(output, 100)
		}
		last := strings.TrimSpace(lines[n-1])
		if last == "" && n >= 2 {
			last = strings.TrimSpace(lines[n-2])
		}
		return fmt.Sprintf("… %s (%d lines)", truncLine(last, 60), n)
	default:
		if n <= 2 {
			return truncLine(output, 100)
		}
		return fmt.Sprintf("%d lines", n)
	}
}

// summarizeToolResult generates a tool-specific result summary.
func summarizeToolResult(toolName, output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	n := len(lines)
	name := strings.ToLower(toolName)

	switch {
	case strings.Contains(name, "read"):
		return fmt.Sprintf("Read %d %s", n, pluralize(n, "line", "lines"))

	case strings.Contains(name, "write"):
		return fmt.Sprintf("Wrote %d %s", n, pluralize(n, "line", "lines"))

	case strings.Contains(name, "edit"):
		return "Applied changes"

	case strings.Contains(name, "grep"):
		// Count file matches from output lines.
		count := 0
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				count++
			}
		}
		return fmt.Sprintf("%d %s", count, pluralize(count, "match", "matches"))

	case strings.Contains(name, "glob"):
		count := 0
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				count++
			}
		}
		return fmt.Sprintf("%d %s", count, pluralize(count, "file", "files"))

	case strings.Contains(name, "bash"):
		if n <= 2 {
			return truncLine(output, 100)
		}
		// Show last meaningful line + line count.
		last := lastNonEmpty(lines)
		return fmt.Sprintf("… %s (%d lines)", truncLine(last, 60), n)

	default:
		if n == 1 {
			return truncLine(output, 100)
		}
		return fmt.Sprintf("%d lines", n)
	}
}

func truncLine(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func lastNonEmpty(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s != "" {
			return s
		}
	}
	return ""
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// extractImagePaths pulls local image file paths from a tool_execution_result event.
// Artifacts are stored in metadata["artifacts"] as []artifact.ArtifactRef or []any.
func extractImagePaths(evt api.StreamEvent) []string {
	m, ok := evt.Output.(map[string]any)
	if !ok {
		return nil
	}
	meta, _ := m["metadata"].(map[string]any)
	if meta == nil {
		return nil
	}
	raw, ok := meta["artifacts"]
	if !ok {
		return nil
	}

	var paths []string
	imageExts := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}

	switch arts := raw.(type) {
	case []artifact.ArtifactRef:
		for _, ref := range arts {
			if ref.Path != "" && (ref.Kind == artifact.ArtifactKindImage || imageExts[strings.ToLower(filepath.Ext(ref.Path))]) {
				paths = append(paths, ref.Path)
			}
		}
	case []any:
		for _, item := range arts {
			if ref, ok := item.(map[string]any); ok {
				path, _ := ref["path"].(string)
				kind, _ := ref["kind"].(string)
				if path != "" && (kind == "image" || imageExts[strings.ToLower(filepath.Ext(path))]) {
					paths = append(paths, path)
				}
			}
		}
	}
	return paths
}
