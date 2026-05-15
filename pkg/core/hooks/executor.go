package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/saker-ai/saker/pkg/core/eventmw"
	"github.com/saker-ai/saker/pkg/core/events"
)

// executor.go owns the runtime: the Executor struct, dispatch (matching,
// async/once handling), per-hook process invocation, and exit-code
// classification. Public types and configuration helpers are in
// executor_types.go; payload assembly and matcher target extraction live in
// executor_payload.go.

// Executor executes hooks by spawning shell commands with JSON stdin payloads.
type Executor struct {
	hooks   []ShellHook
	hooksMu sync.RWMutex

	mw      []eventmw.Middleware
	timeout time.Duration
	errFn   func(events.EventType, error)
	workDir string

	defaultCommand string

	// onceTracker tracks which Once hooks have already executed per session.
	// Value is a onceTrackerEntry carrying a timestamp for TTL cleanup.
	onceTracker sync.Map // key: "sessionID:hookName" -> onceTrackerEntry

	// stopCh stops the onceTracker cleanup goroutine.
	stopCh chan struct{}
}

// onceTrackerEntry records when a Once hook was first executed so stale
// entries can be swept by the background cleanup goroutine.
type onceTrackerEntry struct {
	executedAt time.Time
}

// ExecutorOption configures optional behaviour.
type ExecutorOption func(*Executor)

// WithMiddleware wraps execution with the provided middleware chain.
func WithMiddleware(mw ...eventmw.Middleware) ExecutorOption {
	return func(e *Executor) {
		e.mw = append(e.mw, mw...)
	}
}

// WithTimeout sets the default timeout per hook run. Zero uses the default budget.
func WithTimeout(d time.Duration) ExecutorOption {
	return func(e *Executor) {
		e.timeout = d
	}
}

// WithErrorHandler installs an async error sink. Errors are still returned to callers.
func WithErrorHandler(fn func(events.EventType, error)) ExecutorOption {
	return func(e *Executor) {
		e.errFn = fn
	}
}

// WithCommand defines the fallback shell command used when a hook omits Command.
func WithCommand(cmd string) ExecutorOption {
	return func(e *Executor) {
		e.defaultCommand = strings.TrimSpace(cmd)
	}
}

// WithWorkDir sets the working directory for hook command execution.
func WithWorkDir(dir string) ExecutorOption {
	return func(e *Executor) {
		e.workDir = dir
	}
}

// NewExecutor constructs a shell-based hook executor.
func NewExecutor(opts ...ExecutorOption) *Executor {
	exe := &Executor{timeout: defaultHookTimeout, errFn: func(events.EventType, error) {}, stopCh: make(chan struct{})}
	for _, opt := range opts {
		opt(exe)
	}
	if exe.timeout <= 0 {
		exe.timeout = defaultHookTimeout
	}
	go exe.onceTrackerCleanupLoop()
	return exe
}

// Register adds shell hooks to the executor. Hooks are matched by event type and selector.
func (e *Executor) Register(hooks ...ShellHook) {
	e.hooksMu.Lock()
	defer e.hooksMu.Unlock()
	e.hooks = append(e.hooks, hooks...)
}

// Publish executes matching hooks for the event using a background context.
// It preserves the previous API while delegating to Execute.
func (e *Executor) Publish(evt events.Event) error {
	_, err := e.Execute(context.Background(), evt)
	return err
}

// Execute runs all matching hooks for the provided event and returns their results.
func (e *Executor) Execute(ctx context.Context, evt events.Event) ([]Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateEvent(evt.Type); err != nil {
		return nil, err
	}

	var results []Result
	handler := eventmw.Chain(func(c context.Context, ev events.Event) error {
		var err error
		results, err = e.runHooks(c, ev)
		return err
	}, e.mw...)

	if err := handler(ctx, evt); err != nil {
		e.report(evt.Type, err)
		return nil, err
	}
	return results, nil
}

// Close stops the background onceTracker cleanup goroutine.
func (e *Executor) Close() {
	if e.stopCh != nil {
		close(e.stopCh)
	}
}

// onceTrackerCleanupLoop periodically removes stale entries from the
// onceTracker sync.Map (entries older than 24 hours since execution).
func (e *Executor) onceTrackerCleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
			now := time.Now()
			cutoff := now.Add(-24 * time.Hour)
			e.onceTracker.Range(func(key, value any) bool {
				entry, ok := value.(onceTrackerEntry)
				if ok && entry.executedAt.Before(cutoff) {
					e.onceTracker.Delete(key)
				}
				return true
			})
		}
	}
}

func (e *Executor) runHooks(ctx context.Context, evt events.Event) ([]Result, error) {
	hooks := e.matchingHooks(evt)
	if len(hooks) == 0 {
		return nil, nil
	}

	payload, err := buildPayload(evt)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(hooks))
	for _, hook := range hooks {
		// Check Once constraint. Use hook name as key; fall back to command string.
		if hook.Once {
			onceKey := hook.Name
			if onceKey == "" {
				onceKey = hook.Command
			}
			if onceKey != "" {
				key := evt.SessionID + ":" + onceKey
				if _, loaded := e.onceTracker.LoadOrStore(key, onceTrackerEntry{executedAt: time.Now()}); loaded {
					continue
				}
			}
		}

		// Async hooks: fire-and-forget with bounded timeout
		if hook.Async {
			asyncTimeout := effectiveTimeout(hook.Timeout, e.timeout)
			asyncCtx, asyncCancel := context.WithTimeout(context.WithoutCancel(ctx), asyncTimeout)
			go func(h ShellHook, p []byte, ev events.Event, cancel context.CancelFunc) {
				defer cancel()
				_, err := e.executeHook(asyncCtx, h, p, ev)
				if err != nil {
					e.report(ev.Type, err)
				}
			}(hook, payload, evt, asyncCancel)
			continue
		}

		res, err := e.executeHook(ctx, hook, payload, evt)
		if err != nil {
			e.report(evt.Type, err)
			return nil, err
		}
		results = append(results, res)
	}
	return results, nil
}

func (e *Executor) matchingHooks(evt events.Event) []ShellHook {
	e.hooksMu.RLock()
	defer e.hooksMu.RUnlock()

	var matches []ShellHook
	for _, hook := range e.hooks {
		if hook.Event != evt.Type {
			continue
		}
		if hook.Selector.Match(evt) {
			matches = append(matches, hook)
		}
	}

	// Fallback: single default command bound to all events.
	if len(matches) == 0 && strings.TrimSpace(e.defaultCommand) != "" {
		matches = append(matches, ShellHook{Event: evt.Type, Command: e.defaultCommand})
	}
	return matches
}

func (e *Executor) executeHook(ctx context.Context, hook ShellHook, payload []byte, evt events.Event) (Result, error) {
	var res Result
	res.Event = evt

	cmdStr := strings.TrimSpace(hook.Command)
	if cmdStr == "" {
		cmdStr = e.defaultCommand
	}
	if cmdStr == "" {
		return res, errors.New("hooks: missing command")
	}

	deadline := effectiveTimeout(hook.Timeout, e.timeout)
	runCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	cmd := newShellCommand(runCtx, cmdStr)
	cmd.Env = mergeEnv(os.Environ(), hook.Env)
	if e.workDir != "" {
		cmd.Dir = e.workDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = bytes.NewReader(payload)

	err := cmd.Run()
	outStr := stdout.String()
	errStr := stderr.String()

	// Handle context timeout explicitly.
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		if cmd.Process != nil {
			// nolint:errcheck // Process cleanup, error not actionable
			cmd.Process.Kill()
		}
		return res, fmt.Errorf("hooks: command timed out after %s: %s", deadline, errStr)
	}

	decision, exitCode := classifyExit(err)
	res.Decision = decision
	res.ExitCode = exitCode
	res.Stdout = outStr
	res.Stderr = errStr

	switch decision {
	case DecisionAllow:
		// Exit 0: parse JSON stdout if present
		if trimmed := strings.TrimSpace(outStr); trimmed != "" {
			output, parseErr := decodeHookOutput(trimmed)
			if parseErr != nil {
				return res, parseErr
			}
			res.Output = output
		}
	case DecisionBlockingError:
		// Exit 2: blocking error, stderr is the error message
		return res, fmt.Errorf("hooks: blocking error: %s", errStr)
	case DecisionNonBlocking:
		// Exit 1, 3+: non-blocking, log stderr and continue
		if errStr != "" {
			e.report(evt.Type, fmt.Errorf("hooks: non-blocking (exit %d): %s", exitCode, errStr))
		}
	}

	return res, nil
}

func effectiveTimeout(hookTimeout, defaultTimeout time.Duration) time.Duration {
	if hookTimeout > 0 {
		return hookTimeout
	}
	if defaultTimeout > 0 {
		return defaultTimeout
	}
	return defaultHookTimeout
}

// classifyExit maps process exit codes to Decision per Claude Code spec.
// 0 = success (parse JSON), 2 = blocking error, other = non-blocking.
func classifyExit(runErr error) (Decision, int) {
	if runErr == nil {
		return DecisionAllow, 0
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		code := exitErr.ExitCode()
		switch code {
		case 0:
			return DecisionAllow, code
		case 2:
			return DecisionBlockingError, code
		default:
			return DecisionNonBlocking, code
		}
	}
	// Non-exit errors (e.g., command not found) are blocking.
	return DecisionBlockingError, -1
}

func decodeHookOutput(out string) (*HookOutput, error) {
	var parsed HookOutput
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil, fmt.Errorf("hooks: decode hook output: %w", err)
	}
	return &parsed, nil
}

func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	env := append([]string(nil), base...)
	for k, v := range extra {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}

func newShellCommand(ctx context.Context, command string) *exec.Cmd {
	trimmed := strings.TrimSpace(command)
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", trimmed)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", trimmed)
}

func (e *Executor) report(t events.EventType, err error) {
	if e.errFn != nil && err != nil {
		e.errFn(t, err)
	}
}
