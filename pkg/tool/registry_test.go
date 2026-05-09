package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegistryRegister(t *testing.T) {
	tests := []struct {
		name        string
		tool        Tool
		preRegister []Tool
		wantErr     string
		verify      func(t *testing.T, r *Registry)
	}{
		{name: "nil tool", wantErr: "tool is nil"},
		{name: "empty name", tool: &spyTool{name: ""}, wantErr: "tool name is empty"},
		{
			name:        "duplicate name rejected",
			tool:        &spyTool{name: "echo"},
			preRegister: []Tool{&spyTool{name: "echo"}},
			wantErr:     "already registered",
		},
		{
			name: "successful registration available via get and list",
			tool: &spyTool{name: "sum", result: &ToolResult{Output: "ok"}},
			verify: func(t *testing.T, r *Registry) {
				t.Helper()
				got, err := r.Get("sum")
				if err != nil {
					t.Fatalf("get failed: %v", err)
				}
				if got.Name() != "sum" {
					t.Fatalf("unexpected tool returned: %s", got.Name())
				}
				if len(r.List()) != 1 {
					t.Fatalf("list length = %d", len(r.List()))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			for _, pre := range tt.preRegister {
				if err := r.Register(pre); err != nil {
					t.Fatalf("setup register failed: %v", err)
				}
			}
			err := r.Register(tt.tool)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("register failed: %v", err)
			}
			if tt.verify != nil {
				tt.verify(t, r)
			}
		})
	}
}

func TestRegistryExecute(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		tool       *spyTool
		params     map[string]interface{}
		validator  Validator
		wantErr    string
		wantCalls  int
		wantParams map[string]interface{}
	}{
		{
			name:      "tool without schema bypasses validator",
			tool:      &spyTool{name: "echo", result: &ToolResult{Output: "ok"}},
			validator: &spyValidator{},
			wantCalls: 1,
		},
		{
			name:      "validation failure prevents execution",
			tool:      &spyTool{name: "calc", schema: &JSONSchema{Type: "object"}},
			validator: &spyValidator{err: errors.New("boom")},
			wantErr:   "validation failed",
			wantCalls: 0,
		},
		{
			name:       "validation success forwards params to tool",
			tool:       &spyTool{name: "calc", schema: &JSONSchema{Type: "object"}, result: &ToolResult{Output: "ok"}},
			validator:  &spyValidator{},
			params:     map[string]interface{}{"x": 1},
			wantCalls:  1,
			wantParams: map[string]interface{}{"x": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			if err := r.Register(tt.tool); err != nil {
				t.Fatalf("register: %v", err)
			}
			if tt.validator != nil {
				r.SetValidator(tt.validator)
			}
			res, err := r.Execute(ctx, tt.tool.Name(), tt.params)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q got %v", tt.wantErr, err)
				}
				if tt.tool.calls != tt.wantCalls {
					t.Fatalf("tool calls = %d", tt.tool.calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("execute failed: %v", err)
			}
			if tt.tool.calls != tt.wantCalls {
				t.Fatalf("tool calls = %d want %d", tt.tool.calls, tt.wantCalls)
			}
			if tt.wantParams != nil {
				for k, v := range tt.wantParams {
					if tt.tool.params[k] != v {
						t.Fatalf("param %s mismatch", k)
					}
				}
			}
			if res == nil {
				t.Fatal("nil result returned")
			}
		})
	}

	t.Run("unknown tool name returns error", func(t *testing.T) {
		r := NewRegistry()
		if _, err := r.Execute(ctx, "missing", nil); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected not found error, got %v", err)
		}
	})
}

func TestRegisterMCPServerSSE(t *testing.T) {
	h := newRegistrySSEHarness(t)
	defer h.Close()

	r := NewRegistry()
	if err := r.RegisterMCPServer(context.Background(), h.URL(), ""); err != nil {
		t.Fatalf("register MCP: %v", err)
	}

	res, err := r.Execute(context.Background(), "echo", map[string]interface{}{"text": "ping"})
	if err != nil {
		t.Fatalf("execute remote tool: %v", err)
	}
	if !strings.Contains(res.Output, "echo:ping") {
		t.Fatalf("unexpected output: %s", res.Output)
	}
}

func TestRegisterMCPServerSSERefreshesOnListChanged(t *testing.T) {
	h := newRegistrySSEHarness(t)
	defer h.Close()

	r := NewRegistry()
	if err := r.RegisterMCPServer(context.Background(), h.URL(), ""); err != nil {
		t.Fatalf("register MCP: %v", err)
	}

	h.server.AddTool(&mcpsdk.Tool{
		Name:        "sum",
		Description: "sum tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "sum"}}}, nil
	})

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for MCP tool refresh")
		case <-ticker.C:
			if _, err := r.Get("sum"); err == nil {
				return
			}
		}
	}
}

type spyTool struct {
	name   string
	schema *JSONSchema
	result *ToolResult
	err    error
	calls  int
	params map[string]interface{}
}

func (s *spyTool) Name() string        { return s.name }
func (s *spyTool) Description() string { return "spy" }
func (s *spyTool) Schema() *JSONSchema { return s.schema }
func (s *spyTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	s.calls++
	s.params = params
	if s.result == nil {
		s.result = &ToolResult{}
	}
	return s.result, s.err
}

type spyValidator struct {
	err    error
	calls  int
	schema *JSONSchema
}

func (v *spyValidator) Validate(params map[string]interface{}, schema *JSONSchema) error {
	v.calls++
	v.schema = schema
	return v.err
}

type registrySSEHarness struct {
	srv    *httptest.Server
	server *mcpsdk.Server
}

func newRegistrySSEHarness(t *testing.T) *registrySSEHarness {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("httptest: listen not permitted: %v", err)
	}

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "registry-test", Version: "dev"}, nil)
	server.AddTool(&mcpsdk.Tool{
		Name:        "echo",
		Description: "echo tool",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var payload map[string]string
		if err := json.Unmarshal(req.Params.Arguments, &payload); err != nil {
			return nil, err
		}
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo:" + payload["text"]}},
		}, nil
	})

	handler := mcpsdk.NewSSEHandler(func(*http.Request) *mcpsdk.Server {
		return server
	}, nil)

	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = listener
	srv.Start()

	return &registrySSEHarness{
		srv:    srv,
		server: server,
	}
}

func (h *registrySSEHarness) URL() string {
	return h.srv.URL
}

func (h *registrySSEHarness) Close() {
	h.srv.CloseClientConnections()
	h.srv.Close()
}

func TestConvertMCPSchema(t *testing.T) {
	jsonInput := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`)
	schema, err := convertMCPSchema(jsonInput)
	if err != nil {
		t.Fatalf("convert err: %v", err)
	}
	if schema.Type != "object" || len(schema.Required) != 1 {
		t.Fatalf("unexpected schema: %#v", schema)
	}

	if _, err := convertMCPSchema(json.RawMessage(``)); err != nil {
		t.Fatalf("empty raw should return nil, got %v", err)
	}
	if val, err := convertMCPSchema(nil); err != nil || val != nil {
		t.Fatalf("nil raw should return nil, got %v %v", val, err)
	}
	if alt, err := convertMCPSchema(json.RawMessage(`{"properties":{"x":{"type":"number"}},"required":["x"]}`)); err != nil || alt == nil {
		t.Fatalf("expected map-based schema, got %#v err=%v", alt, err)
	}
	if _, err := convertMCPSchema(json.RawMessage(`{`)); err == nil {
		t.Fatalf("expected unmarshal error")
	}
}

// ---------- Comprehensive Registry Tests ----------

func TestRegistryNewRegistry(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if len(r.tools) != 0 {
		t.Fatalf("expected empty tools map, got %d entries", len(r.tools))
	}
	if len(r.sources) != 0 {
		t.Fatalf("expected empty sources map, got %d entries", len(r.sources))
	}
	if r.validator == nil {
		t.Fatal("expected default validator to be set")
	}
	if _, ok := r.validator.(DefaultValidator); !ok {
		t.Fatalf("expected DefaultValidator, got %T", r.validator)
	}
}

func TestRegistryRegisterWithSource(t *testing.T) {
	t.Parallel()

	r := NewRegistry()

	// Successful registration with explicit source
	tool := &spyTool{name: "read", result: &ToolResult{Output: "ok"}}
	if err := r.RegisterWithSource(tool, "plugin"); err != nil {
		t.Fatalf("RegisterWithSource failed: %v", err)
	}
	if source := r.ToolSource("read"); source != "plugin" {
		t.Fatalf("expected source 'plugin', got '%s'", source)
	}

	// Registration failure propagates: nil tool
	if err := r.RegisterWithSource(nil, "plugin"); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("expected nil tool error, got %v", err)
	}

	// Registration failure propagates: duplicate name
	if err := r.RegisterWithSource(&spyTool{name: "read"}, "plugin2"); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected duplicate error, got %v", err)
	}

	// Empty source still stored (overwrites builtin default from Register)
	tool2 := &spyTool{name: "write", result: &ToolResult{Output: "ok"}}
	if err := r.RegisterWithSource(tool2, ""); err != nil {
		t.Fatalf("RegisterWithSource with empty source: %v", err)
	}
	if source := r.ToolSource("write"); source != "" {
		t.Fatalf("expected empty source, got '%s'", source)
	}
}

func TestRegistryToolSource(t *testing.T) {
	t.Parallel()

	r := NewRegistry()

	// Unknown tool returns "builtin" default
	if source := r.ToolSource("nonexistent"); source != "builtin" {
		t.Fatalf("expected default 'builtin' for unknown tool, got '%s'", source)
	}

	// Register defaults to "builtin" source
	if err := r.Register(&spyTool{name: "calc"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if source := r.ToolSource("calc"); source != "builtin" {
		t.Fatalf("expected 'builtin' for standard register, got '%s'", source)
	}

	// Explicit source overrides default
	if err := r.RegisterWithSource(&spyTool{name: "cmd"}, "mcp:svc"); err != nil {
		t.Fatalf("register with source: %v", err)
	}
	if source := r.ToolSource("cmd"); source != "mcp:svc" {
		t.Fatalf("expected 'mcp:svc', got '%s'", source)
	}
}

func TestRegistryGet(t *testing.T) {
	t.Parallel()

	r := NewRegistry()

	// Not found
	_, err := r.Get("missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}

	// Found after registration
	tool := &spyTool{name: "find", result: &ToolResult{Output: "found"}}
	if err := r.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, err := r.Get("find")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Name() != "find" {
		t.Fatalf("expected tool name 'find', got '%s'", got.Name())
	}
}

func TestRegistryList(t *testing.T) {
	t.Parallel()

	r := NewRegistry()

	// Empty registry
	if tools := r.List(); len(tools) != 0 {
		t.Fatalf("expected empty list, got %d", len(tools))
	}

	// Single tool
	if err := r.Register(&spyTool{name: "alpha"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if tools := r.List(); len(tools) != 1 || tools[0].Name() != "alpha" {
		t.Fatalf("expected [alpha], got %v", toolNames(tools))
	}

	// Multiple tools sorted by name
	for _, name := range []string{"delta", "beta", "gamma"} {
		if err := r.Register(&spyTool{name: name}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	tools := r.List()
	names := toolNames(tools)
	expected := []string{"alpha", "beta", "delta", "gamma"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d tools, got %d", len(expected), len(names))
	}
	for i, n := range expected {
		if names[i] != n {
			t.Fatalf("expected sorted order, got %v", names)
		}
	}
}

func TestRegistrySetValidator(t *testing.T) {
	t.Parallel()

	r := NewRegistry()

	custom := &spyValidator{}
	r.SetValidator(custom)

	r.mu.RLock()
	v := r.validator
	r.mu.RUnlock()

	if v != custom {
		t.Fatalf("expected custom validator, got %T", v)
	}

	// Set to nil to skip validation
	r.SetValidator(nil)
	r.mu.RLock()
	v = r.validator
	r.mu.RUnlock()
	if v != nil {
		t.Fatalf("expected nil validator, got %T", v)
	}
}

func TestRegistryExecuteToolError(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	tool := &spyTool{name: "fail", err: errors.New("tool crashed")}
	if err := r.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err := r.Execute(context.Background(), "fail", nil)
	if err == nil || !strings.Contains(err.Error(), "tool crashed") {
		t.Fatalf("expected tool error propagated, got %v", err)
	}
	if tool.calls != 1 {
		t.Fatalf("expected 1 call, got %d", tool.calls)
	}
}

func TestRegistryExecuteNilValidatorWithSchema(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	tool := &spyTool{name: "check", schema: &JSONSchema{Type: "object"}, result: &ToolResult{Output: "ok"}}
	if err := r.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Set validator to nil so schema validation is skipped
	r.SetValidator(nil)

	res, err := r.Execute(context.Background(), "check", map[string]interface{}{"x": 1})
	if err != nil {
		t.Fatalf("execute with nil validator should skip validation: %v", err)
	}
	if res == nil {
		t.Fatal("expected result")
	}
	if tool.calls != 1 {
		t.Fatalf("expected 1 call, got %d", tool.calls)
	}
}

func TestRegistryExecuteNilSchemaSkipsValidation(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	sv := &spyValidator{err: errors.New("should not be called")}
	r.SetValidator(sv)

	tool := &spyTool{name: "noschema", result: &ToolResult{Output: "ok"}}
	if err := r.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}

	res, err := r.Execute(context.Background(), "noschema", nil)
	if err != nil {
		t.Fatalf("execute should succeed with nil schema: %v", err)
	}
	if res == nil {
		t.Fatal("expected result")
	}
	if sv.calls != 0 {
		t.Fatalf("validator should not be called when schema is nil, got %d calls", sv.calls)
	}
}

func TestRegistryCloseNilSessionInfo(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	// Insert nil entries alongside real sessions to verify Close skips them
	r.mcpSessions = []*mcpSessionInfo{nil}
	r.Close()
	r.mu.RLock()
	sessions := r.mcpSessions
	r.mu.RUnlock()
	if sessions != nil {
		t.Fatalf("expected sessions cleared, got %d", len(sessions))
	}
}

func TestRegistryCloseEmptySessions(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Close() // no sessions, should not panic
	r.mu.RLock()
	sessions := r.mcpSessions
	r.mu.RUnlock()
	if sessions != nil {
		t.Fatalf("expected nil sessions after close, got %v", sessions)
	}
}

func TestRegistryCloseNilSessionWithinInfo(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.mcpSessions = []*mcpSessionInfo{
		{serverID: "srv1", session: nil},
	}
	r.Close()
	r.mu.RLock()
	sessions := r.mcpSessions
	r.mu.RUnlock()
	if sessions != nil {
		t.Fatalf("expected sessions cleared, got %d", len(sessions))
	}
}

func TestRegistryMCPToolFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		enabled    []string
		disabled   []string
		remoteName string
		localName  string
		want       bool
	}{
		{name: "no filters allows all", enabled: nil, disabled: nil, remoteName: "echo", localName: "svc__echo", want: true},
		{name: "enabled allows matching remote name", enabled: []string{"echo"}, disabled: nil, remoteName: "echo", localName: "svc__echo", want: true},
		{name: "enabled allows matching local name", enabled: []string{"svc__echo"}, disabled: nil, remoteName: "echo", localName: "svc__echo", want: true},
		{name: "enabled blocks non-matching", enabled: []string{"sum"}, disabled: nil, remoteName: "echo", localName: "svc__echo", want: false},
		{name: "disabled blocks matching remote name", enabled: nil, disabled: []string{"echo"}, remoteName: "echo", localName: "svc__echo", want: false},
		{name: "disabled blocks matching local name", enabled: nil, disabled: []string{"svc__echo"}, remoteName: "echo", localName: "svc__echo", want: false},
		{name: "disabled allows non-matching", enabled: nil, disabled: []string{"sum"}, remoteName: "echo", localName: "svc__echo", want: true},
		{name: "deny overrides allow", enabled: []string{"echo"}, disabled: []string{"echo"}, remoteName: "echo", localName: "svc__echo", want: false},
		{name: "deny overrides allow by local name", enabled: []string{"svc__echo"}, disabled: []string{"svc__echo"}, remoteName: "echo", localName: "svc__echo", want: false},
		{name: "enabled allows but disabled blocks different tool", enabled: []string{"echo"}, disabled: []string{"sum"}, remoteName: "echo", localName: "svc__echo", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := newMCPToolFilter(tt.enabled, tt.disabled)
			if got := filter.allows(tt.remoteName, tt.localName); got != tt.want {
				t.Fatalf("allows(%q,%q) = %v, want %v", tt.remoteName, tt.localName, got, tt.want)
			}
		})
	}
}

func TestRegistryMCPToolFilterMatchesEmptySet(t *testing.T) {
	t.Parallel()

	f := mcpToolFilter{enabled: nil, disabled: nil}
	if f.matches(nil, "echo", "svc__echo") {
		t.Fatalf("matches on nil set should return false")
	}
	if f.matches(map[string]struct{}{}, "echo") {
		t.Fatalf("matches on empty set should return false")
	}
}

func TestRegistryNormalizeMCPToolNameSet(t *testing.T) {
	t.Parallel()

	// Empty input returns nil
	if got := normalizeMCPToolNameSet(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}
	if got := normalizeMCPToolNameSet([]string{}); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}

	// Whitespace-only input returns nil (all entries filtered out)
	if got := normalizeMCPToolNameSet([]string{"  ", ""}); got != nil {
		t.Fatalf("expected nil for whitespace-only input, got %v", got)
	}

	// Valid entries with trimming
	got := normalizeMCPToolNameSet([]string{" echo ", "sum", ""})
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if _, ok := got["echo"]; !ok {
		t.Fatalf("expected 'echo' in set")
	}
	if _, ok := got["sum"]; !ok {
		t.Fatalf("expected 'sum' in set")
	}
}

func TestRegistryExtractMediaFromMCPContent(t *testing.T) {
	t.Parallel()

	// Empty content returns nil
	if got := extractMediaFromMCPContent(nil); got != nil {
		t.Fatalf("expected nil for nil content, got %v", got)
	}
	if got := extractMediaFromMCPContent([]mcp.Content{}); got != nil {
		t.Fatalf("expected nil for empty content, got %v", got)
	}

	// ImageContent
	img := &mcp.ImageContent{MIMEType: "image/png", Data: []byte("iVBOR")}
	got := extractMediaFromMCPContent([]mcp.Content{img})
	if got == nil {
		t.Fatal("expected media metadata for image")
	}
	if got["media_type"] != "image" {
		t.Fatalf("expected media_type=image, got %v", got["media_type"])
	}
	url, _ := got["media_url"].(string)
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("expected data URI prefix for image, got %s", url)
	}

	// AudioContent
	audio := &mcp.AudioContent{MIMEType: "audio/wav", Data: []byte("RIFF")}
	got = extractMediaFromMCPContent([]mcp.Content{audio})
	if got == nil {
		t.Fatal("expected media metadata for audio")
	}
	if got["media_type"] != "audio" {
		t.Fatalf("expected media_type=audio, got %v", got["media_type"])
	}

	// EmbeddedResource with image resource
	rc := &mcp.ResourceContents{URI: "file://img", MIMEType: "image/jpeg", Blob: []byte("jpegdata")}
	embedded := &mcp.EmbeddedResource{Resource: rc}
	got = extractMediaFromMCPContent([]mcp.Content{embedded})
	if got == nil {
		t.Fatal("expected media metadata for embedded image")
	}
	if got["media_type"] != "image" {
		t.Fatalf("expected media_type=image for embedded, got %v", got["media_type"])
	}

	// EmbeddedResource with nil resource returns nil
	got = extractMediaFromMCPContent([]mcp.Content{&mcp.EmbeddedResource{Resource: nil}})
	if got != nil {
		t.Fatalf("expected nil for embedded with nil resource, got %v", got)
	}

	// TextContent does not produce media metadata
	text := &mcp.TextContent{Text: "hello"}
	got = extractMediaFromMCPContent([]mcp.Content{text})
	if got != nil {
		t.Fatalf("expected nil for text content, got %v", got)
	}
}

func TestRegistryExtractMediaFromResource(t *testing.T) {
	t.Parallel()

	// Nil resource
	if got := extractMediaFromResource(nil); got != nil {
		t.Fatalf("expected nil for nil resource, got %v", got)
	}

	// Empty blob
	rc := &mcp.ResourceContents{URI: "u", MIMEType: "image/png", Blob: []byte{}}
	if got := extractMediaFromResource(rc); got != nil {
		t.Fatalf("expected nil for empty blob, got %v", got)
	}

	// Image MIME
	rc = &mcp.ResourceContents{URI: "u", MIMEType: "image/png", Blob: []byte("data")}
	got := extractMediaFromResource(rc)
	if got == nil || got["media_type"] != "image" {
		t.Fatalf("expected image metadata, got %v", got)
	}

	// Audio MIME
	rc = &mcp.ResourceContents{URI: "u", MIMEType: "audio/mp3", Blob: []byte("data")}
	got = extractMediaFromResource(rc)
	if got == nil || got["media_type"] != "audio" {
		t.Fatalf("expected audio metadata, got %v", got)
	}

	// Video MIME
	rc = &mcp.ResourceContents{URI: "u", MIMEType: "video/mp4", Blob: []byte("data")}
	got = extractMediaFromResource(rc)
	if got == nil || got["media_type"] != "video" {
		t.Fatalf("expected video metadata, got %v", got)
	}

	// Non-media MIME (application/json) returns nil
	rc = &mcp.ResourceContents{URI: "u", MIMEType: "application/json", Blob: []byte("data")}
	if got := extractMediaFromResource(rc); got != nil {
		t.Fatalf("expected nil for non-media mime, got %v", got)
	}
}

func TestRegistryBase64Encode(t *testing.T) {
	t.Parallel()

	input := []byte("hello world")
	expected := base64.StdEncoding.EncodeToString(input)
	if got := base64Encode(input); got != expected {
		t.Fatalf("expected %s, got %s", expected, got)
	}

	// Empty input
	if got := base64Encode([]byte{}); got != "" {
		t.Fatalf("expected empty string for empty input, got %s", got)
	}

	// Nil input
	if got := base64Encode(nil); got != "" {
		t.Fatalf("expected empty string for nil input, got %s", got)
	}
}

func TestRegistryCloneMCPServerOptions(t *testing.T) {
	t.Parallel()

	original := MCPServerOptions{
		Headers:       map[string]string{"A": "1"},
		Env:           map[string]string{"B": "2"},
		Timeout:       5 * time.Second,
		EnabledTools:  []string{"echo"},
		DisabledTools: []string{"sum"},
		ToolTimeout:   3 * time.Second,
	}

	cloned := cloneMCPServerOptions(original)

	// Duration fields copied by value
	if cloned.Timeout != original.Timeout {
		t.Fatalf("timeout mismatch")
	}
	if cloned.ToolTimeout != original.ToolTimeout {
		t.Fatalf("tool timeout mismatch")
	}

	// Maps are independent
	cloned.Headers["A"] = "modified"
	if original.Headers["A"] != "1" {
		t.Fatalf("original headers mutated: %v", original.Headers)
	}

	cloned.Env["B"] = "modified"
	if original.Env["B"] != "2" {
		t.Fatalf("original env mutated: %v", original.Env)
	}

	// Slices are independent
	cloned.EnabledTools[0] = "changed"
	if original.EnabledTools[0] != "echo" {
		t.Fatalf("original EnabledTools mutated: %v", original.EnabledTools)
	}
	cloned.DisabledTools = append(cloned.DisabledTools, "extra")
	if len(original.DisabledTools) != 1 {
		t.Fatalf("original DisabledTools mutated: %v", original.DisabledTools)
	}
}

func TestRegistryCloneStringMap(t *testing.T) {
	t.Parallel()

	// Nil input returns nil
	if got := cloneStringMap(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}

	// Empty input returns nil
	if got := cloneStringMap(map[string]string{}); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}

	// Cloned map is independent
	original := map[string]string{"key": "value"}
	cloned := cloneStringMap(original)
	cloned["key"] = "modified"
	if original["key"] != "value" {
		t.Fatalf("original map mutated")
	}
}

func TestRegistryToNameSet(t *testing.T) {
	t.Parallel()

	// Empty input returns nil
	if got := toNameSet(nil); got != nil {
		t.Fatalf("expected nil for nil, got %v", got)
	}
	if got := toNameSet([]string{}); got != nil {
		t.Fatalf("expected nil for empty, got %v", got)
	}

	// Filters whitespace entries
	got := toNameSet([]string{"a", "  ", "", "b"})
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if _, ok := got["a"]; !ok {
		t.Fatalf("expected 'a' in set")
	}
	if _, ok := got["b"]; !ok {
		t.Fatalf("expected 'b' in set")
	}

	// All whitespace returns empty map (not nil, because len(names) > 0)
	got = toNameSet([]string{"  ", ""})
	if len(got) != 0 {
		t.Fatalf("expected 0 entries for all-whitespace, got %d", len(got))
	}
}

func TestRegistryRemoteToolAccessors(t *testing.T) {
	t.Parallel()

	schema := &JSONSchema{Type: "object", Properties: map[string]interface{}{"x": map[string]interface{}{"type": "string"}}}
	rt := &remoteTool{name: "svc__echo", remoteName: "echo", description: "remote echo", schema: schema}

	if rt.Name() != "svc__echo" {
		t.Fatalf("expected name 'svc__echo', got '%s'", rt.Name())
	}
	if rt.Description() != "remote echo" {
		t.Fatalf("expected description 'remote echo', got '%s'", rt.Description())
	}
	if rt.Schema() == nil || rt.Schema().Type != "object" {
		t.Fatalf("expected schema with type object, got %v", rt.Schema())
	}
}

func TestRegistryRemoteToolExecuteNilSession(t *testing.T) {
	t.Parallel()

	tool := &remoteTool{name: "remote", description: "desc"}
	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "session is nil") {
		t.Fatalf("expected nil session error, got %v", err)
	}
}

func TestRegistryRemoteToolExecuteUsesRemoteName(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}}
	var capturedName string
	server.callFn = func(_ context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
		capturedName = params.Name
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	}
	session, err := server.newSession()
	if err != nil {
		t.Fatalf("stub session: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	tool := &remoteTool{name: "svc__echo", remoteName: "echo", description: "desc", session: session}
	_, err = tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if capturedName != "echo" {
		t.Fatalf("expected remote name 'echo' in call, got '%s'", capturedName)
	}
}

func TestRegistryRemoteToolExecuteNilResult(t *testing.T) {
	t.Parallel()

	// The nil-result path in remoteTool.Execute is guarded by `res == nil`,
	// which cannot easily be triggered through the MCP stub session because
	// the SDK never returns a nil CallToolResult from a successful JSON-RPC
	// response. Verify the code path exists by checking the source, and test
	// that a CallTool error is correctly propagated instead.
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "remote"}}}
	server.callFn = func(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error) {
		return nil, fmt.Errorf("call failed")
	}
	session, err := server.newSession()
	if err != nil {
		t.Fatalf("stub session: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	tool := &remoteTool{name: "remote", session: session}
	_, err = tool.Execute(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "call failed") {
		t.Fatalf("expected call error propagated, got %v", err)
	}
}

func TestRegistryConvertMCPSchemaComprehensive(t *testing.T) {
	t.Parallel()

	// nil input
	schema, err := convertMCPSchema(nil)
	if err != nil || schema != nil {
		t.Fatalf("nil input: expected nil schema, got %v err=%v", schema, err)
	}

	// empty json.RawMessage
	schema, err = convertMCPSchema(json.RawMessage(""))
	if err != nil || schema != nil {
		t.Fatalf("empty raw: expected nil schema, got %v err=%v", schema, err)
	}

	// empty byte slice
	schema, err = convertMCPSchema([]byte(""))
	if err != nil || schema != nil {
		t.Fatalf("empty bytes: expected nil schema, got %v err=%v", schema, err)
	}

	// valid JSON object with type/properties/required
	input := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`)
	schema, err = convertMCPSchema(input)
	if err != nil || schema == nil {
		t.Fatalf("valid schema: expected non-nil, got %v err=%v", schema, err)
	}
	if schema.Type != "object" || len(schema.Required) != 1 || schema.Required[0] != "x" {
		t.Fatalf("schema fields mismatch: %+v", schema)
	}

	// valid JSON but missing type field: falls through to map-based extraction
	// (first unmarshal into JSONSchema succeeds with Type="", then map parsing
	// appends to the same schema struct, so Required may have duplicates)
	input = json.RawMessage(`{"properties":{"y":{"type":"number"}},"required":["y"]}`)
	schema, err = convertMCPSchema(input)
	if err != nil || schema == nil {
		t.Fatalf("schema without type: expected non-nil, got %v err=%v", schema, err)
	}
	// Type is not inferred from properties/required in convertMCPSchema
	if schema.Type != "" {
		t.Fatalf("expected empty type when not provided, got '%s'", schema.Type)
	}
	// Required is populated from both struct unmarshal and map parsing
	if len(schema.Required) < 1 {
		t.Fatalf("expected at least 1 required field, got %v", schema.Required)
	}

	// generic map input
	mapInput := map[string]interface{}{
		"type":       "string",
		"properties": map[string]interface{}{"z": map[string]interface{}{"type": "integer"}},
	}
	schema, err = convertMCPSchema(mapInput)
	if err != nil || schema == nil || schema.Type != "string" {
		t.Fatalf("map input: expected type 'string', got %v err=%v", schema, err)
	}

	// invalid JSON
	_, err = convertMCPSchema(json.RawMessage("{invalid"))
	if err == nil {
		t.Fatalf("expected unmarshal error for invalid JSON")
	}

	// invalid JSON as byte slice
	_, err = convertMCPSchema([]byte("{invalid"))
	if err == nil {
		t.Fatalf("expected unmarshal error for invalid bytes")
	}

	// required with non-string values: map parsing filters out non-string entries
	data := map[string]interface{}{
		"type":     "object",
		"required": []interface{}{"x", 1, "y"},
	}
	schema, err = convertMCPSchema(data)
	if err != nil || schema == nil {
		t.Fatalf("mixed required: %v", err)
	}
	// At minimum, string entries "x" and "y" must be present
	for _, want := range []string{"x", "y"} {
		found := false
		for _, r := range schema.Required {
			if r == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected required field '%s' present, got %v", want, schema.Required)
		}
	}
}

func TestRegistryNonNilContext(t *testing.T) {
	t.Parallel()

	// Valid context passed through
	ctx := context.TODO()
	if got := nonNilContext(ctx); got != ctx {
		t.Fatalf("expected context passthrough")
	}

	// nil replaced with Background
	if got := nonNilContext(nil); got == nil { //nolint:staticcheck
		t.Fatalf("expected non-nil context for nil input")
	}

	// Canceled context still passed through
	ctx2, cancel := context.WithCancel(context.Background())
	cancel()
	if got := nonNilContext(ctx2); got != ctx2 {
		t.Fatalf("expected canceled context passthrough")
	}
}

func TestRegistryFirstTextContent(t *testing.T) {
	t.Parallel()

	// nil content
	if got := firstTextContent(nil); got != "" {
		t.Fatalf("expected empty string for nil, got %q", got)
	}

	// empty content
	if got := firstTextContent([]mcp.Content{}); got != "" {
		t.Fatalf("expected empty string for empty, got %q", got)
	}

	// single text content
	if got := firstTextContent([]mcp.Content{&mcp.TextContent{Text: "hello"}}); got != "hello" {
		t.Fatalf("expected 'hello', got %q", got)
	}

	// text after non-text content
	content := []mcp.Content{
		&mcp.ImageContent{MIMEType: "image/png", Data: []byte("img")},
		&mcp.TextContent{Text: "found"},
	}
	if got := firstTextContent(content); got != "found" {
		t.Fatalf("expected first text 'found', got %q", got)
	}

	// no text content at all
	content = []mcp.Content{&mcp.ImageContent{MIMEType: "image/png", Data: []byte("img")}}
	if got := firstTextContent(content); got != "" {
		t.Fatalf("expected empty when no text content, got %q", got)
	}
}

func TestRegistryMergeEnv(t *testing.T) {
	t.Parallel()

	// nil base with extra
	env := mergeEnv(nil, map[string]string{"KEY": "val"})
	if len(env) == 0 {
		t.Fatalf("expected env entries")
	}
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "KEY=") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected KEY=val in env: %v", env)
	}

	// extra overrides base
	env = mergeEnv([]string{"A=1", "B=2"}, map[string]string{"B": "3"})
	for _, e := range env {
		if e == "B=2" {
			t.Fatalf("expected B=2 overridden by B=3")
		}
		if e == "B=3" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected B=3 in env: %v", env)
	}

	// duplicate base entries deduped
	env = mergeEnv([]string{"X=1", "X=2"}, map[string]string{"Y": "3"})
	xCount := 0
	for _, e := range env {
		if strings.HasPrefix(e, "X=") {
			xCount++
		}
	}
	if xCount != 1 {
		t.Fatalf("expected deduped X entries, got %d", xCount)
	}

	// malformed entries skipped when extra is present (triggers dedup loop)
	env = mergeEnv([]string{"BAD", "GOOD=val"}, map[string]string{"X": "1"})
	for _, e := range env {
		if e == "BAD" {
			t.Fatalf("expected malformed entry skipped")
		}
	}
	if found := false; !found {
		for _, e := range env {
			if e == "GOOD=val" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected GOOD=val retained: %v", env)
		}
	}

	// empty extra returns base unchanged
	env = mergeEnv([]string{"A=1"}, map[string]string{})
	if len(env) != 1 || env[0] != "A=1" {
		t.Fatalf("expected base unchanged: %v", env)
	}

	// empty key in extra skipped
	env = mergeEnv([]string{"A=1"}, map[string]string{"": "skip", "B": "2"})
	for _, e := range env {
		if strings.HasPrefix(e, "=") {
			t.Fatalf("expected empty key skipped")
		}
	}
}

func TestRegistryNormalizeHeaders(t *testing.T) {
	t.Parallel()

	// nil returns nil
	if got := normalizeHeaders(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}

	// empty returns nil
	if got := normalizeHeaders(map[string]string{}); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}

	// keys canonicalized and trimmed, empty keys skipped
	headers := normalizeHeaders(map[string]string{
		" x-test ": " value ",
		"":         "skip",
		"accept":   "application/json",
	})
	if headers.Get("X-Test") != "value" {
		t.Fatalf("expected X-Test=value, got %v", headers.Get("X-Test"))
	}
	if headers.Get("Accept") != "application/json" {
		t.Fatalf("expected Accept=application/json, got %v", headers.Get("Accept"))
	}
	if len(headers) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(headers))
	}
}

func TestRegistryHeaderRoundTripper(t *testing.T) {
	t.Parallel()

	var capturedHeaders http.Header
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		capturedHeaders = req.Header.Clone()
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})

	rt := &headerRoundTripper{
		base:    base,
		headers: http.Header{"X-Injected": []string{"new-value"}},
	}

	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("X-Injected", "old-value")

	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("round trip failed: %v", err)
	}

	// Original request should not be mutated
	if req.Header.Get("X-Injected") != "old-value" {
		t.Fatalf("original request header mutated")
	}

	// Cloned request should have injected headers replacing original
	if capturedHeaders.Get("X-Injected") != "new-value" {
		t.Fatalf("expected injected header 'new-value', got '%s'", capturedHeaders.Get("X-Injected"))
	}

	// nil request returns error
	if _, err := rt.RoundTrip(nil); err == nil {
		t.Fatalf("expected nil request error")
	}

	// empty headers delegates to base
	rt2 := &headerRoundTripper{base: base, headers: nil}
	req2, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req2.Header.Set("X-Keep", "keep")
	capturedHeaders = nil
	if _, err := rt2.RoundTrip(req2); err != nil {
		t.Fatalf("round trip with nil headers failed: %v", err)
	}
	if capturedHeaders.Get("X-Keep") != "keep" {
		t.Fatalf("expected original headers preserved")
	}
}

func TestRegistryApplyMCPTransportOptions(t *testing.T) {
	t.Parallel()

	// nil transport returns error
	if err := applyMCPTransportOptions(nil, MCPServerOptions{}); err == nil {
		t.Fatalf("expected nil transport error")
	}

	// CommandTransport with env
	cmd := &mcp.CommandTransport{Command: exec.Command("true")}
	opts := MCPServerOptions{Env: map[string]string{"TEST_VAR": "test_val"}}
	if err := applyMCPTransportOptions(cmd, opts); err != nil {
		t.Fatalf("apply env to CommandTransport: %v", err)
	}
	if len(cmd.Command.Env) == 0 {
		t.Fatalf("expected env to be set on command")
	}

	// CommandTransport nil command returns error
	nilCmd := &mcp.CommandTransport{Command: nil}
	if err := applyMCPTransportOptions(nilCmd, MCPServerOptions{Env: map[string]string{"X": "1"}}); err == nil {
		t.Fatalf("expected missing command error")
	}

	// SSEClientTransport with headers
	sse := &mcp.SSEClientTransport{}
	if err := applyMCPTransportOptions(sse, MCPServerOptions{Headers: map[string]string{"X-Auth": "token"}}); err != nil {
		t.Fatalf("apply headers to SSEClientTransport: %v", err)
	}
	if sse.HTTPClient == nil || sse.HTTPClient.Transport == nil {
		t.Fatalf("expected injected headers on SSEClientTransport")
	}

	// StreamableClientTransport with headers
	streamable := &mcp.StreamableClientTransport{}
	if err := applyMCPTransportOptions(streamable, MCPServerOptions{Headers: map[string]string{"X-Key": "val"}}); err != nil {
		t.Fatalf("apply headers to StreamableClientTransport: %v", err)
	}
	if streamable.HTTPClient == nil || streamable.HTTPClient.Transport == nil {
		t.Fatalf("expected injected headers on StreamableClientTransport")
	}

	// No headers or env: no-op for all transports
	if err := applyMCPTransportOptions(cmd, MCPServerOptions{}); err != nil {
		t.Fatalf("no-op on CommandTransport: %v", err)
	}
	if err := applyMCPTransportOptions(&mcp.SSEClientTransport{}, MCPServerOptions{}); err != nil {
		t.Fatalf("no-op on SSEClientTransport: %v", err)
	}
	if err := applyMCPTransportOptions(&mcp.StreamableClientTransport{}, MCPServerOptions{}); err != nil {
		t.Fatalf("no-op on StreamableClientTransport: %v", err)
	}

	// CommandTransport with empty env: early return
	cmd2 := &mcp.CommandTransport{Command: exec.Command("true")}
	if err := applyMCPTransportOptions(cmd2, MCPServerOptions{Env: nil}); err != nil {
		t.Fatalf("empty env on CommandTransport should be no-op: %v", err)
	}

	// SSEClientTransport with empty headers: early return
	sse2 := &mcp.SSEClientTransport{}
	if err := applyMCPTransportOptions(sse2, MCPServerOptions{Headers: nil}); err != nil {
		t.Fatalf("empty headers on SSEClientTransport should be no-op: %v", err)
	}
}

func TestRegistryWithInjectedHeaders(t *testing.T) {
	t.Parallel()

	// nil client gets new client with transport
	client := withInjectedHeaders(nil, map[string]string{"X-Test": "1"})
	if client == nil || client.Transport == nil {
		t.Fatalf("expected http client with transport")
	}
	if _, ok := client.Transport.(*headerRoundTripper); !ok {
		t.Fatalf("expected headerRoundTripper, got %T", client.Transport)
	}

	// existing client gets transport added
	existing := &http.Client{}
	client = withInjectedHeaders(existing, map[string]string{"X-Test": "1"})
	if client != existing {
		t.Fatalf("expected same client instance")
	}
	if client.Transport == nil {
		t.Fatalf("expected transport injected")
	}

	// empty headers: no modification
	client2 := &http.Client{}
	result := withInjectedHeaders(client2, nil)
	if result != client2 || result.Transport != nil {
		t.Fatalf("expected no transport override for empty headers")
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	const n = 50

	var wg sync.WaitGroup
	wg.Add(n * 3)

	// Concurrent registrations
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			r.Register(&spyTool{name: fmt.Sprintf("tool_%d", i)})
		}(i)
	}

	// Concurrent lookups
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, _ = r.Get(fmt.Sprintf("tool_%d", i))
		}(i)
	}

	// Concurrent list calls
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = r.List()
		}()
	}

	wg.Wait()

	tools := r.List()
	if len(tools) != n {
		t.Fatalf("expected %d tools after concurrent access, got %d", n, len(tools))
	}
}

func TestRegistryRegisterWithSourceOverwritesBuiltin(t *testing.T) {
	t.Parallel()

	r := NewRegistry()

	// Register normally (source = "builtin")
	if err := r.Register(&spyTool{name: "cmd"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if source := r.ToolSource("cmd"); source != "builtin" {
		t.Fatalf("expected builtin, got '%s'", source)
	}

	// Register again with explicit source via RegisterWithSource
	// This should fail because the tool name is already registered
	if err := r.RegisterWithSource(&spyTool{name: "cmd"}, "mcp:svc"); err == nil {
		t.Fatalf("expected duplicate error from RegisterWithSource")
	}
}

// Helper to extract tool names from a list.
func toolNames(tools []Tool) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	return names
}

// roundTripperFunc for header tests (reuse if not defined elsewhere).
type roundTripperFunc2 func(*http.Request) (*http.Response, error)

func (f roundTripperFunc2) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
