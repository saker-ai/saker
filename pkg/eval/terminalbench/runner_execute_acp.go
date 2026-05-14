// runner_execute_acp.go: ACP-based agent execution for end-to-end evaluation.
//
// Instead of the bare agent loop (modelBridge + historyToolExecutor), this
// path creates a full Saker Runtime via the ACP protocol. The Runtime brings
// middleware, compaction, prompt-cache, hooks, failover — the full product
// stack. Tool calls flow back to the runner through ACP capability callbacks
// and execute inside the per-task Docker container.
package terminalbench

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"

	acp "github.com/cinience/saker/pkg/acp"
	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/conversation"
	"github.com/cinience/saker/pkg/eval/dataset"
	"github.com/cinience/saker/pkg/message"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	acpproto "github.com/coder/acp-go-sdk"
)

// runAgentACP replaces the bare agent loop with a full Saker Runtime
// reached through an in-process ACP connection. The dockerACPClient
// routes all tool callbacks (bash, read, write, edit) into the Docker
// container so the agent operates identically to bare mode — just with
// the full product middleware stack in the path.
func (r *Runner) runAgentACP(
	ctx context.Context,
	task dataset.Task,
	env sandboxenv.ExecutionEnvironment,
	ps *sandboxenv.PreparedSession,
	guestRoot string,
	res *TaskResult,
) {
	tmpDir, err := os.MkdirTemp("", "tb2-acp-*")
	if err != nil {
		res.Stage = "acp-tmpdir"
		res.ErrorMsg = err.Error()
		return
	}
	defer os.RemoveAll(tmpDir)

	cs, err := conversation.Open(conversation.Config{
		FallbackPath: filepath.Join(tmpDir, "conversation.db"),
	})
	if err != nil {
		res.Stage = "acp-conversation-store"
		res.ErrorMsg = err.Error()
		return
	}
	defer cs.Close()

	opts := api.Options{
		ProjectRoot:       tmpDir,
		ConversationStore: cs,
		EntryPoint:        api.EntryPointCI,
		ModelFactory:      api.ModelFactoryFunc(r.cfg.ModelFactory),
		DangerouslySkipPermissions: true,
		MaxIterations:              r.cfg.MaxIterations,
		Timeout:                    r.taskAgentCap(task),
	}
	if strings.TrimSpace(r.cfg.SystemPrompt) != "" {
		opts.SystemPrompt = r.cfg.SystemPrompt
	}

	// --- set up in-process ACP connection (net.Pipe) ---

	agentPipe, clientPipe := net.Pipe()
	defer agentPipe.Close()
	defer clientPipe.Close()

	adapter := acp.NewAdapter(opts)
	agentConn := acpproto.NewAgentSideConnection(adapter, agentPipe, agentPipe)
	adapter.SetConnection(agentConn)

	dockerClient := newDockerACPClient(ctx, env, ps, r.cfg.TerminalTimeout)
	clientConn := acpproto.NewClientSideConnection(dockerClient, clientPipe, clientPipe)

	// --- Initialize handshake ---

	if _, err := clientConn.Initialize(ctx, acpproto.InitializeRequest{
		ProtocolVersion: acpproto.ProtocolVersionNumber,
		ClientCapabilities: acpproto.ClientCapabilities{
			Terminal: true,
			Fs: acpproto.FileSystemCapability{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
		},
	}); err != nil {
		res.Stage = "acp-initialize"
		res.ErrorMsg = err.Error()
		return
	}

	// --- Create session ---

	sess, err := clientConn.NewSession(ctx, acpproto.NewSessionRequest{
		Cwd:        guestRoot,
		McpServers: []acpproto.McpServer{},
	})
	if err != nil {
		res.Stage = "acp-new-session"
		res.ErrorMsg = err.Error()
		return
	}

	// Set code mode for auto-approval of all tools.
	_, _ = clientConn.SetSessionMode(ctx, acpproto.SetSessionModeRequest{
		SessionId: sess.SessionId,
		ModeId:    "code",
	})

	dockerClient.clearUpdates()

	// --- Send prompt ---

	promptResp, err := clientConn.Prompt(ctx, acpproto.PromptRequest{
		SessionId: sess.SessionId,
		Prompt:    []acpproto.ContentBlock{acpproto.TextBlock(task.Instruction)},
	})
	if err != nil {
		if reason := extractACPStopReason(err); reason != "" {
			res.StopReason = reason
		} else {
			res.Stage = "acp-prompt"
			res.ErrorMsg = err.Error()
		}
		// Fall through — partial completion can still pass tests.
	}

	// --- Extract results from session updates ---

	updates := dockerClient.updatesSnapshot()
	if res.StopReason == "" {
		res.StopReason = string(promptResp.StopReason)
	}

	var agentOutput strings.Builder
	iterations := 0
	for _, u := range updates {
		if u.Update.AgentMessageChunk != nil && u.Update.AgentMessageChunk.Content.Text != nil {
			agentOutput.WriteString(u.Update.AgentMessageChunk.Content.Text.Text)
		}
		if u.Update.ToolCall != nil {
			iterations++
		}
	}
	res.Iterations = iterations
	if res.Iterations == 0 {
		res.Iterations = 1
	}

	if promptResp.Usage != nil {
		res.InputTokens = int(promptResp.Usage.InputTokens)
		res.OutputTokens = int(promptResp.Usage.OutputTokens)
		if promptResp.Usage.CachedReadTokens != nil {
			res.CacheReadTokens = int(*promptResp.Usage.CachedReadTokens)
		}
		if promptResp.Usage.CachedWriteTokens != nil {
			res.CacheCreationTokens = int(*promptResp.Usage.CachedWriteTokens)
		}
	}

	// --- Write transcript from session updates ---

	if !r.cfg.DisableTranscripts {
		msgs := updatesToTranscriptMessages(updates, sess.SessionId)
		if path, terr := writeTranscript(r.cfg.OutputDir, task.Name, msgs); terr != nil && res.ErrorMsg == "" {
			res.Stage = "write-transcript"
			res.ErrorMsg = terr.Error()
		} else if path != "" {
			res.TranscriptPath = path
		}
	}
}

// updatesToTranscriptMessages converts ACP session updates into
// message.Message records suitable for the JSONL transcript writer.
//
// Deduplication: ACP sends multiple ToolCallUpdate notifications per tool
// call (status changes, partial output, final output). We keep only the
// last non-empty result per ToolCallId to avoid 2x transcript bloat.
// Arguments: captured from both the initial ToolCall and subsequent
// ToolCallUpdate.RawInput (whichever is non-nil first).
func updatesToTranscriptMessages(updates []acpproto.SessionNotification, sessionID acpproto.SessionId) []message.Message {
	var msgs []message.Message
	var pendingText strings.Builder

	// Collect tool call arguments and results across multiple updates.
	type toolInfo struct {
		args   map[string]any
		name   string
		result string
	}
	toolData := make(map[string]*toolInfo)

	flushText := func() {
		if pendingText.Len() > 0 {
			msgs = append(msgs, message.Message{
				Role:    "assistant",
				Content: pendingText.String(),
			})
			pendingText.Reset()
		}
	}

	// First pass: collect all tool data (args + results).
	for _, u := range updates {
		if u.SessionId != sessionID {
			continue
		}
		up := u.Update

		if up.ToolCall != nil {
			id := string(up.ToolCall.ToolCallId)
			info := &toolInfo{name: up.ToolCall.Title}
			if up.ToolCall.RawInput != nil {
				if raw, err := json.Marshal(up.ToolCall.RawInput); err == nil {
					_ = json.Unmarshal(raw, &info.args)
				}
			}
			toolData[id] = info
		}

		if up.ToolCallUpdate != nil {
			id := string(up.ToolCallUpdate.ToolCallId)
			if toolData[id] == nil {
				toolData[id] = &toolInfo{}
			}
			// Capture args from update if initial was empty.
			if toolData[id].args == nil && up.ToolCallUpdate.RawInput != nil {
				if raw, err := json.Marshal(up.ToolCallUpdate.RawInput); err == nil {
					_ = json.Unmarshal(raw, &toolData[id].args)
				}
			}
			// Keep the last non-empty result (final > intermediate).
			content := ""
			if up.ToolCallUpdate.RawOutput != nil {
				raw, _ := json.Marshal(up.ToolCallUpdate.RawOutput)
				content = string(raw)
			} else if len(up.ToolCallUpdate.Content) > 0 {
				for _, block := range up.ToolCallUpdate.Content {
					if block.Content != nil && block.Content.Content.Text != nil {
						content += block.Content.Content.Text.Text
					}
				}
			}
			if content != "" {
				toolData[id].result = content
			}
		}
	}

	// Second pass: emit messages in order, using deduplicated tool data.
	emittedResults := make(map[string]bool)

	for _, u := range updates {
		if u.SessionId != sessionID {
			continue
		}
		up := u.Update

		if up.AgentMessageChunk != nil && up.AgentMessageChunk.Content.Text != nil {
			pendingText.WriteString(up.AgentMessageChunk.Content.Text.Text)
		}

		if up.ToolCall != nil {
			flushText()
			id := string(up.ToolCall.ToolCallId)
			info := toolData[id]
			msgs = append(msgs, message.Message{
				Role: "assistant",
				ToolCalls: []message.ToolCall{{
					ID:        id,
					Name:      info.name,
					Arguments: info.args,
				}},
			})
		}

		if up.ToolCallUpdate != nil {
			id := string(up.ToolCallUpdate.ToolCallId)
			if emittedResults[id] {
				continue
			}
			info := toolData[id]
			if info != nil && info.result != "" {
				emittedResults[id] = true
				msgs = append(msgs, message.Message{
					Role: "tool",
					ToolCalls: []message.ToolCall{{
						ID:     id,
						Result: info.result,
					}},
				})
			}
		}
	}
	flushText()
	return msgs
}

// extractACPStopReason checks if an ACP error is actually a known agent
// stop condition (max_iterations, max_budget, max_tokens) rather than a
// true failure. Returns the stop reason string or "" if it's a real error.
func extractACPStopReason(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "max iterations"):
		return "max_iterations"
	case strings.Contains(msg, "max_budget"):
		return "max_budget"
	case strings.Contains(msg, "max_tokens"):
		return "max_tokens"
	case strings.Contains(msg, "repeat"):
		return "repeat_loop"
	case strings.Contains(msg, "context deadline exceeded"):
		return "timeout"
	case strings.Contains(msg, "context canceled"):
		return "cancelled"
	default:
		return ""
	}
}
