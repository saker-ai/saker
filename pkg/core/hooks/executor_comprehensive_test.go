package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/saker-ai/saker/pkg/core/eventmw"
	"github.com/saker-ai/saker/pkg/core/events"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 1. Executor creation with options
// ---------------------------------------------------------------------------

func TestNewExecutorAppliesAllOptions(t *testing.T) {
	t.Parallel()

	var reportedType events.EventType
	var reportedErr error
	mwCalled := false
	mw := func(next eventmw.Handler) eventmw.Handler {
		return func(ctx context.Context, evt events.Event) error {
			mwCalled = true
			return next(ctx, evt)
		}
	}

	exec := NewExecutor(
		WithTimeout(5*time.Second),
		WithCommand("echo hello"),
		WithMiddleware(mw),
		WithErrorHandler(func(et events.EventType, err error) {
			reportedType = et
			reportedErr = err
		}),
		WithWorkDir("/tmp"),
	)

	require.Equal(t, 5*time.Second, exec.timeout, "WithTimeout should set timeout")
	require.Equal(t, "echo hello", exec.defaultCommand, "WithCommand should set default command")
	require.Len(t, exec.mw, 1, "WithMiddleware should add one middleware")
	require.Equal(t, "/tmp", exec.workDir, "WithWorkDir should set working directory")

	// Verify middleware and error handler work end-to-end
	dir := t.TempDir()
	script := writeScript(t, dir, "fail2.sh", shScript(
		"#!/bin/sh\necho blocked >&2; exit 2\n",
		"@echo blocked >&2\r\n@exit /b 2\r\n",
	))
	exec.Register(ShellHook{Event: events.Notification, Command: script})
	_, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.Error(t, err, "blocking exit 2 should return error")
	require.True(t, mwCalled, "middleware should have been invoked")
	require.Equal(t, events.Notification, reportedType, "error handler should receive event type")
	require.Error(t, reportedErr, "error handler should receive the error")
}

func TestNewExecutorNegativeTimeoutFallsBackToDefault(t *testing.T) {
	t.Parallel()
	exec := NewExecutor(WithTimeout(-1 * time.Second))
	require.Equal(t, defaultHookTimeout, exec.timeout,
		"negative timeout should fall back to defaultHookTimeout")
}

func TestNewExecutorCustomTimeoutPreserved(t *testing.T) {
	t.Parallel()
	custom := 42 * time.Second
	exec := NewExecutor(WithTimeout(custom))
	require.Equal(t, custom, exec.timeout, "custom timeout should be preserved")
}

func TestNewExecutorDefaultErrorHandlerIsNoOp(t *testing.T) {
	t.Parallel()
	exec := NewExecutor()
	// The default errFn should not panic when called with nil error
	exec.report(events.Notification, nil)
	// Calling with a real error should also not panic
	exec.report(events.Notification, fmt.Errorf("test error"))
}

// ---------------------------------------------------------------------------
// 2. Hook registration and matching
// ---------------------------------------------------------------------------

func TestRegisterMultipleHooksSameEventAllExecute(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	marker1 := filepath.Join(dir, "hook1_marker")
	marker2 := filepath.Join(dir, "hook2_marker")

	script1 := writeScript(t, dir, "hook1.sh", shScript(
		fmt.Sprintf("#!/bin/sh\necho hook1 > %q\n", marker1),
		fmt.Sprintf("@echo hook1 > \"%s\"\r\n", marker1),
	))
	script2 := writeScript(t, dir, "hook2.sh", shScript(
		fmt.Sprintf("#!/bin/sh\necho hook2 > %q\n", marker2),
		fmt.Sprintf("@echo hook2 > \"%s\"\r\n", marker2),
	))

	exec := NewExecutor(WithWorkDir(dir))
	exec.Register(
		ShellHook{Event: events.Notification, Command: script1, Name: "hook-1"},
		ShellHook{Event: events.Notification, Command: script2, Name: "hook-2"},
	)

	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.NoError(t, err)
	require.Len(t, results, 2, "both hooks should produce results")

	data1, err := os.ReadFile(marker1)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(string(data1)), "hook1")

	data2, err := os.ReadFile(marker2)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(string(data2)), "hook2")
}

func TestRegisterHooksDifferentEventsOnlyMatchOwnType(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	notifMarker := filepath.Join(dir, "notif_marker")
	stopMarker := filepath.Join(dir, "stop_marker")

	notifScript := writeScript(t, dir, "notif.sh", shScript(
		fmt.Sprintf("#!/bin/sh\necho notif > %q\n", notifMarker),
		fmt.Sprintf("@echo notif > \"%s\"\r\n", notifMarker),
	))
	stopScript := writeScript(t, dir, "stop.sh", shScript(
		fmt.Sprintf("#!/bin/sh\necho stop > %q\n", stopMarker),
		fmt.Sprintf("@echo stop > \"%s\"\r\n", stopMarker),
	))

	exec := NewExecutor(WithWorkDir(dir))
	exec.Register(
		ShellHook{Event: events.Notification, Command: notifScript},
		ShellHook{Event: events.Stop, Command: stopScript},
	)

	// Execute Notification -- only notifScript should run
	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.NoError(t, err)
	require.Len(t, results, 1, "only notification hook should match")

	data, err := os.ReadFile(notifMarker)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(string(data)), "notif")

	// stop_marker should not exist
	_, err = os.ReadFile(stopMarker)
	require.Error(t, err, "stop hook should not have executed for Notification event")
}

func TestMatchingHooksNoMatchReturnsNilResults(t *testing.T) {
	t.Parallel()
	exec := NewExecutor()
	exec.Register(ShellHook{Event: events.Stop, Command: shCmd("exit 0", "@exit /b 0")})

	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.NoError(t, err)
	require.Nil(t, results, "no matching hooks should return nil results")
}

func TestSelectorCombinedToolAndPayloadPattern(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "sel.sh", shScript(
		"#!/bin/sh\nexit 0\n",
		"@exit /b 0\r\n",
	))

	// Selector that requires tool_name=Bash AND payload contains "ls"
	sel, err := NewSelector("^bash$", `"command":"ls"`)
	require.NoError(t, err)

	exec := NewExecutor()
	exec.Register(ShellHook{Event: events.PreToolUse, Command: script, Selector: sel})

	// Should match: tool=Bash, payload has command=ls
	results, err := exec.Execute(context.Background(), events.Event{
		Type:    events.PreToolUse,
		Payload: events.ToolUsePayload{Name: "bash", Params: map[string]any{"command": "ls"}},
	})
	require.NoError(t, err)
	require.Len(t, results, 1, "selector with both tool and payload pattern should match")

	// Should NOT match: tool=Bash but payload has command=rm
	results, err = exec.Execute(context.Background(), events.Event{
		Type:    events.PreToolUse,
		Payload: events.ToolUsePayload{Name: "bash", Params: map[string]any{"command": "rm"}},
	})
	require.NoError(t, err)
	require.Len(t, results, 0, "payload pattern mismatch should not match")

	// Should NOT match: tool=Read (tool pattern mismatch)
	results, err = exec.Execute(context.Background(), events.Event{
		Type:    events.PreToolUse,
		Payload: events.ToolUsePayload{Name: "read", Params: map[string]any{"command": "ls"}},
	})
	require.NoError(t, err)
	require.Len(t, results, 0, "tool pattern mismatch should not match")
}

func TestSelectorEmptyMatchesAll(t *testing.T) {
	t.Parallel()
	sel, err := NewSelector("", "")
	require.NoError(t, err)

	// Empty selector should match any event
	evt := events.Event{
		Type:    events.PreToolUse,
		Payload: events.ToolUsePayload{Name: "bash", Params: map[string]any{}},
	}
	require.True(t, sel.Match(evt), "empty selector should match all events")
}

func TestNewSelectorInvalidRegexReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewSelector("[invalid", "")
	require.Error(t, err, "invalid tool regex should return error")
	require.Contains(t, err.Error(), "compile tool matcher")

	_, err = NewSelector("", "[invalid")
	require.Error(t, err, "invalid payload regex should return error")
	require.Contains(t, err.Error(), "compile payload matcher")
}

func TestRegisterAddsHooksInOrder(t *testing.T) {
	t.Parallel()
	exec := NewExecutor()
	h1 := ShellHook{Event: events.Notification, Command: "echo 1", Name: "first"}
	h2 := ShellHook{Event: events.Notification, Command: "echo 2", Name: "second"}
	h3 := ShellHook{Event: events.Notification, Command: "echo 3", Name: "third"}

	exec.Register(h1, h2)
	exec.Register(h3)

	exec.hooksMu.RLock()
	defer exec.hooksMu.RUnlock()
	require.Len(t, exec.hooks, 3)
	require.Equal(t, "first", exec.hooks[0].Name)
	require.Equal(t, "second", exec.hooks[1].Name)
	require.Equal(t, "third", exec.hooks[2].Name)
}

// ---------------------------------------------------------------------------
// 3. Once hooks
// ---------------------------------------------------------------------------

func TestOnceHookDifferentSessionsReExecute(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	counter := filepath.Join(dir, "counter")

	script := writeScript(t, dir, "once.sh", shScript(
		fmt.Sprintf("#!/bin/sh\necho x >> %q\n", counter),
		fmt.Sprintf("@echo x >> \"%s\"\r\n", counter),
	))

	exec := NewExecutor(WithWorkDir(dir))
	exec.Register(ShellHook{Event: events.Notification, Command: script, Once: true, Name: "once-per-session"})

	// Session 1: should execute
	_, err := exec.Execute(context.Background(), events.Event{Type: events.Notification, SessionID: "sess-1"})
	require.NoError(t, err)

	// Session 1 again: should NOT execute (already ran for this session+key)
	_, err = exec.Execute(context.Background(), events.Event{Type: events.Notification, SessionID: "sess-1"})
	require.NoError(t, err)

	// Session 2: should execute again (different session ID)
	_, err = exec.Execute(context.Background(), events.Event{Type: events.Notification, SessionID: "sess-2"})
	require.NoError(t, err)

	data, err := os.ReadFile(counter)
	require.NoError(t, err)
	lines := strings.Count(strings.TrimSpace(string(data)), "x")
	require.Equal(t, 2, lines, "once hook should execute once per session, so 2 sessions = 2 executions")
}

func TestOnceHookUsesCommandAsKeyWhenNameEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	counter := filepath.Join(dir, "counter")

	// Use the same script path as the "command" -- it becomes the once key
	script := writeScript(t, dir, "once_cmd_key.sh", shScript(
		fmt.Sprintf("#!/bin/sh\necho y >> %q\n", counter),
		fmt.Sprintf("@echo y >> \"%s\"\r\n", counter),
	))

	exec := NewExecutor(WithWorkDir(dir))
	// Once=true, Name="" -- should use Command as the key
	exec.Register(ShellHook{Event: events.Notification, Command: script, Once: true})

	sess := "sess-once-cmd"
	_, err := exec.Execute(context.Background(), events.Event{Type: events.Notification, SessionID: sess})
	require.NoError(t, err)
	_, err = exec.Execute(context.Background(), events.Event{Type: events.Notification, SessionID: sess})
	require.NoError(t, err)

	data, err := os.ReadFile(counter)
	require.NoError(t, err)
	lines := strings.Count(strings.TrimSpace(string(data)), "y")
	require.Equal(t, 1, lines, "once hook without Name should use Command as key, executing only once")
}

func TestOnceHookEmptyNameAndCommandRunsEveryTime(t *testing.T) {
	t.Parallel()
	// When both Name and Command are empty, onceKey becomes "" and the
	// tracker skips (onceKey != "" check). This means the hook runs every time.
	// But a hook with empty Command and no defaultCommand would error anyway,
	// so we test with a default command to make it runnable.
	dir := t.TempDir()
	counter := filepath.Join(dir, "counter")

	script := writeScript(t, dir, "default_once.sh", shScript(
		fmt.Sprintf("#!/bin/sh\necho z >> %q\n", counter),
		fmt.Sprintf("@echo z >> \"%s\"\r\n", counter),
	))

	exec := NewExecutor(WithCommand(script), WithWorkDir(dir))
	// Once=true, Name="", Command="" -- both empty, onceKey=""
	// With defaultCommand set, the hook is still runnable
	exec.Register(ShellHook{Event: events.Notification, Once: true})

	sess := "sess-once-empty"
	for i := 0; i < 3; i++ {
		_, err := exec.Execute(context.Background(), events.Event{Type: events.Notification, SessionID: sess})
		require.NoError(t, err)
	}

	data, err := os.ReadFile(counter)
	require.NoError(t, err)
	lines := strings.Count(strings.TrimSpace(string(data)), "z")
	// With empty onceKey, the tracker never stores, so it runs every time
	require.Equal(t, 3, lines, "once hook with empty Name and Command should run every invocation")
}

// ---------------------------------------------------------------------------
// 4. Async hooks
// ---------------------------------------------------------------------------

func TestAsyncHookErrorReportedToHandler(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "async_fail.sh", shScript(
		"#!/bin/sh\necho async_error >&2; exit 2\n",
		"@echo async_error >&2\r\n@exit /b 2\r\n",
	))

	var reportedErr atomic.Value
	exec := NewExecutor(WithWorkDir(dir),
		WithErrorHandler(func(_ events.EventType, err error) {
			reportedErr.Store(err)
		}),
	)
	exec.Register(ShellHook{Event: events.Notification, Command: script, Async: true})

	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.NoError(t, err, "async hook errors are not returned to caller")
	require.Len(t, results, 0, "async hooks do not return results")

	// Wait for async goroutine to report the error
	require.Eventually(t, func() bool {
		v := reportedErr.Load()
		return v != nil
	}, 5*time.Second, 50*time.Millisecond, "async hook error should be reported to handler")

	storedErr, ok := reportedErr.Load().(error)
	require.True(t, ok)
	require.Contains(t, storedErr.Error(), "async_error", "reported error should contain async hook stderr")
}

func TestAsyncHookCustomTimeout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A script that sleeps longer than the hook-specific timeout
	script := writeScript(t, dir, "async_slow.sh", shScript(
		"#!/bin/sh\nsleep 2\n",
		"@ping -n 3 127.0.0.1 >nul\r\n",
	))

	var timeoutErr atomic.Value
	exec := NewExecutor(WithWorkDir(dir),
		WithErrorHandler(func(_ events.EventType, err error) {
			timeoutErr.Store(err)
		}),
	)
	// Hook-level timeout of 100ms, shorter than the sleep
	exec.Register(ShellHook{
		Event:   events.Notification,
		Command: script,
		Async:   true,
		Timeout: 100 * time.Millisecond,
	})

	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.NoError(t, err)
	require.Len(t, results, 0)

	// Async timeout error should be reported
	require.Eventually(t, func() bool {
		v := timeoutErr.Load()
		if v == nil {
			return false
		}
		e, ok := v.(error)
		return ok && strings.Contains(e.Error(), "timed out")
	}, 5*time.Second, 50*time.Millisecond, "async hook timeout should be reported")
}

func TestAsyncHookPreservesValuesWithoutCancel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "async_ran")

	script := writeScript(t, dir, "async_without_cancel.sh", shScript(
		fmt.Sprintf("#!/bin/sh\ntouch %q\n", marker),
		fmt.Sprintf("@type nul > \"%s\"\r\n", marker),
	))

	exec := NewExecutor(WithWorkDir(dir))
	exec.Register(ShellHook{Event: events.Notification, Command: script, Async: true})

	// Create a context that we cancel immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Despite parent context being cancelled, async hook should still run
	// because runHooks uses context.WithoutCancel(ctx) for async hooks
	results, err := exec.Execute(ctx, events.Event{Type: events.Notification})
	require.NoError(t, err)
	require.Len(t, results, 0, "async hooks return no results synchronously")

	// The async hook should eventually create the marker file
	require.Eventually(t, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}, 5*time.Second, 50*time.Millisecond,
		"async hook should run despite parent context being cancelled (WithoutCancel)")
}

// ---------------------------------------------------------------------------
// 5. Hook execution with context cancellation
// ---------------------------------------------------------------------------

func TestSyncHookPreCancelledContextReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A script that would take a long time if it actually ran
	script := writeScript(t, dir, "cancel.sh", shScript(
		"#!/bin/sh\nsleep 30\n",
		"@ping -n 31 127.0.0.1 >nul\r\n",
	))

	exec := NewExecutor(WithWorkDir(dir))
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	// Cancel context BEFORE calling Execute -- deterministic failure
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := exec.Execute(ctx, events.Event{Type: events.Notification})
	elapsed := time.Since(start)

	// Should complete quickly (not hang for 30 seconds waiting for the script)
	require.Less(t, elapsed, 5*time.Second,
		"pre-cancelled context should not hang for full script duration")
	require.Error(t, err,
		"pre-cancelled context should return an error since command cannot start")
}

func TestExecuteNilContextDefaultsToBackground(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "nil_ctx.sh", shScript(
		"#!/bin/sh\nexit 0\n",
		"@exit /b 0\r\n",
	))

	exec := NewExecutor(WithWorkDir(dir))
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	// Pass nil context -- should default to context.Background()
	results, err := exec.Execute(nil, events.Event{Type: events.Notification})
	require.NoError(t, err)
	require.Len(t, results, 1, "nil context should be treated as background context")
}

// ---------------------------------------------------------------------------
// 6. Middleware chain
// ---------------------------------------------------------------------------

func TestMiddlewareChainOrdering(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "mw_order.sh", shScript(
		"#!/bin/sh\nexit 0\n",
		"@exit /b 0\r\n",
	))

	var order []string
	var mu sync.Mutex
	record := func(name string) {
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
	}

	mw1 := func(next eventmw.Handler) eventmw.Handler {
		return func(ctx context.Context, evt events.Event) error {
			record("mw1-before")
			err := next(ctx, evt)
			record("mw1-after")
			return err
		}
	}
	mw2 := func(next eventmw.Handler) eventmw.Handler {
		return func(ctx context.Context, evt events.Event) error {
			record("mw2-before")
			err := next(ctx, evt)
			record("mw2-after")
			return err
		}
	}

	exec := NewExecutor(WithMiddleware(mw1, mw2), WithWorkDir(dir))
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	_, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	// Chain applies middlewares in reverse order so mw1 wraps mw2 wraps handler:
	// mw1-before -> mw2-before -> handler -> mw2-after -> mw1-after
	require.Equal(t, []string{"mw1-before", "mw2-before", "mw2-after", "mw1-after"}, order,
		"middleware chain should wrap in correct order")
}

func TestMiddlewareShortCircuitReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "short_circuit.sh", shScript(
		"#!/bin/sh\nexit 0\n",
		"@exit /b 0\r\n",
	))

	shortCircuitErr := fmt.Errorf("middleware blocked execution")
	mw := func(next eventmw.Handler) eventmw.Handler {
		return func(ctx context.Context, evt events.Event) error {
			// Do NOT call next -- short-circuit
			return shortCircuitErr
		}
	}

	var reportedErr error
	exec := NewExecutor(WithMiddleware(mw), WithWorkDir(dir),
		WithErrorHandler(func(_ events.EventType, err error) {
			reportedErr = err
		}),
	)
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.Error(t, err, "short-circuit middleware should cause Execute to return error")
	require.Equal(t, shortCircuitErr, err)
	require.Nil(t, results, "short-circuit should produce no results")
	require.Equal(t, shortCircuitErr, reportedErr, "error should be reported to handler")
}

func TestMiddlewareCanModifyEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	payloadPath := filepath.Join(dir, "modified_payload.json")

	script := writeScript(t, dir, "mw_modify.sh", shScript(
		fmt.Sprintf("#!/bin/sh\ncat > %q\nexit 0\n", payloadPath),
		fmt.Sprintf("@findstr \"^\" > \"%s\"\r\n@exit /b 0\r\n", payloadPath),
	))

	// Middleware that changes the event payload
	mw := func(next eventmw.Handler) eventmw.Handler {
		return func(ctx context.Context, evt events.Event) error {
			// Replace payload with a different tool name
			evt.Payload = events.ToolUsePayload{Name: "ModifiedTool", Params: map[string]any{"key": "modified"}}
			return next(ctx, evt)
		}
	}

	exec := NewExecutor(WithMiddleware(mw), WithWorkDir(dir))
	exec.Register(ShellHook{Event: events.PreToolUse, Command: script})

	// Start with a Bash tool use payload
	_, err := exec.Execute(context.Background(), events.Event{
		Type:    events.PreToolUse,
		Payload: events.ToolUsePayload{Name: "bash", Params: map[string]any{"command": "ls"}},
	})
	require.NoError(t, err)

	// Read the payload that was sent to the hook
	raw, err := os.ReadFile(payloadPath)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "ModifiedTool", got["tool_name"],
		"middleware should be able to modify event payload before hooks see it")
}

// ---------------------------------------------------------------------------
// 7. Error reporting via reporter callback
// ---------------------------------------------------------------------------

func TestErrorHandlerCalledForBlockingExit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "block_err.sh", shScript(
		"#!/bin/sh\necho blocking_msg >&2; exit 2\n",
		"@echo blocking_msg >&2\r\n@exit /b 2\r\n",
	))

	var reportedEvents []events.EventType
	var reportedErrs []error
	var mu sync.Mutex
	exec := NewExecutor(WithWorkDir(dir),
		WithErrorHandler(func(et events.EventType, err error) {
			mu.Lock()
			reportedEvents = append(reportedEvents, et)
			reportedErrs = append(reportedErrs, err)
			mu.Unlock()
		}),
	)
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	_, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.Error(t, err)

	mu.Lock()
	defer mu.Unlock()
	// Blocking errors are reported twice: once from runHooks (executeHook returns error)
	// and once from Execute (handler returns error). Both call e.report.
	require.Len(t, reportedErrs, 2, "blocking error should be reported twice (from runHooks and Execute)")
	require.Equal(t, events.Notification, reportedEvents[0])
	require.Contains(t, reportedErrs[0].Error(), "blocking_msg")
}

func TestErrorHandlerCalledForTimeout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "timeout_report.sh", shScript(
		"#!/bin/sh\nsleep 2\n",
		"@ping -n 3 127.0.0.1 >nul\r\n",
	))

	var reportedErr error
	exec := NewExecutor(WithTimeout(100*time.Millisecond), WithWorkDir(dir),
		WithErrorHandler(func(_ events.EventType, err error) {
			reportedErr = err
		}),
	)
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	_, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.Error(t, err)
	require.Error(t, reportedErr, "timeout should be reported to error handler")
	require.Contains(t, reportedErr.Error(), "timed out")
}

func TestReportSkipsNilError(t *testing.T) {
	t.Parallel()
	var callCount atomic.Int32
	exec := NewExecutor(
		WithErrorHandler(func(_ events.EventType, err error) {
			callCount.Add(1)
		}),
	)
	// report should skip nil errors
	exec.report(events.Notification, nil)
	require.Equal(t, int32(0), callCount.Load(), "nil error should not trigger error handler")
}

func TestErrorHandlerCalledForNonBlockingStderr(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Exit 3 = non-blocking with stderr
	script := writeScript(t, dir, "nonblock_stderr.sh", shScript(
		"#!/bin/sh\necho nonblock_warning >&2; exit 3\n",
		"@echo nonblock_warning >&2\r\n@exit /b 3\r\n",
	))

	var reportedErrs []string
	var mu sync.Mutex
	exec := NewExecutor(WithWorkDir(dir),
		WithErrorHandler(func(_ events.EventType, err error) {
			mu.Lock()
			reportedErrs = append(reportedErrs, err.Error())
			mu.Unlock()
		}),
	)
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.NoError(t, err, "non-blocking should not return error to caller")
	require.Len(t, results, 1)
	require.Equal(t, DecisionNonBlocking, results[0].Decision)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, reportedErrs, 1, "non-blocking stderr should be reported to handler")
	require.Contains(t, reportedErrs[0], "nonblock_warning")
}

// ---------------------------------------------------------------------------
// 8. Concurrent Execute calls (thread safety)
// ---------------------------------------------------------------------------

func TestConcurrentRegisterAndExecute(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "conc_reg.sh", shScript(
		"#!/bin/sh\nexit 0\n",
		"@exit /b 0\r\n",
	))

	exec := NewExecutor(WithWorkDir(dir))
	// Start with one hook
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	var wg sync.WaitGroup
	errs := make([]error, 10)

	// 5 goroutines executing concurrently
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = exec.Execute(context.Background(), events.Event{Type: events.Notification})
		}(i)
	}

	// 5 goroutines registering new hooks concurrently
	for i := 5; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			exec.Register(ShellHook{Event: events.Notification, Command: script})
			errs[idx] = nil // register doesn't return error
		}(i)
	}

	wg.Wait()
	for i, e := range errs {
		require.NoErrorf(t, e, "goroutine %d should not error", i)
	}
}

func TestConcurrentOnceHooksNoDoubleExecution(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	counter := filepath.Join(dir, "concurrent_counter")

	script := writeScript(t, dir, "conc_once.sh", shScript(
		fmt.Sprintf("#!/bin/sh\necho x >> %q\n", counter),
		fmt.Sprintf("@echo x >> \"%s\"\r\n", counter),
	))

	exec := NewExecutor(WithWorkDir(dir))
	exec.Register(ShellHook{
		Event:   events.Notification,
		Command: script,
		Once:    true,
		Name:    "concurrent-once",
	})

	// Fire 10 concurrent Execute calls with the same session ID
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			exec.Execute(context.Background(), events.Event{Type: events.Notification, SessionID: "same-session"})
		}()
	}
	wg.Wait()

	// Only one execution should have happened due to once constraint
	data, err := os.ReadFile(counter)
	require.NoError(t, err)
	lines := strings.Count(strings.TrimSpace(string(data)), "x")
	require.Equal(t, 1, lines, "concurrent once hooks should execute exactly once")
}

// ---------------------------------------------------------------------------
// 9. context.WithoutCancel usage in async hooks
// ---------------------------------------------------------------------------

func TestAsyncHookContextValuesPreservedAfterParentCancel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "async_values.json")

	// Script reads stdin (payload) and writes it to a file
	script := writeScript(t, dir, "async_values.sh", shScript(
		fmt.Sprintf("#!/bin/sh\ncat > %q\n", outputPath),
		fmt.Sprintf("@findstr \"^\" > \"%s\"\r\n", outputPath),
	))

	exec := NewExecutor(WithWorkDir(dir))
	exec.Register(ShellHook{Event: events.Notification, Command: script, Async: true})

	// Create context with a value and then cancel it
	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("test-key"), "test-value")
	ctx, cancel := context.WithCancel(ctx)
	cancel() // cancel immediately

	// Execute with cancelled context -- async hook should still run
	// because context.WithoutCancel detaches from parent cancellation
	evt := events.Event{
		Type:      events.Notification,
		SessionID: "async-values-session",
		Payload:   events.NotificationPayload{Message: "async-test", NotificationType: "test"},
	}
	results, err := exec.Execute(ctx, evt)
	require.NoError(t, err)
	require.Len(t, results, 0, "async hooks return no results synchronously")

	// Wait for async hook to complete and verify the payload was delivered
	require.Eventually(t, func() bool {
		_, err := os.Stat(outputPath)
		return err == nil
	}, 5*time.Second, 50*time.Millisecond, "async hook should complete despite parent context cancellation")

	raw, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, "async-values-session", payload["session_id"],
		"async hook payload should preserve session_id from event")
	require.Equal(t, "async-test", payload["message"],
		"async hook payload should preserve message from event")
}

// ---------------------------------------------------------------------------
// 10. Default command fallback when no hooks match
// ---------------------------------------------------------------------------

func TestDefaultCommandNotUsedWhenHooksMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	hookMarker := filepath.Join(dir, "hook_marker")
	defaultMarker := filepath.Join(dir, "default_marker")

	hookScript := writeScript(t, dir, "hook.sh", shScript(
		fmt.Sprintf("#!/bin/sh\ntouch %q\nexit 0\n", hookMarker),
		fmt.Sprintf("@type nul > \"%s\"\r\n@exit /b 0\r\n", hookMarker),
	))
	defaultScript := writeScript(t, dir, "default.sh", shScript(
		fmt.Sprintf("#!/bin/sh\ntouch %q\nexit 0\n", defaultMarker),
		fmt.Sprintf("@type nul > \"%s\"\r\n@exit /b 0\r\n", defaultMarker),
	))

	exec := NewExecutor(WithCommand(defaultScript), WithWorkDir(dir))
	exec.Register(ShellHook{Event: events.Notification, Command: hookScript})

	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.NoError(t, err)
	require.Len(t, results, 1, "registered hook should be used, not default command")

	// Hook marker should exist, default marker should NOT
	_, err = os.Stat(hookMarker)
	require.NoError(t, err, "registered hook should have executed")
	_, err = os.Stat(defaultMarker)
	require.Error(t, err, "default command should not have been used when hooks match")
}

func TestDefaultCommandEmptyStringIgnored(t *testing.T) {
	t.Parallel()
	exec := NewExecutor(WithCommand(""))
	// Empty string default command should not be used as fallback
	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.NoError(t, err)
	require.Nil(t, results, "empty default command should not produce results")
}

func TestHookEmptyCommandFallsBackToDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	payloadPath := filepath.Join(dir, "fallback_payload.json")

	defaultScript := writeScript(t, dir, "default_fallback.sh", shScript(
		fmt.Sprintf("#!/bin/sh\ncat > %q\nexit 0\n", payloadPath),
		fmt.Sprintf("@findstr \"^\" > \"%s\"\r\n@exit /b 0\r\n", payloadPath),
	))

	exec := NewExecutor(WithCommand(defaultScript), WithWorkDir(dir))
	// Register hook with empty Command -- should fall back to default
	exec.Register(ShellHook{Event: events.Notification, Command: ""})

	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.NoError(t, err)
	require.Len(t, results, 1, "hook with empty command should use default command")

	raw, err := os.ReadFile(payloadPath)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, "Notification", payload["hook_event_name"],
		"fallback default command should receive the event payload")
}

func TestHookEmptyCommandNoDefaultReturnsError(t *testing.T) {
	t.Parallel()
	exec := NewExecutor()
	// No default command, hook with empty Command
	exec.Register(ShellHook{Event: events.Notification, Command: ""})

	_, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.Error(t, err, "hook with empty command and no default should return error")
	require.Contains(t, err.Error(), "missing command")
}

// ---------------------------------------------------------------------------
// 11. validateEvent for valid/invalid event types
// ---------------------------------------------------------------------------

func TestValidateEventTableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		eventType events.EventType
		wantErr   bool
	}{
		// All valid hook event types
		{events.PreToolUse, false},
		{events.PostToolUse, false},
		{events.PostToolUseFailure, false},
		{events.PreCompact, false},
		{events.ContextCompacted, false},
		{events.Notification, false},
		{events.UserPromptSubmit, false},
		{events.SessionStart, false},
		{events.SessionEnd, false},
		{events.Stop, false},
		{events.TokenUsage, false},
		{events.SubagentStart, false},
		{events.SubagentStop, false},
		{events.PermissionRequest, false},
		{events.ModelSelected, false},
		// Invalid event types
		{events.EventType(""), true},
		{events.EventType("BogusEvent"), true},
		{events.EventType("random_string"), true},
		// MCPToolsChanged is NOT a valid hook event
		{events.MCPToolsChanged, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.eventType), func(t *testing.T) {
			t.Parallel()
			err := validateEvent(tc.eventType)
			if tc.wantErr {
				require.Error(t, err, "event type %s should be rejected", tc.eventType)
			} else {
				require.NoError(t, err, "event type %s should be valid", tc.eventType)
			}
		})
	}
}

func TestValidateEventRejectsMCPToolsChanged(t *testing.T) {
	t.Parallel()
	err := validateEvent(events.MCPToolsChanged)
	require.Error(t, err, "MCPToolsChanged should not be a valid hook event type")
	require.Contains(t, err.Error(), "unsupported event")
}

// ---------------------------------------------------------------------------
// Additional helper coverage tests
// ---------------------------------------------------------------------------

func TestEffectiveTimeoutTableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		hook       time.Duration
		defaultDur time.Duration
		want       time.Duration
	}{
		{"hook_timeout_preferred", 5 * time.Second, 10 * time.Second, 5 * time.Second},
		{"default_used_when_hook_zero", 0, 30 * time.Second, 30 * time.Second},
		{"default_used_when_hook_negative", -1 * time.Second, 30 * time.Second, 30 * time.Second},
		{"fallback_when_both_zero", 0, 0, defaultHookTimeout},
		{"fallback_when_both_negative", -1 * time.Second, -1 * time.Second, defaultHookTimeout},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := effectiveTimeout(tc.hook, tc.defaultDur)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestMergeEnvAddsExtraVars(t *testing.T) {
	t.Parallel()
	base := []string{"PATH=/usr/bin", "HOME=/home/user"}
	extra := map[string]string{"MY_VAR": "hello", "OTHER": "world"}

	result := mergeEnv(base, extra)
	require.Len(t, result, 4)
	require.Contains(t, result, "PATH=/usr/bin")
	require.Contains(t, result, "HOME=/home/user")
	require.Contains(t, result, "MY_VAR=hello")
	require.Contains(t, result, "OTHER=world")
}

func TestMergeEnvEmptyExtraReturnsBase(t *testing.T) {
	t.Parallel()
	base := []string{"PATH=/usr/bin"}
	result := mergeEnv(base, nil)
	require.Equal(t, base, result, "empty extra should return base unchanged")
}

func TestMergeEnvEmptyBaseAndExtra(t *testing.T) {
	t.Parallel()
	result := mergeEnv(nil, map[string]string{"X": "1"})
	require.Len(t, result, 1)
	require.Contains(t, result, "X=1")
}

func TestNewShellCommandWindowsFormat(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("only relevant on Windows")
	}
	cmd := newShellCommand(context.Background(), "echo hello")
	require.Equal(t, "cmd", cmd.Args[0])
	require.Contains(t, cmd.Args, "/C")
}

func TestNewShellCommandUnixFormat(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("only relevant on Unix")
	}
	cmd := newShellCommand(context.Background(), "echo hello")
	require.Equal(t, "/bin/sh", cmd.Path)
}

func TestExecuteShellCommandNotFoundIsNonBlocking(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// /bin/sh -c "/nonexistent_command_xyz" causes the shell to exit with code 127
	// (command not found), which classifyExit maps to DecisionNonBlocking (not 0 or 2).
	script := writeScript(t, dir, "cmd_not_found.sh", shScript(
		"#!/bin/sh\n/nonexistent_command_xyz\n",
		"@echo nonexistent_command_xyz\r\n",
	))

	var reportedErr error
	exec := NewExecutor(WithWorkDir(dir),
		WithErrorHandler(func(_ events.EventType, err error) {
			reportedErr = err
		}),
	)
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	// Shell exit code 127 is DecisionNonBlocking, so no error is returned to caller
	require.NoError(t, err, "shell-level command not found (exit 127) is non-blocking, not a caller error")
	require.Len(t, results, 1)
	require.Equal(t, DecisionNonBlocking, results[0].Decision,
		"shell command-not-found should produce DecisionNonBlocking")
	// But stderr from the non-blocking exit is reported via error handler
	require.Eventually(t, func() bool {
		return reportedErr != nil && strings.Contains(reportedErr.Error(), "nonexistent")
	}, 2*time.Second, 50*time.Millisecond,
		"non-blocking stderr should be reported to error handler")
}

func TestCloseIsNoOp(t *testing.T) {
	t.Parallel()
	exec := NewExecutor()
	// Close should not panic and is a no-op
	exec.Close()
	// Verify executor still works after Close
	dir := t.TempDir()
	script := writeScript(t, dir, "after_close.sh", shScript(
		"#!/bin/sh\nexit 0\n",
		"@exit /b 0\r\n",
	))
	exec.Register(ShellHook{Event: events.Notification, Command: script})
	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	require.NoError(t, err)
	require.Len(t, results, 1, "executor should still work after Close")
}
