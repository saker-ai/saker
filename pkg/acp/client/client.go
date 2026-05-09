// Package client provides an ACP client that launches external agent
// processes and communicates with them over the ACP protocol via stdio.
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	acpproto "github.com/coder/acp-go-sdk"
)

var (
	ErrClientClosed  = errors.New("acp: client is closed")
	ErrDialFailed    = errors.New("acp: dial failed")
	ErrSessionFailed = errors.New("acp: session creation failed")
	ErrPromptFailed  = errors.New("acp: prompt failed")
	ErrProcessExited = errors.New("acp: agent process exited unexpectedly")
)

// DialOptions configures how to launch and connect to an external ACP agent.
type DialOptions struct {
	Command string   // Agent binary path (e.g. "claude", "codex", "./bin/saker").
	Args    []string // Extra CLI arguments; "--acp=true" is appended automatically if absent.
	Cwd     string   // Working directory for the child process.
	Env     []string // Extra environment variables (KEY=VALUE).
	Timeout time.Duration
}

// RunOptions configures a single ACP prompt turn.
type RunOptions struct {
	Cwd  string // Session working directory (defaults to DialOptions.Cwd).
	Task string // Prompt text sent to the agent.
	Mode string // Optional session mode (e.g. "code", "architect").
}

// RunResult holds the outcome of a completed ACP prompt turn.
type RunResult struct {
	Output     string
	StopReason acpproto.StopReason
	Updates    []acpproto.SessionNotification
	Usage      *acpproto.Usage
}

// ACPClient manages a connection to an external agent process over ACP/stdio.
type ACPClient struct {
	conn    *acpproto.ClientSideConnection
	handler *clientHandler
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser

	mu     sync.Mutex
	closed bool
	done   chan struct{} // closed when process exits
	cmdErr error         // process exit error
}

// Dial launches an external agent process and performs the ACP Initialize handshake.
func Dial(ctx context.Context, opts DialOptions) (*ACPClient, error) {
	if opts.Command == "" {
		return nil, fmt.Errorf("%w: command is required", ErrDialFailed)
	}

	args := ensureACPFlag(opts.Args)
	cmd := exec.CommandContext(ctx, opts.Command, args...)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	if len(opts.Env) > 0 {
		cmd.Env = append(cmd.Environ(), opts.Env...)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: stdin pipe: %v", ErrDialFailed, err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: stdout pipe: %v", ErrDialFailed, err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: start process: %v", ErrDialFailed, err)
	}

	handler := newClientHandler()
	// ACP SDK: client writes to agent's stdin, reads from agent's stdout.
	conn := acpproto.NewClientSideConnection(handler, stdinPipe, stdoutPipe)

	done := make(chan struct{})
	c := &ACPClient{
		conn:    conn,
		handler: handler,
		cmd:     cmd,
		stdin:   stdinPipe,
		stdout:  stdoutPipe,
		done:    done,
	}

	// Monitor process lifecycle.
	go func() {
		c.cmdErr = cmd.Wait()
		close(done)
	}()

	// Perform Initialize handshake.
	initCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		initCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	if _, err := conn.Initialize(initCtx, acpproto.InitializeRequest{
		ProtocolVersion: acpproto.ProtocolVersionNumber,
		ClientCapabilities: acpproto.ClientCapabilities{
			Terminal: true,
		},
	}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("%w: initialize: %v", ErrDialFailed, err)
	}

	return c, nil
}

// Run executes a full ACP session turn: NewSession -> optional SetMode -> Prompt.
func (c *ACPClient) Run(ctx context.Context, opts RunOptions) (*RunResult, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	c.mu.Unlock()

	select {
	case <-c.done:
		return nil, ErrProcessExited
	default:
	}

	cwd := opts.Cwd
	if cwd == "" {
		if c.cmd.Dir != "" {
			cwd = c.cmd.Dir
		} else {
			cwd = "."
		}
	}

	sess, err := c.conn.NewSession(ctx, acpproto.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acpproto.McpServer{},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionFailed, err)
	}

	if opts.Mode != "" {
		if _, err := c.conn.SetSessionMode(ctx, acpproto.SetSessionModeRequest{
			SessionId: sess.SessionId,
			ModeId:    acpproto.SessionModeId(opts.Mode),
		}); err != nil {
			// Non-fatal: mode may not be supported by the target agent.
			_ = err
		}
	}

	// Clear any updates from session setup before sending the prompt.
	c.handler.clearUpdates()

	resp, err := c.conn.Prompt(ctx, acpproto.PromptRequest{
		SessionId: sess.SessionId,
		Prompt:    []acpproto.ContentBlock{acpproto.TextBlock(opts.Task)},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPromptFailed, err)
	}

	updates := c.handler.updatesSnapshot()
	output := extractTextFromUpdates(updates, sess.SessionId)

	return &RunResult{
		Output:     output,
		StopReason: resp.StopReason,
		Updates:    updates,
		Usage:      resp.Usage,
	}, nil
}

// Done returns a channel that is closed when the agent process exits.
func (c *ACPClient) Done() <-chan struct{} {
	return c.done
}

// Close terminates the agent process and releases resources.
func (c *ACPClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	// Close stdin to signal EOF to the agent.
	_ = c.stdin.Close()

	// Wait briefly for graceful exit.
	select {
	case <-c.done:
		return nil
	case <-time.After(3 * time.Second):
	}

	// Force kill if still running.
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	<-c.done
	return nil
}

// ensureACPFlag appends --acp=true if no --acp flag is present.
func ensureACPFlag(args []string) []string {
	for _, a := range args {
		if strings.HasPrefix(a, "--acp") {
			return args
		}
	}
	return append(append([]string(nil), args...), "--acp=true")
}

// extractTextFromUpdates concatenates agent message text chunks from session updates.
func extractTextFromUpdates(updates []acpproto.SessionNotification, sessionID acpproto.SessionId) string {
	var sb strings.Builder
	for _, u := range updates {
		if u.SessionId != sessionID {
			continue
		}
		if u.Update.AgentMessageChunk != nil && u.Update.AgentMessageChunk.Content.Text != nil {
			sb.WriteString(u.Update.AgentMessageChunk.Content.Text.Text)
		}
	}
	return sb.String()
}

// ---------- clientHandler ----------

// clientHandler implements acpproto.Client to handle callbacks from the agent.
// It collects streaming updates and auto-approves permission requests.
type clientHandler struct {
	mu      sync.Mutex
	updates []acpproto.SessionNotification
}

func newClientHandler() *clientHandler {
	return &clientHandler{}
}

func (h *clientHandler) SessionUpdate(_ context.Context, params acpproto.SessionNotification) error {
	h.mu.Lock()
	h.updates = append(h.updates, params)
	h.mu.Unlock()
	return nil
}

func (h *clientHandler) RequestPermission(_ context.Context, params acpproto.RequestPermissionRequest) (acpproto.RequestPermissionResponse, error) {
	// Auto-approve: select the first available option.
	if len(params.Options) > 0 {
		return acpproto.RequestPermissionResponse{
			Outcome: acpproto.RequestPermissionOutcome{
				Selected: &acpproto.RequestPermissionOutcomeSelected{OptionId: params.Options[0].OptionId},
			},
		}, nil
	}
	return acpproto.RequestPermissionResponse{
		Outcome: acpproto.RequestPermissionOutcome{
			Cancelled: &acpproto.RequestPermissionOutcomeCancelled{},
		},
	}, nil
}

func (h *clientHandler) ReadTextFile(_ context.Context, _ acpproto.ReadTextFileRequest) (acpproto.ReadTextFileResponse, error) {
	return acpproto.ReadTextFileResponse{}, fmt.Errorf("acp client: ReadTextFile not supported")
}

func (h *clientHandler) WriteTextFile(_ context.Context, _ acpproto.WriteTextFileRequest) (acpproto.WriteTextFileResponse, error) {
	return acpproto.WriteTextFileResponse{}, fmt.Errorf("acp client: WriteTextFile not supported")
}

func (h *clientHandler) CreateTerminal(_ context.Context, _ acpproto.CreateTerminalRequest) (acpproto.CreateTerminalResponse, error) {
	return acpproto.CreateTerminalResponse{}, fmt.Errorf("acp client: CreateTerminal not supported")
}

func (h *clientHandler) KillTerminalCommand(_ context.Context, _ acpproto.KillTerminalCommandRequest) (acpproto.KillTerminalCommandResponse, error) {
	return acpproto.KillTerminalCommandResponse{}, fmt.Errorf("acp client: KillTerminalCommand not supported")
}

func (h *clientHandler) TerminalOutput(_ context.Context, _ acpproto.TerminalOutputRequest) (acpproto.TerminalOutputResponse, error) {
	return acpproto.TerminalOutputResponse{}, fmt.Errorf("acp client: TerminalOutput not supported")
}

func (h *clientHandler) ReleaseTerminal(_ context.Context, _ acpproto.ReleaseTerminalRequest) (acpproto.ReleaseTerminalResponse, error) {
	return acpproto.ReleaseTerminalResponse{}, fmt.Errorf("acp client: ReleaseTerminal not supported")
}

func (h *clientHandler) WaitForTerminalExit(_ context.Context, _ acpproto.WaitForTerminalExitRequest) (acpproto.WaitForTerminalExitResponse, error) {
	return acpproto.WaitForTerminalExitResponse{}, fmt.Errorf("acp client: WaitForTerminalExit not supported")
}

func (h *clientHandler) updatesSnapshot() []acpproto.SessionNotification {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]acpproto.SessionNotification(nil), h.updates...)
}

func (h *clientHandler) clearUpdates() {
	h.mu.Lock()
	h.updates = h.updates[:0]
	h.mu.Unlock()
}
