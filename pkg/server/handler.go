package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/canvas"
	coreevents "github.com/saker-ai/saker/pkg/core/events"
	"github.com/saker-ai/saker/pkg/project"
	storagecfg "github.com/saker-ai/saker/pkg/storage"
	"github.com/google/uuid"
	"github.com/mojatter/s2"
)

const (
	// defaultTurnTimeout is the maximum duration a turn can run before being
	// automatically cancelled. Prevents orphaned turns from running forever.
	defaultTurnTimeout = 45 * time.Minute

	// approvalTimeout is the maximum time to wait for a user to respond to
	// an approval request before automatically denying it.
	approvalTimeout = 5 * time.Minute
)

// Handler processes JSON-RPC methods and manages WebSocket subscriptions.
type Handler struct {
	runtime  *api.Runtime
	sessions *SessionStore
	dataDir  string
	logger   *slog.Logger
	dispatch map[string]rpcHandler // method → handler (populated by initDispatch)

	mu          sync.RWMutex
	clients     map[string]*wsClient            // clientID → client
	subscribers map[string]map[string]*wsClient // threadID → clientID → client

	// Pending approval channels: approvalID → result chan
	approvalMu sync.Mutex
	approvals  map[string]chan coreevents.PermissionDecisionType

	// Pending question channels: questionID → answers chan
	questionMu sync.Mutex
	questions  map[string]chan map[string]string

	// Active turn cancellation
	cancelMu    sync.Mutex
	cancels     map[string]context.CancelFunc // turnID → cancel
	turnThreads map[string]string             // turnID → threadID (for thread-scoped interrupt)

	// Cron & active turns (set after construction via SetCron/SetTracker)
	cronStore *CronStore
	scheduler *Scheduler
	tracker   *ActiveTurnTracker
	auth      *AuthManager

	// Background tool task tracker
	taskTracker *TaskTracker

	// Canvas DAG executor — lazy-initialised on first canvas/execute call
	// (see ensureCanvasExecutor in canvas_execute_handler.go).
	canvasExecutor *canvas.Executor

	// Media cache dedup: prevents concurrent downloads of the same URL
	// and remembers recently failed URLs to avoid retry storms.
	cacheInflight sync.Map      // URL → struct{} (in-flight downloads)
	cacheFailed   sync.Map      // URL → time.Time (failure cooldown until)
	stopCacheCh   chan struct{} // stops cacheFailed cleanup goroutine

	// Settings file mutation lock — serialises load-modify-save cycles
	// for settings.local.json and channels.json to prevent TOCTOU races.
	settingsMu sync.Mutex

	// Multi-tenant project store. nil when running in legacy single-project
	// mode (no project.Store wired in via Options) — useful for embedded
	// uses (tests, library mode) that don't need projects at all.
	projects *project.Store

	// Per-project component caches. Built on first multi-tenant hit (see
	// initPerProjectRegistries). Both stay nil while the scope middleware
	// is gated off, so single-project deployments pay no cost.
	sessionRegistry    *project.ComponentRegistry[*SessionStore]
	canvasExecRegistry *project.ComponentRegistry[*canvas.Executor]

	// canvasRunPins keeps the per-project canvas Executor pinned in the
	// registry while a RunAsync goroutine is in flight. Keyed by runId,
	// the value is the release callback returned by Acquire. The Notify
	// callback (canvas/run-finished) drains entries here.
	canvasRunPins sync.Map // map[string]func()

	// appTempThreads bridges the apps.Runner Save → canvas/run-finished
	// Notify pipeline: keys are threadIDs of "app-run-*" temp documents the
	// runner wrote into a per-scope dataDir; values are that dataDir so the
	// Notify hook knows where to delete the file once the run terminates.
	// See pkg/server/apps_temp_gc.go.
	appTempThreads sync.Map // map[string]string  threadID → dataDir

	// Pluggable media object storage. nil = legacy on-disk path (writes
	// to <projectRoot>/.saker/canvas-media). When non-nil, cacheArtifactMedia
	// routes new writes through s2.Storage and returns /media/<key> URLs.
	//
	// objectMu guards both fields. Read paths must use objectStoreSnapshot()
	// so a settings/update-driven reload can swap the backend live without
	// tearing in-flight requests.
	objectMu       sync.RWMutex
	objectStore    s2.Storage
	objectStoreCfg storagecfg.Config

	// storageReloader, when set, rebuilds the object store from the latest
	// settings. Wired by the Server in New() so handleSettingsUpdate can
	// trigger a hot swap when settings.storage changes.
	storageReloader func(ctx context.Context) error
}

// SetStorage attaches (or replaces) the media object store. Safe for
// concurrent reconfiguration: reads on the hot path go through
// objectStoreSnapshot which holds an RLock for the duration of the lookup.
func (h *Handler) SetStorage(st s2.Storage, cfg storagecfg.Config) {
	h.objectMu.Lock()
	h.objectStore = st
	h.objectStoreCfg = cfg
	h.objectMu.Unlock()
}

// objectStoreSnapshot returns the current store + config under an RLock.
// Returned values are safe to use after the lock is dropped because s2.Storage
// is goroutine-safe and storagecfg.Config is a value type.
func (h *Handler) objectStoreSnapshot() (s2.Storage, storagecfg.Config) {
	h.objectMu.RLock()
	defer h.objectMu.RUnlock()
	return h.objectStore, h.objectStoreCfg
}

// SetStorageReloader wires the callback that reloads the object store from
// the latest settings. Safe to call before/after ListenAndServe.
func (h *Handler) SetStorageReloader(fn func(ctx context.Context) error) {
	h.storageReloader = fn
}

// wsClient represents a connected WebSocket client.
type wsClient struct {
	id   string
	send func(msg any) error
}

func newHandler(runtime *api.Runtime, sessions *SessionStore, dataDir string, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	h := &Handler{
		runtime:     runtime,
		sessions:    sessions,
		dataDir:     dataDir,
		logger:      logger,
		clients:     make(map[string]*wsClient),
		subscribers: make(map[string]map[string]*wsClient),
		approvals:   make(map[string]chan coreevents.PermissionDecisionType),
		questions:   make(map[string]chan map[string]string),
		cancels:     make(map[string]context.CancelFunc),
		turnThreads: make(map[string]string),
		taskTracker: NewTaskTracker(),
		stopCacheCh: make(chan struct{}),
	}
	h.dispatch = h.initDispatch()
	go h.cacheFailedCleanupLoop()
	return h
}

// SetCron sets the cron store and scheduler for RPC handling.
func (h *Handler) SetCron(store *CronStore, scheduler *Scheduler) {
	h.cronStore = store
	h.scheduler = scheduler
}

// Close stops the background cacheFailed cleanup goroutine.
func (h *Handler) Close() {
	if h.stopCacheCh != nil {
		close(h.stopCacheCh)
	}
}

// cacheFailedCleanupLoop periodically removes expired entries from the
// cacheFailed sync.Map (URLs whose 10-minute failure cooldown has elapsed).
func (h *Handler) cacheFailedCleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-h.stopCacheCh:
			return
		case <-ticker.C:
			now := time.Now()
			h.cacheFailed.Range(func(key, value any) bool {
				if expiry, ok := value.(time.Time); ok && now.After(expiry) {
					h.cacheFailed.Delete(key)
				}
				return true
			})
		}
	}
}

// SetAuth sets the auth manager for RPC handling.
func (h *Handler) SetAuth(auth *AuthManager) {
	h.auth = auth
}

// SetTracker sets the active turn tracker.
func (h *Handler) SetTracker(tracker *ActiveTurnTracker) {
	h.tracker = tracker
}

// SetProjects wires the multi-tenant project store. Safe to pass a nil
// store: the scope middleware stays disabled and all RPCs run in legacy
// single-project mode (used by embedded library callers and tests that
// don't need multi-tenancy).
func (h *Handler) SetProjects(store *project.Store) {
	h.projects = store
}

// RegisterClient adds a new WebSocket client.
func (h *Handler) RegisterClient(sendFn func(msg any) error) string {
	id := uuid.New().String()
	h.mu.Lock()
	h.clients[id] = &wsClient{id: id, send: sendFn}
	h.mu.Unlock()
	return id
}

// UnregisterClient removes a client and its subscriptions.
func (h *Handler) UnregisterClient(clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, clientID)
	for threadID, subs := range h.subscribers {
		delete(subs, clientID)
		if len(subs) == 0 {
			delete(h.subscribers, threadID)
		}
	}
}

// rpcHandler is the unified signature for all dispatch table entries.
// Handlers that don't need ctx or clientID simply ignore those parameters.
type rpcHandler func(ctx context.Context, clientID string, req Request) Response

// initDispatch builds the method→handler lookup table used by HandleRequest.
// Each entry adapts the underlying handler's signature to the unified rpcHandler
// type — handlers that don't need ctx or clientID receive them but ignore them.
func (h *Handler) initDispatch() map[string]rpcHandler {
	return map[string]rpcHandler{
		"initialize": func(_ context.Context, clientID string, req Request) Response {
			return h.success(req.ID, map[string]any{"clientId": clientID})
		},

		// Thread
		"thread/list":        h.adaptCtx(h.handleThreadList),
		"thread/create":      h.adaptCtx(h.handleThreadCreate),
		"thread/update":      h.adaptCtx(h.handleThreadUpdate),
		"thread/delete":      h.adaptCtx(h.handleThreadDelete),
		"thread/subscribe":   h.handleThreadSubscribe,
		"thread/unsubscribe": h.adaptClientID(h.handleThreadUnsubscribe),
		"thread/history":     h.adaptCtx(h.handleThreadHistory),

		// Turn
		"turn/send":        h.handleTurnSend,
		"turn/cancel":      h.adaptNone(h.handleTurnCancel),
		"thread/interrupt": h.adaptNone(h.handleThreadInterrupt),

		// Approval & question
		"approval/respond": h.adaptNone(h.handleApprovalRespond),
		"question/respond": h.adaptNone(h.handleQuestionRespond),

		// Skill
		"skill/list":              h.adaptNone(h.handleSkillList),
		"skill/remove":            h.adaptNone(h.handleSkillRemove),
		"skill/promote":           h.adaptNone(h.handleSkillPromote),
		"skill/content":           h.adaptNone(h.handleSkillContent),
		"skill/patch":             h.adaptNone(h.handleSkillPatch),
		"skill/import-preview":    h.adaptNone(h.handleSkillImportPreview),
		"skill/import":            h.adaptNone(h.handleSkillImport),
		"skill/analytics":         h.adaptNone(h.handleSkillAnalytics),
		"skill/analytics/history": h.adaptNone(h.handleSkillAnalyticsHistory),

		// Config & settings
		"config/get":      h.adaptNone(h.handleConfigGet),
		"settings/get":    h.adaptNone(h.handleSettingsGet),
		"settings/update": h.adaptCtx(h.handleSettingsUpdate),

		// Stats & sessions
		"stats/session":   h.adaptNone(h.handleStatsSession),
		"stats/total":     h.adaptNone(h.handleStatsTotal),
		"sessions/search": h.adaptCtx(h.handleSessionsSearch),
		"sessions/list":   h.adaptCtx(h.handleSessionsList),

		// Model
		"model/switch": h.adaptCtx(h.handleModelSwitch),

		// Auth & user
		"auth/update":          h.adaptCtx(h.handleAuthUpdate),
		"auth/delete":          h.adaptCtx(h.handleAuthDelete),
		"user/list":            h.adaptCtx(h.handleUserList),
		"user/create":          h.adaptCtx(h.handleUserCreate),
		"user/delete":          h.adaptCtx(h.handleUserDelete),
		"user/me":              h.adaptCtx(h.handleUserMe),
		"user/update-password": h.adaptCtx(h.handleUserUpdatePassword),

		// Canvas
		"canvas/save":       h.adaptCtx(h.handleCanvasSave),
		"canvas/load":       h.adaptCtx(h.handleCanvasLoad),
		"canvas/text-gen":   h.adaptCtx(h.handleCanvasTextGen),
		"canvas/execute":    h.adaptCtx(h.handleCanvasExecute),
		"canvas/run-status": h.adaptCtx(h.handleCanvasRunStatus),
		"canvas/run-cancel": h.adaptCtx(h.handleCanvasRunCancel),

		// Media
		"media/cache":    h.adaptNone(h.handleMediaCache),
		"media/data_url": h.adaptNone(h.handleMediaDataURL),

		// Cron
		"cron/list":   h.adaptNone(h.handleCronList),
		"cron/add":    h.adaptNone(h.handleCronAdd),
		"cron/update": h.adaptNone(h.handleCronUpdate),
		"cron/remove": h.adaptNone(h.handleCronRemove),
		"cron/toggle": h.adaptNone(h.handleCronToggle),
		"cron/run":    h.adaptNone(h.handleCronRun),
		"cron/runs":   h.adaptNone(h.handleCronRuns),
		"cron/status": h.adaptNone(h.handleCronStatus),

		// Turns & tools
		"turns/active":      h.adaptNone(h.handleTurnsActive),
		"tool/run":          h.adaptCtx(h.handleToolRun),
		"tool/task-status":  h.adaptNone(h.handleToolTaskStatus),
		"tool/active-tasks": h.adaptNone(h.handleToolActiveTasks),
		"tool/schema":       h.adaptNone(h.handleToolSchema),

		// Aigo
		"aigo/models": func(_ context.Context, _ string, req Request) Response {
			return h.success(req.ID, h.runtime.AigoModels())
		},
		"aigo/providers": func(_ context.Context, _ string, req Request) Response {
			return h.success(req.ID, h.runtime.AigoProviders())
		},
		"aigo/status": func(_ context.Context, _ string, req Request) Response {
			return h.success(req.ID, h.runtime.AigoProviderStatus())
		},

		// Monitor
		"monitor/list":  h.adaptNone(h.handleMonitorList),
		"monitor/start": h.adaptCtx(h.handleMonitorStart),
		"monitor/stop":  h.adaptCtx(h.handleMonitorStop),

		// Persona
		"persona/list":            h.adaptNone(h.handlePersonaList),
		"persona/save":            h.adaptCtx(h.handlePersonaSave),
		"persona/delete":          h.adaptCtx(h.handlePersonaDelete),
		"persona/set-default":     h.adaptCtx(h.handlePersonaSetDefault),
		"persona/user-list":       h.adaptCtx(h.handleUserPersonaList),
		"persona/user-save":       h.adaptCtx(h.handleUserPersonaSave),
		"persona/user-delete":     h.adaptCtx(h.handleUserPersonaDelete),
		"persona/user-set-active": h.adaptCtx(h.handleUserPersonaSetActive),

		// Channels
		"channels/list":      h.adaptCtx(h.handleChannelsList),
		"channels/save":      h.adaptCtx(h.handleChannelsSave),
		"channels/delete":    h.adaptCtx(h.handleChannelsDelete),
		"channels/toggle":    h.adaptCtx(h.handleChannelsToggle),
		"channels/route-set": h.adaptCtx(h.handleChannelsRouteSet),

		// Profile
		"profile/list": h.adaptCtx(h.handleProfileList),

		// Memory
		"memory/list":   h.adaptNone(h.handleMemoryList),
		"memory/read":   h.adaptNone(h.handleMemoryRead),
		"memory/delete": h.adaptNone(h.handleMemoryDelete),

		// Project
		"project/list":               h.adaptCtx(h.handleProjectList),
		"project/create":             h.adaptCtx(h.handleProjectCreate),
		"project/get":                h.adaptCtx(h.handleProjectGet),
		"project/update":             h.adaptCtx(h.handleProjectUpdate),
		"project/delete":             h.adaptCtx(h.handleProjectDelete),
		"project/transfer":           h.adaptCtx(h.handleProjectTransfer),
		"project/me":                 h.adaptCtx(h.handleProjectMe),
		"project/invite":             h.adaptCtx(h.handleProjectInvite),
		"project/invite/list":        h.adaptCtx(h.handleProjectInviteList),
		"project/invite/cancel":      h.adaptCtx(h.handleProjectInviteCancel),
		"project/invite/list-for-me": h.adaptCtx(h.handleProjectInviteListForMe),
		"project/invite/accept":      h.adaptCtx(h.handleProjectInviteAccept),
		"project/invite/decline":     h.adaptCtx(h.handleProjectInviteDecline),
		"project/member/list":        h.adaptCtx(h.handleProjectMemberList),
		"project/member/update-role": h.adaptCtx(h.handleProjectMemberUpdateRole),
		"project/member/remove":      h.adaptCtx(h.handleProjectMemberRemove),

		// Team
		"team/list":        h.adaptCtx(h.handleTeamList),
		"team/create":      h.adaptCtx(h.handleTeamCreate),
		"team/delete":      h.adaptCtx(h.handleTeamDelete),
		"team/member/list": h.adaptCtx(h.handleTeamMemberList),
	}
}

// Signature adapters — thin closures that bridge handler methods with fewer
// parameters to the unified rpcHandler signature.

// adaptCtx wraps a handler(ctx, req) → Response as a rpcHandler.
func (h *Handler) adaptCtx(fn func(context.Context, Request) Response) rpcHandler {
	return func(ctx context.Context, _ string, req Request) Response { return fn(ctx, req) }
}

// adaptNone wraps a handler(req) → Response as a rpcHandler.
func (h *Handler) adaptNone(fn func(Request) Response) rpcHandler {
	return func(_ context.Context, _ string, req Request) Response { return fn(req) }
}

// adaptClientID wraps a handler(clientID, req) → Response as a rpcHandler.
func (h *Handler) adaptClientID(fn func(string, Request) Response) rpcHandler {
	return func(_ context.Context, clientID string, req Request) Response { return fn(clientID, req) }
}

// HandleRequest dispatches a JSON-RPC request to the appropriate handler.
//
// Every call emits one structured log line at completion with method, client,
// duration_ms, and (on failure) error code + message. Successful calls log at
// Info; failures escalate to Warn so log filters surface them by default.
// This is the minimum signal needed to trace a stuck request, audit who
// invoked what, and spot regressions in handler latency without pulling in a
// full tracing dependency.
func (h *Handler) HandleRequest(ctx context.Context, clientID string, req Request) (resp Response) {
	start := time.Now()
	h.logger.Debug("rpc request", "method", req.Method, "client_id", clientID, "request_id", req.ID)
	defer func() {
		attrs := []any{
			"method", req.Method,
			"client_id", clientID,
			"request_id", req.ID,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if resp.Error != nil {
			attrs = append(attrs, "error_code", resp.Error.Code, "error_message", resp.Error.Message)
			h.logger.Warn("rpc complete", attrs...)
			return
		}
		h.logger.Info("rpc complete", attrs...)
	}()
	// Resolve the project scope (membership + role) before dispatching. When
	// the scope middleware is gated off this is a no-op and ctx is unchanged.
	if newCtx, denyResp := h.resolveScope(ctx, req); denyResp != nil {
		return *denyResp
	} else {
		ctx = newCtx
	}
	// Skillhub methods (skillhub/*) are routed via a sub-dispatcher to keep
	// the main switch flat. Returns ok=true when the method matched.
	if strings.HasPrefix(req.Method, "skillhub/") {
		if resp, ok := h.dispatchSkillhub(ctx, req); ok {
			return resp
		}
	}
	// Dispatch via the method lookup table.
	if handler, ok := h.dispatch[req.Method]; ok {
		return handler(ctx, clientID, req)
	}
	h.logger.Warn("unknown rpc method", "method", req.Method, "client_id", clientID)
	return h.methodNotFound(req.ID, req.Method)
}

// --- Notification helpers ---

func (h *Handler) notifySubscribers(threadID, method string, params any) {
	h.mu.RLock()
	subs := h.subscribers[threadID]
	// Copy to avoid holding lock during send.
	clients := make([]*wsClient, 0, len(subs))
	for _, c := range subs {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	msg := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	for _, c := range clients {
		_ = c.send(msg) // best-effort
	}
}

// notifyAllClients sends a notification to all connected clients (not thread-scoped).
func (h *Handler) notifyAllClients(method string, params any) {
	h.mu.RLock()
	clients := make([]*wsClient, 0, len(h.clients))
	for _, c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	msg := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	for _, c := range clients {
		_ = c.send(msg)
	}
}

// --- Response builders ---

func (h *Handler) success(id any, result any) Response {
	return Response{JSONRPC: "2.0", ID: id, Result: result}
}

func (h *Handler) methodNotFound(id any, method string) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: ErrCodeMethodNotFound, Message: fmt.Sprintf("method not found: %s", method)},
	}
}

func (h *Handler) invalidParams(id any, msg string) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: ErrCodeInvalidParams, Message: msg},
	}
}

func (h *Handler) internalError(id any, msg string) Response {
	return Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: ErrCodeInternal, Message: msg},
	}
}
