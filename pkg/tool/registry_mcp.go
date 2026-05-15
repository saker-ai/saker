package tool

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/mcp"
)

type mcpListChangedHandler = func(context.Context, *mcp.ClientSession)

var newMCPClient = func(ctx context.Context, spec string, handler mcpListChangedHandler) (*mcp.ClientSession, error) {
	return connectMCPClientWithOptions(ctx, spec, MCPServerOptions{}, handler)
}

// MCPServerOptions configures an MCP server connection (headers, env, timeouts,
// per-server tool allowlist/blocklist).
type MCPServerOptions struct {
	Headers       map[string]string
	Env           map[string]string
	Timeout       time.Duration
	EnabledTools  []string
	DisabledTools []string
	ToolTimeout   time.Duration
}

var newMCPClientWithOptions = func(ctx context.Context, spec string, opts MCPServerOptions, handler mcpListChangedHandler) (*mcp.ClientSession, error) {
	return connectMCPClientWithOptions(ctx, spec, opts, handler)
}

// RegisterMCPServer discovers tools exposed by an MCP server and registers them.
// serverPath accepts either an http(s) URL (SSE transport) or a stdio command.
func (r *Registry) RegisterMCPServer(ctx context.Context, serverPath, serverName string) error {
	return r.RegisterMCPServerWithOptions(ctx, serverPath, serverName, MCPServerOptions{})
}

func (r *Registry) RegisterMCPServerWithOptions(ctx context.Context, serverPath, serverName string, opts MCPServerOptions) error {
	ctx = nonNilContext(ctx)
	if strings.TrimSpace(serverPath) == "" {
		return fmt.Errorf("server path is empty")
	}
	serverName = strings.TrimSpace(serverName)

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session, err := newMCPClientWithOptions(connectCtx, serverPath, opts, r.mcpToolsChangedHandler(serverPath))
	if err != nil {
		if ctxErr := connectCtx.Err(); ctxErr != nil {
			return fmt.Errorf("connect MCP client: %w", ctxErr)
		}
		return fmt.Errorf("connect MCP client: %w", err)
	}
	if session == nil {
		return fmt.Errorf("connect MCP client: session is nil")
	}
	success := false
	defer func() {
		if !success {
			_ = session.Close()
		}
	}()

	if err := connectCtx.Err(); err != nil {
		return fmt.Errorf("initialize MCP client: connect context: %w", err)
	}
	if session.InitializeResult() == nil {
		return fmt.Errorf("initialize MCP client: mcp session missing initialize result")
	}
	if err := connectCtx.Err(); err != nil {
		return fmt.Errorf("connect MCP client: %w", err)
	}

	listCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var tools []*mcp.Tool
	for tool, iterErr := range session.Tools(listCtx, nil) {
		if iterErr != nil {
			return fmt.Errorf("list MCP tools: %w", iterErr)
		}
		tools = append(tools, tool)
	}
	if len(tools) == 0 {
		return fmt.Errorf("MCP server returned no tools")
	}

	wrappers, names, err := buildRemoteToolWrappers(session, serverName, tools, opts)
	if err != nil {
		return err
	}
	if err := r.registerMCPSession(serverPath, serverName, session, wrappers, names, opts); err != nil {
		return err
	}

	success = true
	return nil
}

// connectMCPSession is the seam tests can replace to inject fake sessions
// without depending on a live MCP server.
var connectMCPSession = mcp.ConnectSessionWithOptions

func connectMCPClientWithOptions(ctx context.Context, spec string, opts MCPServerOptions, handler mcpListChangedHandler) (*mcp.ClientSession, error) {
	connectOpts := []mcp.ConnectOption{
		mcp.WithTransportConfigurer(func(t mcp.Transport) error {
			return applyMCPTransportOptions(t, opts)
		}),
	}
	if handler != nil {
		connectOpts = append(connectOpts, mcp.WithToolListChangedHandler(handler))
	}
	return connectMCPSession(ctx, spec, connectOpts...)
}

type mcpSessionInfo struct {
	serverID   string
	serverName string
	sessionID  string
	session    *mcp.ClientSession
	toolNames  map[string]struct{}
	opts       MCPServerOptions
}

func (r *Registry) registerMCPSession(serverID, serverName string, session *mcp.ClientSession, wrappers []Tool, names []string, opts MCPServerOptions) error {
	if session == nil {
		return fmt.Errorf("mcp session is nil")
	}
	if len(wrappers) != len(names) {
		return fmt.Errorf("mcp tools mismatch")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, name := range names {
		if _, exists := r.tools[name]; exists {
			return fmt.Errorf("tool %s already registered", name)
		}
	}
	mcpSource := "mcp"
	if serverName != "" {
		mcpSource = "mcp:" + serverName
	}
	for i, tool := range wrappers {
		r.tools[names[i]] = tool
		r.sources[names[i]] = mcpSource
	}
	info := &mcpSessionInfo{
		serverID:   strings.TrimSpace(serverID),
		serverName: strings.TrimSpace(serverName),
		sessionID:  session.ID(),
		session:    session,
		toolNames:  toNameSet(names),
		opts:       cloneMCPServerOptions(opts),
	}
	r.mcpSessions = append(r.mcpSessions, info)
	return nil
}

func (r *Registry) mcpToolsChangedHandler(serverID string) mcpListChangedHandler {
	if r == nil {
		return nil
	}
	serverID = strings.TrimSpace(serverID)
	return func(ctx context.Context, session *mcp.ClientSession) {
		sessionID := ""
		if session != nil {
			sessionID = session.ID()
		}
		go func() {
			refreshCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := r.refreshMCPTools(refreshCtx, serverID, sessionID); err != nil {
				slog.Error("tool registry: refresh MCP tools", "error", err)
			}
		}()
	}
}

func (r *Registry) refreshMCPTools(ctx context.Context, serverID, sessionID string) error {
	if r == nil {
		return fmt.Errorf("registry is nil")
	}
	serverID = strings.TrimSpace(serverID)
	sessionID = strings.TrimSpace(sessionID)

	var (
		serverName string
		session    *mcp.ClientSession
		opts       MCPServerOptions
	)
	r.mu.RLock()
	for _, info := range r.mcpSessions {
		if info == nil {
			continue
		}
		if sessionID != "" && info.sessionID == sessionID {
			serverName = info.serverName
			session = info.session
			opts = cloneMCPServerOptions(info.opts)
			break
		}
		if session == nil && serverID != "" && info.serverID == serverID {
			serverName = info.serverName
			session = info.session
			opts = cloneMCPServerOptions(info.opts)
		}
	}
	r.mu.RUnlock()
	if session == nil {
		return fmt.Errorf("mcp session not found")
	}

	listCtx, cancel := context.WithTimeout(nonNilContext(ctx), 10*time.Second)
	defer cancel()

	var tools []*mcp.Tool
	for tool, iterErr := range session.Tools(listCtx, nil) {
		if iterErr != nil {
			return fmt.Errorf("list MCP tools: %w", iterErr)
		}
		tools = append(tools, tool)
	}
	if len(tools) == 0 {
		return fmt.Errorf("MCP server returned no tools")
	}

	wrappers, names, err := buildRemoteToolWrappers(session, serverName, tools, opts)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	info := r.findMCPSessionLocked(serverID, sessionID)
	if info == nil {
		return fmt.Errorf("mcp session not tracked")
	}
	for _, name := range names {
		if _, exists := r.tools[name]; exists {
			if info.toolNames == nil {
				return fmt.Errorf("tool %s already registered", name)
			}
			if _, ok := info.toolNames[name]; !ok {
				return fmt.Errorf("tool %s already registered", name)
			}
		}
	}
	for name := range info.toolNames {
		delete(r.tools, name)
	}
	for i, tool := range wrappers {
		r.tools[names[i]] = tool
	}
	info.toolNames = toNameSet(names)
	if info.sessionID == "" {
		info.sessionID = session.ID()
	}
	if info.serverID == "" {
		info.serverID = serverID
	}
	if info.serverName == "" {
		info.serverName = serverName
	}
	return nil
}

func (r *Registry) findMCPSessionLocked(serverID, sessionID string) *mcpSessionInfo {
	serverID = strings.TrimSpace(serverID)
	sessionID = strings.TrimSpace(sessionID)
	for _, info := range r.mcpSessions {
		if info == nil {
			continue
		}
		if sessionID != "" && info.sessionID == sessionID {
			return info
		}
		if info.sessionID == "" && info.session != nil && sessionID != "" && info.session.ID() == sessionID {
			return info
		}
		if serverID != "" && info.serverID == serverID {
			return info
		}
	}
	return nil
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}
