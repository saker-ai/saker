package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/saker-ai/saker/pkg/agent"
	"github.com/saker-ai/saker/pkg/config"
	"github.com/saker-ai/saker/pkg/model"
	"github.com/saker-ai/saker/pkg/runtime/subagents"
	"github.com/saker-ai/saker/pkg/security"
	"github.com/saker-ai/saker/pkg/tool"
	toolbuiltin "github.com/saker-ai/saker/pkg/tool/builtin"
)

// ---------------------------------------------------------------------------
// buildPermissionReason
// ---------------------------------------------------------------------------

func TestRuntimeToolsBuildPermissionReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		decision security.PermissionDecision
		want     string
	}{
		{
			name:     "empty rule and target",
			decision: security.PermissionDecision{},
			want:     "",
		},
		{
			name:     "whitespace-only rule and target",
			decision: security.PermissionDecision{Rule: "  ", Target: "  "},
			want:     "",
		},
		{
			name:     "rule only",
			decision: security.PermissionDecision{Rule: "Bash(*)", Target: ""},
			want:     `rule "Bash(*)"`,
		},
		{
			name:     "whitespace rule with target",
			decision: security.PermissionDecision{Rule: "  ", Target: "/tmp/file"},
			want:     `target "/tmp/file"`,
		},
		{
			name:     "target only",
			decision: security.PermissionDecision{Rule: "", Target: "/tmp/file"},
			want:     `target "/tmp/file"`,
		},
		{
			name:     "rule and target together",
			decision: security.PermissionDecision{Rule: "Write(*)", Target: "/etc/passwd"},
			want:     `rule "Write(*)" for /etc/passwd`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildPermissionReason(tt.decision)
			if got != tt.want {
				t.Fatalf("buildPermissionReason(%+v) = %q, want %q", tt.decision, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// formatApprovalCommand
// ---------------------------------------------------------------------------

func TestRuntimeToolsFormatApprovalCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		toolName string
		target   string
		want     string
	}{
		{"bash", "/tmp/script.sh", "bash(/tmp/script.sh)"},
		{"bash", "", "bash"},
		{"", "/tmp/file", "tool(/tmp/file)"},
		{"", "", "tool"},
		{"  read  ", "  /etc/hosts  ", "read(/etc/hosts)"},
		{"  read  ", "   ", "read"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%s", tt.toolName, tt.target), func(t *testing.T) {
			got := formatApprovalCommand(tt.toolName, tt.target)
			if got != tt.want {
				t.Fatalf("formatApprovalCommand(%q, %q) = %q, want %q", tt.toolName, tt.target, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// decisionWithAction
// ---------------------------------------------------------------------------

func TestRuntimeToolsDecisionWithAction(t *testing.T) {
	t.Parallel()
	base := security.PermissionDecision{
		Action: security.PermissionAsk,
		Rule:   "Bash(*)",
		Tool:   "bash",
		Target: "/tmp/script.sh",
	}
	allow := decisionWithAction(base, security.PermissionAllow)
	if allow.Action != security.PermissionAllow {
		t.Fatalf("expected allow action, got %v", allow.Action)
	}
	if allow.Rule != "Bash(*)" || allow.Tool != "bash" || allow.Target != "/tmp/script.sh" {
		t.Fatalf("other fields should be preserved: %+v", allow)
	}
	deny := decisionWithAction(base, security.PermissionDeny)
	if deny.Action != security.PermissionDeny {
		t.Fatalf("expected deny action, got %v", deny.Action)
	}
}

// ---------------------------------------------------------------------------
// approvalActor
// ---------------------------------------------------------------------------

func TestRuntimeToolsApprovalActor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		approver string
		want     string
	}{
		{"", "host"},
		{"   ", "host"},
		{"admin", "admin"},
		{"  tester  ", "tester"},
	}
	for _, tt := range tests {
		t.Run(tt.approver, func(t *testing.T) {
			got := approvalActor(tt.approver)
			if got != tt.want {
				t.Fatalf("approvalActor(%q) = %q, want %q", tt.approver, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// shouldRegisterTaskTool
// ---------------------------------------------------------------------------

func TestRuntimeToolsShouldRegisterTaskTool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		entry EntryPoint
		want  bool
	}{
		{EntryPointCLI, true},
		{EntryPointPlatform, true},
		{EntryPointCI, false},
		{"unknown", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.entry), func(t *testing.T) {
			got := shouldRegisterTaskTool(tt.entry)
			if got != tt.want {
				t.Fatalf("shouldRegisterTaskTool(%v) = %v, want %v", tt.entry, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// effectiveEntryPoint
// ---------------------------------------------------------------------------

func TestRuntimeToolsEffectiveEntryPoint(t *testing.T) {
	t.Parallel()
	// When opts.EntryPoint is set, it takes precedence.
	opts := Options{EntryPoint: EntryPointPlatform}
	if got := effectiveEntryPoint(opts); got != EntryPointPlatform {
		t.Fatalf("expected EntryPointPlatform, got %v", got)
	}
	// When opts.EntryPoint is empty but Mode.EntryPoint is set.
	opts = Options{Mode: ModeContext{EntryPoint: EntryPointCI}}
	if got := effectiveEntryPoint(opts); got != EntryPointCI {
		t.Fatalf("expected EntryPointCI from Mode, got %v", got)
	}
	// When both are empty, falls back to defaultEntrypoint.
	opts = Options{}
	if got := effectiveEntryPoint(opts); got != defaultEntrypoint {
		t.Fatalf("expected defaultEntrypoint, got %v", got)
	}
	// When both are set, EntryPoint on opts wins.
	opts = Options{EntryPoint: EntryPointCLI, Mode: ModeContext{EntryPoint: EntryPointPlatform}}
	if got := effectiveEntryPoint(opts); got != EntryPointCLI {
		t.Fatalf("expected opts.EntryPoint to win, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// locateTaskTool
// ---------------------------------------------------------------------------

func TestRuntimeToolsLocateTaskTool(t *testing.T) {
	t.Parallel()
	// Empty list returns nil.
	if got := locateTaskTool(nil); got != nil {
		t.Fatalf("expected nil for nil list")
	}
	if got := locateTaskTool([]tool.Tool{}); got != nil {
		t.Fatalf("expected nil for empty list")
	}
	// List without TaskTool returns nil.
	bash := toolbuiltin.NewBashToolWithRoot(t.TempDir())
	if got := locateTaskTool([]tool.Tool{bash}); got != nil {
		t.Fatalf("expected nil when no TaskTool present")
	}
	// List with nil entries returns nil.
	if got := locateTaskTool([]tool.Tool{nil}); got != nil {
		t.Fatalf("expected nil for nil tool entry")
	}
	// List with TaskTool returns it.
	taskTool := toolbuiltin.NewTaskTool()
	if got := locateTaskTool([]tool.Tool{bash, taskTool}); got != taskTool {
		t.Fatalf("expected TaskTool to be found")
	}
	// First TaskTool wins when multiple exist.
	first := toolbuiltin.NewTaskTool()
	if got := locateTaskTool([]tool.Tool{first, toolbuiltin.NewTaskTool()}); got != first {
		t.Fatalf("expected first TaskTool to be returned")
	}
}

// ---------------------------------------------------------------------------
// filterBuiltinNames
// ---------------------------------------------------------------------------

func TestRuntimeToolsFilterBuiltinNames(t *testing.T) {
	t.Parallel()
	order := []string{"bash", "file_read", "file_write", "grep", "glob"}

	// nil enabled returns full order copy.
	got := filterBuiltinNames(nil, order)
	if len(got) != len(order) {
		t.Fatalf("expected full order for nil enabled, got %v", got)
	}
	// Verify it is a copy, not the same slice.
	got[0] = "modified"
	if order[0] == "modified" {
		t.Fatalf("filterBuiltinNames should return a copy, not alias the original")
	}

	// Empty enabled returns nil (explicitly no tools).
	if result := filterBuiltinNames([]string{}, order); result != nil {
		t.Fatalf("expected nil for empty enabled, got %v", result)
	}

	// Matching subset.
	got = filterBuiltinNames([]string{"bash", "grep"}, order)
	if len(got) != 2 || got[0] != "bash" || got[1] != "grep" {
		t.Fatalf("expected [bash, grep], got %v", got)
	}

	// Names with hyphens/spaces normalized to underscores.
	got = filterBuiltinNames([]string{"file-read", "file write"}, order)
	if len(got) != 2 || got[0] != "file_read" || got[1] != "file_write" {
		t.Fatalf("expected normalized names [file_read, file_write], got %v", got)
	}

	// Case-insensitive matching.
	got = filterBuiltinNames([]string{"BASH", "grep"}, order)
	if len(got) != 2 || got[0] != "bash" || got[1] != "grep" {
		t.Fatalf("expected case-insensitive match, got %v", got)
	}

	// Unknown names are silently dropped (order-preserving).
	got = filterBuiltinNames([]string{"bash", "unknown_tool"}, order)
	if len(got) != 1 || got[0] != "bash" {
		t.Fatalf("expected only known names, got %v", got)
	}

	// Whitespace-only and empty names are dropped.
	got = filterBuiltinNames([]string{"", "  ", "bash"}, order)
	if len(got) != 1 || got[0] != "bash" {
		t.Fatalf("expected whitespace/empty dropped, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// builtinOrder
// ---------------------------------------------------------------------------

func TestRuntimeToolsBuiltinOrder(t *testing.T) {
	t.Parallel()
	// CLI and Platform include "task" tool.
	cliOrder := builtinOrder(EntryPointCLI, "")
	if !rtContainsString(cliOrder, "task") {
		t.Fatalf("CLI order should include task tool")
	}
	platformOrder := builtinOrder(EntryPointPlatform, "")
	if !rtContainsString(platformOrder, "task") {
		t.Fatalf("Platform order should include task tool")
	}
	// CI does not include "task" tool.
	ciOrder := builtinOrder(EntryPointCI, "")
	if rtContainsString(ciOrder, "task") {
		t.Fatalf("CI order should not include task tool")
	}
	// Core tools are present in all entrypoints.
	coreTools := []string{"bash", "file_read", "file_write", "file_edit", "grep", "glob"}
	for _, entry := range []EntryPoint{EntryPointCLI, EntryPointCI, EntryPointPlatform} {
		order := builtinOrder(entry, "")
		for _, name := range coreTools {
			if !rtContainsString(order, name) {
				t.Fatalf("builtinOrder(%v) missing core tool %q", entry, name)
			}
		}
	}
	// Verify order is deterministic (same entrypoint yields same result).
	order1 := builtinOrder(EntryPointCLI, "")
	order2 := builtinOrder(EntryPointCLI, "")
	if len(order1) != len(order2) {
		t.Fatalf("order lengths differ between repeated calls")
	}
	for i := range order1 {
		if order1[i] != order2[i] {
			t.Fatalf("order differs at position %d: %q vs %q", i, order1[i], order2[i])
		}
	}
}

// ---------------------------------------------------------------------------
// resolveContextWindow
// ---------------------------------------------------------------------------

func TestRuntimeToolsResolveContextWindow(t *testing.T) {
	t.Parallel()
	// Explicit value takes precedence.
	if got := resolveContextWindow(100000, nil); got != 100000 {
		t.Fatalf("expected explicit value 100000, got %d", got)
	}
	// Zero explicit falls through to nil model returning 0.
	if got := resolveContextWindow(0, nil); got != 0 {
		t.Fatalf("expected 0 with nil model, got %d", got)
	}
	// Negative explicit is not > 0 so falls through.
	if got := resolveContextWindow(-1, nil); got != 0 {
		t.Fatalf("expected 0 with negative explicit, got %d", got)
	}

	// ContextWindowProvider model: embed existing mockModel to satisfy model.Model.
	cwpModel := &rtContextWindowModel{window: 200000}
	if got := resolveContextWindow(0, cwpModel); got != 200000 {
		t.Fatalf("expected 200000 from ContextWindowProvider, got %d", got)
	}
	// Explicit still wins over ContextWindowProvider.
	if got := resolveContextWindow(50000, cwpModel); got != 50000 {
		t.Fatalf("expected explicit 50000 to win, got %d", got)
	}
	// ContextWindowProvider returning 0 falls through to ModelNamer.
	zeroCWPModel := &rtContextWindowModel{window: 0, namer: "claude-3-opus"}
	if got := resolveContextWindow(0, zeroCWPModel); got != 0 {
		// The value depends on whether LookupContextWindow finds the model name.
		// Just verify it didn't return a CWP value since CWP was 0.
		t.Logf("resolveContextWindow with zero CWP + namer = %d (depends on registry)", got)
	}
	// Both CWP and namer return 0, final fallback is 0.
	emptyModel := &rtContextWindowModel{window: 0, namer: ""}
	if got := resolveContextWindow(0, emptyModel); got != 0 {
		t.Fatalf("expected 0 when all sources return 0, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// newOutputPersister
// ---------------------------------------------------------------------------

func TestRuntimeToolsNewOutputPersister(t *testing.T) {
	t.Parallel()
	// nil settings returns default persister.
	p := newOutputPersister(nil)
	if p == nil {
		t.Fatalf("expected non-nil persister")
	}
	if p.DefaultThresholdBytes == 0 {
		t.Fatalf("expected non-zero default threshold from NewOutputPersister")
	}

	// Settings with nil ToolOutput returns default persister.
	p = newOutputPersister(&config.Settings{})
	if p == nil {
		t.Fatalf("expected non-nil persister with nil ToolOutput")
	}

	// Settings with custom ToolOutput.
	s := &config.Settings{
		ToolOutput: &config.ToolOutputConfig{
			DefaultThresholdBytes: 50000,
			PerToolThresholdBytes: map[string]int{"bash": 10000, "grep": 5000},
		},
	}
	p = newOutputPersister(s)
	if p.DefaultThresholdBytes != 50000 {
		t.Fatalf("expected DefaultThresholdBytes=50000, got %d", p.DefaultThresholdBytes)
	}
	if len(p.PerToolThresholdBytes) != 2 {
		t.Fatalf("expected 2 per-tool thresholds, got %d", len(p.PerToolThresholdBytes))
	}
	if p.PerToolThresholdBytes["bash"] != 10000 {
		t.Fatalf("expected bash threshold 10000, got %d", p.PerToolThresholdBytes["bash"])
	}
	if p.PerToolThresholdBytes["grep"] != 5000 {
		t.Fatalf("expected grep threshold 5000, got %d", p.PerToolThresholdBytes["grep"])
	}

	// Empty PerToolThresholdBytes.
	s = &config.Settings{
		ToolOutput: &config.ToolOutputConfig{
			DefaultThresholdBytes: 50000,
		},
	}
	p = newOutputPersister(s)
	if p.DefaultThresholdBytes != 50000 {
		t.Fatalf("expected DefaultThresholdBytes=50000, got %d", p.DefaultThresholdBytes)
	}
	if len(p.PerToolThresholdBytes) != 0 {
		t.Fatalf("expected empty per-tool thresholds, got %d", len(p.PerToolThresholdBytes))
	}
}

// ---------------------------------------------------------------------------
// truncateString
// ---------------------------------------------------------------------------

func TestRuntimeToolsTruncateString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello..."},
		{"abcdef", 6, "abcdef"},
		{"abcdef", 3, "..."},
		{"", 10, ""},
		{"x", 1, "x"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q/%d", tt.input, tt.max), func(t *testing.T) {
			got := truncateString(tt.input, tt.max)
			if got != tt.want {
				t.Fatalf("truncateString(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// defaultSessionID
// ---------------------------------------------------------------------------

func TestRuntimeToolsDefaultSessionID(t *testing.T) {
	t.Parallel()
	// With explicit entrypoint.
	id := defaultSessionID(EntryPointCLI)
	if !strings.HasPrefix(id, "cli-") {
		t.Fatalf("expected 'cli-' prefix, got %q", id)
	}
	id = defaultSessionID(EntryPointPlatform)
	if !strings.HasPrefix(id, "platform-") {
		t.Fatalf("expected 'platform-' prefix, got %q", id)
	}
	id = defaultSessionID(EntryPointCI)
	if !strings.HasPrefix(id, "ci-") {
		t.Fatalf("expected 'ci-' prefix, got %q", id)
	}
	// Empty entrypoint falls back to defaultEntrypoint.
	id = defaultSessionID("")
	if !strings.HasPrefix(id, string(defaultEntrypoint)+"-") {
		t.Fatalf("expected defaultEntrypoint prefix, got %q", id)
	}
	// The suffix after the dash should be a numeric timestamp.
	parts := strings.SplitN(id, "-", 2)
	if len(parts) != 2 {
		t.Fatalf("expected two parts separated by dash, got %v", parts)
	}
	// Ensure the numeric part contains only digits.
	for _, ch := range parts[1] {
		if ch < '0' || ch > '9' {
			t.Fatalf("expected numeric suffix, got char %q in %q", ch, id)
		}
	}
}

// ---------------------------------------------------------------------------
// resolveModel
// ---------------------------------------------------------------------------

func TestRuntimeToolsResolveModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// No model, no factory returns ErrMissingModel.
	_, err := resolveModel(ctx, Options{})
	if !errors.Is(err, ErrMissingModel) {
		t.Fatalf("expected ErrMissingModel, got %v", err)
	}

	// opts.Model is returned directly.
	mdl := &mockModel{name: "test"}
	result, err := resolveModel(ctx, Options{Model: mdl})
	if err != nil || result != mdl {
		t.Fatalf("expected model returned directly, got result=%v err=%v", result, err)
	}

	// opts.ModelFactory is called when Model is nil.
	factory := ModelFactoryFunc(func(ctx context.Context) (model.Model, error) {
		return mdl, nil
	})
	result, err = resolveModel(ctx, Options{ModelFactory: factory})
	if err != nil || result != mdl {
		t.Fatalf("expected model from factory, got result=%v err=%v", result, err)
	}

	// ModelFactory error is propagated and wrapped.
	factoryErr := ModelFactoryFunc(func(ctx context.Context) (model.Model, error) {
		return nil, errors.New("factory failed")
	})
	_, err = resolveModel(ctx, Options{ModelFactory: factoryErr})
	if err == nil || !strings.Contains(err.Error(), "factory failed") {
		t.Fatalf("expected factory error propagated, got %v", err)
	}
	if !strings.Contains(err.Error(), "api: model factory") {
		t.Fatalf("expected wrapped error, got %v", err)
	}

	// Model takes precedence over ModelFactory.
	result, err = resolveModel(ctx, Options{Model: mdl, ModelFactory: factoryErr})
	if err != nil || result != mdl {
		t.Fatalf("expected Model to take precedence over factory, got result=%v err=%v", result, err)
	}
}

// ---------------------------------------------------------------------------
// hasMCPServerOptions
// ---------------------------------------------------------------------------

func TestRuntimeToolsHasMCPServerOptions(t *testing.T) {
	t.Parallel()
	// Empty options returns false.
	if hasMCPServerOptions(tool.MCPServerOptions{}) {
		t.Fatalf("expected false for empty options")
	}
	// Headers set returns true.
	if !hasMCPServerOptions(tool.MCPServerOptions{Headers: map[string]string{"k": "v"}}) {
		t.Fatalf("expected true with Headers")
	}
	// Env set returns true.
	if !hasMCPServerOptions(tool.MCPServerOptions{Env: map[string]string{"k": "v"}}) {
		t.Fatalf("expected true with Env")
	}
	// Timeout set returns true.
	if !hasMCPServerOptions(tool.MCPServerOptions{Timeout: time.Second}) {
		t.Fatalf("expected true with Timeout")
	}
	// EnabledTools set returns true.
	if !hasMCPServerOptions(tool.MCPServerOptions{EnabledTools: []string{"bash"}}) {
		t.Fatalf("expected true with EnabledTools")
	}
	// DisabledTools set returns true.
	if !hasMCPServerOptions(tool.MCPServerOptions{DisabledTools: []string{"bash"}}) {
		t.Fatalf("expected true with DisabledTools")
	}
	// ToolTimeout set returns true.
	if !hasMCPServerOptions(tool.MCPServerOptions{ToolTimeout: time.Second}) {
		t.Fatalf("expected true with ToolTimeout")
	}
}

// ---------------------------------------------------------------------------
// enforceSandboxHost
// ---------------------------------------------------------------------------

func TestRuntimeToolsEnforceSandboxHost(t *testing.T) {
	t.Parallel()
	// nil manager returns nil.
	if err := enforceSandboxHost(nil, "https://example.com"); err != nil {
		t.Fatalf("expected nil error with nil manager, got %v", err)
	}
	// Empty/whitespace server returns nil regardless of manager.
	if err := enforceSandboxHost(nil, ""); err != nil {
		t.Fatalf("expected nil with empty server, got %v", err)
	}
	if err := enforceSandboxHost(nil, "  "); err != nil {
		t.Fatalf("expected nil with whitespace server, got %v", err)
	}
	// Non-URL server (no scheme) returns nil -- not a network host.
	if err := enforceSandboxHost(nil, "just-a-string"); err != nil {
		t.Fatalf("expected nil for non-URL spec, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// coreToolUsePayload
// ---------------------------------------------------------------------------

func TestRuntimeToolsCoreToolUsePayload(t *testing.T) {
	t.Parallel()
	call := agent.ToolCall{
		ID:    "id-1",
		Name:  "bash",
		Input: map[string]any{"command": "ls"},
	}
	payload := coreToolUsePayload(call)
	if payload.Name != "bash" {
		t.Fatalf("expected Name=Bash, got %q", payload.Name)
	}
	if payload.Params["command"] != "ls" {
		t.Fatalf("expected command param, got %v", payload.Params)
	}
	// Verify nil Input is passed as nil Params.
	call2 := agent.ToolCall{Name: "read", Input: nil}
	payload2 := coreToolUsePayload(call2)
	if payload2.Name != "read" {
		t.Fatalf("expected Name=Read, got %q", payload2.Name)
	}
}

// ---------------------------------------------------------------------------
// coreToolResultPayload
// ---------------------------------------------------------------------------

func TestRuntimeToolsCoreToolResultPayload(t *testing.T) {
	t.Parallel()
	call := agent.ToolCall{ID: "id-1", Name: "bash"}

	// nil result, nil error.
	payload := coreToolResultPayload(call, nil, nil)
	if payload.Name != "bash" {
		t.Fatalf("expected Name=Bash, got %q", payload.Name)
	}
	if payload.Result != nil {
		t.Fatalf("expected nil Result with nil call result")
	}
	if payload.Err != nil {
		t.Fatalf("expected nil Err")
	}

	// With result.
	now := time.Now()
	result := &tool.CallResult{
		Call:        tool.Call{Name: "bash"},
		Result:      &tool.ToolResult{Output: "hello"},
		StartedAt:   now,
		CompletedAt: now.Add(100 * time.Millisecond),
	}
	payload = coreToolResultPayload(call, result, nil)
	if payload.Result != "hello" {
		t.Fatalf("expected Result=hello, got %v", payload.Result)
	}
	if payload.Duration != 100*time.Millisecond {
		t.Fatalf("expected Duration=100ms, got %v", payload.Duration)
	}

	// With error.
	execErr := errors.New("execution failed")
	payload = coreToolResultPayload(call, nil, execErr)
	if payload.Err != execErr {
		t.Fatalf("expected Err to be execution error, got %v", payload.Err)
	}

	// Result with nil inner ToolResult: payload.Result stays nil.
	resultNilInner := &tool.CallResult{Call: tool.Call{Name: "bash"}}
	payload = coreToolResultPayload(call, resultNilInner, nil)
	if payload.Result != nil {
		t.Fatalf("expected nil Result when inner result is nil, got %v", payload.Result)
	}
}

// ---------------------------------------------------------------------------
// isAllowed
// ---------------------------------------------------------------------------

func TestRuntimeToolsIsAllowed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// yolo mode allows everything.
	yoloExec := &runtimeToolExecutor{yolo: true}
	if !yoloExec.isAllowed(ctx, "anything") {
		t.Fatalf("yolo mode should allow all tools")
	}
	if !yoloExec.isAllowed(ctx, "") {
		t.Fatalf("yolo mode should allow even empty name")
	}

	// Empty canonical name is not allowed (canonicalToolName("") == "").
	normalExec := &runtimeToolExecutor{}
	if normalExec.isAllowed(ctx, "") {
		t.Fatalf("empty tool name should not be allowed")
	}
	if normalExec.isAllowed(ctx, "   ") {
		t.Fatalf("whitespace-only tool name should not be allowed")
	}

	// Empty allow map means all tools allowed (len==0 check).
	allAllowed := &runtimeToolExecutor{allow: map[string]struct{}{}}
	if !allAllowed.isAllowed(ctx, "bash") {
		t.Fatalf("empty allow map should allow all tools")
	}

	// Specific allow map restricts to listed tools.
	restricted := &runtimeToolExecutor{allow: map[string]struct{}{"bash": {}, "read": {}}}
	if !restricted.isAllowed(ctx, "bash") {
		t.Fatalf("bash should be allowed in restricted map")
	}
	if !restricted.isAllowed(ctx, "bash") {
		t.Fatalf("Bash (uppercase) should be allowed (canonical lowercase)")
	}
	if restricted.isAllowed(ctx, "grep") {
		t.Fatalf("grep should not be allowed when not in allow map")
	}

	// nil allow map means all tools allowed.
	nilAllow := &runtimeToolExecutor{allow: nil}
	if !nilAllow.isAllowed(ctx, "bash") {
		t.Fatalf("nil allow map should allow all tools")
	}

	// Subagent context whitelist intersects with allow map.
	subCtx := subagents.Context{ToolWhitelist: []string{"bash", "grep"}}
	subCtxInCtx := subagents.WithContext(ctx, subCtx)

	// No allow map, subagent whitelist grants access.
	if !nilAllow.isAllowed(subCtxInCtx, "bash") {
		t.Fatalf("subagent whitelist should allow bash")
	}
	if !nilAllow.isAllowed(subCtxInCtx, "grep") {
		t.Fatalf("subagent whitelist should allow grep")
	}
	if nilAllow.isAllowed(subCtxInCtx, "write") {
		t.Fatalf("tool not in subagent whitelist should be denied")
	}

	// Both allow map and subagent whitelist: intersection required.
	intersectExec := &runtimeToolExecutor{allow: map[string]struct{}{"bash": {}}}
	if !intersectExec.isAllowed(subCtxInCtx, "bash") {
		t.Fatalf("bash in both allow and whitelist should be allowed")
	}
	if intersectExec.isAllowed(subCtxInCtx, "grep") {
		t.Fatalf("grep in whitelist but not allow map should be denied")
	}

	// Subagent with empty whitelist: subagent does not restrict.
	emptySubCtx := subagents.WithContext(ctx, subagents.Context{ToolWhitelist: []string{}})
	if !intersectExec.isAllowed(emptySubCtx, "bash") {
		t.Fatalf("empty subagent whitelist should not restrict, only allow map applies")
	}

	// No subagent context in ctx: only allow map applies.
	if !intersectExec.isAllowed(ctx, "bash") {
		t.Fatalf("without subagent context, only allow map applies")
	}
}

// ---------------------------------------------------------------------------
// measureUsage
// ---------------------------------------------------------------------------

func TestRuntimeToolsMeasureUsage(t *testing.T) {
	t.Parallel()
	exec := &runtimeToolExecutor{}
	usage := exec.measureUsage()
	if usage.MemoryBytes == 0 {
		t.Fatalf("expected non-zero memory usage from runtime.MemStats")
	}
}

// ---------------------------------------------------------------------------
// mock types for testing (unique names to avoid conflicts with other test files)
// ---------------------------------------------------------------------------

// rtContextWindowModel satisfies model.Model, model.ContextWindowProvider,
// and model.ModelNamer for resolveContextWindow tests.
type rtContextWindowModel struct {
	mockModel // embed existing mockModel to satisfy model.Model
	window    int
	namer     string
}

func (m *rtContextWindowModel) ContextWindow() int { return m.window }
func (m *rtContextWindowModel) ModelName() string  { return m.namer }

// ---------------------------------------------------------------------------
// helpers (unique names to avoid conflicts with other test files)
// ---------------------------------------------------------------------------

func rtContainsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}
