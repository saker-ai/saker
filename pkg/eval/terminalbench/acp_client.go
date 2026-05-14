// acp_client.go: ACP client handler that routes tool callbacks to a Docker container.
//
// When the TB2 runner operates in ACP mode, Saker runs as a full Runtime
// (middleware, compaction, hooks, prompt-cache) and delegates tool execution
// back to the client via the ACP protocol. This handler receives those
// callbacks and forwards them to the per-task Docker ExecutionEnvironment.
package terminalbench

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	acpproto "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

// dockerACPClient implements acpproto.Client, routing ACP tool callbacks
// (ReadTextFile, WriteTextFile, CreateTerminal, …) into a Docker container
// managed by the TB2 runner.
type dockerACPClient struct {
	env     sandboxenv.ExecutionEnvironment
	ps      *sandboxenv.PreparedSession
	timeout time.Duration
	taskCtx context.Context // long-lived context for command execution

	mu        sync.Mutex
	updates   []acpproto.SessionNotification
	terminals map[string]*dockerTerminal
}

type dockerTerminal struct {
	done   chan struct{}
	result *sandboxenv.CommandResult
	err    error
}

func newDockerACPClient(ctx context.Context, env sandboxenv.ExecutionEnvironment, ps *sandboxenv.PreparedSession, timeout time.Duration) *dockerACPClient {
	return &dockerACPClient{
		env:       env,
		ps:        ps,
		timeout:   timeout,
		taskCtx:   ctx,
		terminals: make(map[string]*dockerTerminal),
	}
}

// --- Session updates (streaming) ---

func (c *dockerACPClient) SessionUpdate(_ context.Context, params acpproto.SessionNotification) error {
	c.mu.Lock()
	c.updates = append(c.updates, params)
	c.mu.Unlock()
	return nil
}

func (c *dockerACPClient) updatesSnapshot() []acpproto.SessionNotification {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acpproto.SessionNotification(nil), c.updates...)
}

func (c *dockerACPClient) clearUpdates() {
	c.mu.Lock()
	c.updates = c.updates[:0]
	c.mu.Unlock()
}

// --- Permission ---

func (c *dockerACPClient) RequestPermission(_ context.Context, params acpproto.RequestPermissionRequest) (acpproto.RequestPermissionResponse, error) {
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

// --- Filesystem callbacks → Docker ---

func (c *dockerACPClient) ReadTextFile(_ context.Context, params acpproto.ReadTextFileRequest) (acpproto.ReadTextFileResponse, error) {
	data, err := c.env.ReadFile(c.taskCtx, c.ps, params.Path)
	if err != nil {
		return acpproto.ReadTextFileResponse{}, fmt.Errorf("docker read %s: %w", params.Path, err)
	}
	content := string(data)
	if params.Line != nil || params.Limit != nil {
		content = sliceLines(content, params.Line, params.Limit)
	}
	return acpproto.ReadTextFileResponse{Content: content}, nil
}

func (c *dockerACPClient) WriteTextFile(_ context.Context, params acpproto.WriteTextFileRequest) (acpproto.WriteTextFileResponse, error) {
	if err := c.env.WriteFile(c.taskCtx, c.ps, params.Path, []byte(params.Content)); err != nil {
		return acpproto.WriteTextFileResponse{}, fmt.Errorf("docker write %s: %w", params.Path, err)
	}
	return acpproto.WriteTextFileResponse{}, nil
}

// --- Terminal callbacks → Docker ---

func (c *dockerACPClient) CreateTerminal(_ context.Context, params acpproto.CreateTerminalRequest) (acpproto.CreateTerminalResponse, error) {
	id := uuid.NewString()
	term := &dockerTerminal{done: make(chan struct{})}

	cmdParts := []string{params.Command}
	cmdParts = append(cmdParts, params.Args...)
	cmdStr := strings.Join(cmdParts, " ")

	timeout := c.timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}

	c.mu.Lock()
	c.terminals[id] = term
	c.mu.Unlock()

	go func() {
		defer close(term.done)
		cmdCtx, cancel := context.WithTimeout(c.taskCtx, timeout)
		defer cancel()
		term.result, term.err = c.env.RunCommand(cmdCtx, c.ps, sandboxenv.CommandRequest{
			Command: cmdStr,
			Timeout: timeout,
		})
	}()

	return acpproto.CreateTerminalResponse{TerminalId: id}, nil
}

func (c *dockerACPClient) WaitForTerminalExit(ctx context.Context, params acpproto.WaitForTerminalExitRequest) (acpproto.WaitForTerminalExitResponse, error) {
	c.mu.Lock()
	term, ok := c.terminals[params.TerminalId]
	c.mu.Unlock()
	if !ok {
		return acpproto.WaitForTerminalExitResponse{}, fmt.Errorf("unknown terminal %s", params.TerminalId)
	}

	select {
	case <-term.done:
	case <-ctx.Done():
		return acpproto.WaitForTerminalExitResponse{}, ctx.Err()
	}

	exitCode := 0
	if term.result != nil {
		exitCode = term.result.ExitCode
	}
	if term.err != nil && exitCode == 0 {
		exitCode = 1
	}
	return acpproto.WaitForTerminalExitResponse{ExitCode: &exitCode}, nil
}

func (c *dockerACPClient) TerminalOutput(_ context.Context, params acpproto.TerminalOutputRequest) (acpproto.TerminalOutputResponse, error) {
	c.mu.Lock()
	term, ok := c.terminals[params.TerminalId]
	c.mu.Unlock()
	if !ok {
		return acpproto.TerminalOutputResponse{}, fmt.Errorf("unknown terminal %s", params.TerminalId)
	}

	select {
	case <-term.done:
	default:
		return acpproto.TerminalOutputResponse{Output: "(command still running)"}, nil
	}

	output := ""
	if term.result != nil {
		output = term.result.Stdout + term.result.Stderr
	}
	if term.err != nil && output == "" {
		output = term.err.Error()
	}
	return acpproto.TerminalOutputResponse{Output: output}, nil
}

func (c *dockerACPClient) KillTerminalCommand(_ context.Context, _ acpproto.KillTerminalCommandRequest) (acpproto.KillTerminalCommandResponse, error) {
	return acpproto.KillTerminalCommandResponse{}, nil
}

func (c *dockerACPClient) ReleaseTerminal(_ context.Context, params acpproto.ReleaseTerminalRequest) (acpproto.ReleaseTerminalResponse, error) {
	c.mu.Lock()
	delete(c.terminals, params.TerminalId)
	c.mu.Unlock()
	return acpproto.ReleaseTerminalResponse{}, nil
}

// sliceLines extracts a subset of lines from content, supporting optional
// 1-based offset and limit (matching the ACP ReadTextFile semantics).
func sliceLines(content string, linePtr, limitPtr *int) string {
	lines := strings.Split(content, "\n")
	start := 0
	if linePtr != nil && *linePtr > 1 {
		start = *linePtr - 1
	}
	if start >= len(lines) {
		return ""
	}
	end := len(lines)
	if limitPtr != nil && *limitPtr > 0 {
		if start+*limitPtr < end {
			end = start + *limitPtr
		}
	}
	return strings.Join(lines[start:end], "\n")
}
