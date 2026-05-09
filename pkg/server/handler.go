package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/canvas"
	coreevents "github.com/cinience/saker/pkg/core/events"
	"github.com/cinience/saker/pkg/project"
	storagecfg "github.com/cinience/saker/pkg/storage"
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
	cacheInflight sync.Map // URL → struct{} (in-flight downloads)
	cacheFailed   sync.Map // URL → time.Time (failure cooldown until)
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
	switch req.Method {
	case "initialize":
		return h.success(req.ID, map[string]any{"clientId": clientID})
	case "thread/list":
		return h.handleThreadList(ctx, req)
	case "thread/create":
		return h.handleThreadCreate(ctx, req)
	case "thread/update":
		return h.handleThreadUpdate(ctx, req)
	case "thread/delete":
		return h.handleThreadDelete(ctx, req)
	case "thread/subscribe":
		return h.handleThreadSubscribe(ctx, clientID, req)
	case "thread/unsubscribe":
		return h.handleThreadUnsubscribe(clientID, req)
	case "thread/history":
		return h.handleThreadHistory(ctx, req)
	case "turn/send":
		return h.handleTurnSend(ctx, clientID, req)
	case "turn/cancel":
		return h.handleTurnCancel(req)
	case "thread/interrupt":
		return h.handleThreadInterrupt(req)
	case "approval/respond":
		return h.handleApprovalRespond(req)
	case "question/respond":
		return h.handleQuestionRespond(req)
	case "skill/list":
		return h.handleSkillList(req)
	case "skill/remove":
		return h.handleSkillRemove(req)
	case "skill/promote":
		return h.handleSkillPromote(req)
	case "skill/content":
		return h.handleSkillContent(req)
	case "skill/patch":
		return h.handleSkillPatch(req)
	case "skill/import-preview":
		return h.handleSkillImportPreview(req)
	case "skill/import":
		return h.handleSkillImport(req)
	case "skill/analytics":
		return h.handleSkillAnalytics(req)
	case "skill/analytics/history":
		return h.handleSkillAnalyticsHistory(req)
	case "config/get":
		return h.handleConfigGet(req)
	case "settings/get":
		return h.handleSettingsGet(req)
	case "settings/update":
		return h.handleSettingsUpdate(ctx, req)
	case "stats/session":
		return h.handleStatsSession(req)
	case "stats/total":
		return h.handleStatsTotal(req)
	case "sessions/search":
		return h.handleSessionsSearch(req)
	case "sessions/list":
		return h.handleSessionsList(req)
	case "model/switch":
		return h.handleModelSwitch(req)
	case "auth/update":
		return h.handleAuthUpdate(ctx, req)
	case "auth/delete":
		return h.handleAuthDelete(ctx, req)
	case "canvas/save":
		return h.handleCanvasSave(ctx, req)
	case "canvas/load":
		return h.handleCanvasLoad(ctx, req)
	case "canvas/text-gen":
		return h.handleCanvasTextGen(ctx, req)
	case "canvas/execute":
		return h.handleCanvasExecute(ctx, req)
	case "canvas/run-status":
		return h.handleCanvasRunStatus(ctx, req)
	case "canvas/run-cancel":
		return h.handleCanvasRunCancel(ctx, req)
	case "media/cache":
		return h.handleMediaCache(req)
	case "media/data_url":
		return h.handleMediaDataURL(req)
	case "cron/list":
		return h.handleCronList(req)
	case "cron/add":
		return h.handleCronAdd(req)
	case "cron/update":
		return h.handleCronUpdate(req)
	case "cron/remove":
		return h.handleCronRemove(req)
	case "cron/toggle":
		return h.handleCronToggle(req)
	case "cron/run":
		return h.handleCronRun(req)
	case "cron/runs":
		return h.handleCronRuns(req)
	case "cron/status":
		return h.handleCronStatus(req)
	case "turns/active":
		return h.handleTurnsActive(req)
	case "tool/run":
		return h.handleToolRun(ctx, req)
	case "tool/task-status":
		return h.handleToolTaskStatus(req)
	case "tool/active-tasks":
		return h.handleToolActiveTasks(req)
	case "tool/schema":
		return h.handleToolSchema(req)
	case "aigo/models":
		return h.success(req.ID, h.runtime.AigoModels())
	case "aigo/providers":
		return h.success(req.ID, h.runtime.AigoProviders())
	case "aigo/status":
		return h.success(req.ID, h.runtime.AigoProviderStatus())
	case "monitor/list":
		return h.handleMonitorList(req)
	case "monitor/start":
		return h.handleMonitorStart(ctx, req)
	case "monitor/stop":
		return h.handleMonitorStop(ctx, req)
	case "user/list":
		return h.handleUserList(ctx, req)
	case "user/create":
		return h.handleUserCreate(ctx, req)
	case "user/delete":
		return h.handleUserDelete(ctx, req)
	case "user/me":
		return h.handleUserMe(ctx, req)
	case "user/update-password":
		return h.handleUserUpdatePassword(ctx, req)
	case "persona/list":
		return h.handlePersonaList(req)
	case "persona/save":
		return h.handlePersonaSave(ctx, req)
	case "persona/delete":
		return h.handlePersonaDelete(ctx, req)
	case "persona/set-default":
		return h.handlePersonaSetDefault(ctx, req)
	case "persona/user-list":
		return h.handleUserPersonaList(ctx, req)
	case "persona/user-save":
		return h.handleUserPersonaSave(ctx, req)
	case "persona/user-delete":
		return h.handleUserPersonaDelete(ctx, req)
	case "persona/user-set-active":
		return h.handleUserPersonaSetActive(ctx, req)
	case "channels/list":
		return h.handleChannelsList(ctx, req)
	case "channels/save":
		return h.handleChannelsSave(ctx, req)
	case "channels/delete":
		return h.handleChannelsDelete(ctx, req)
	case "channels/toggle":
		return h.handleChannelsToggle(ctx, req)
	case "channels/route-set":
		return h.handleChannelsRouteSet(ctx, req)
	case "profile/list":
		return h.handleProfileList(ctx, req)
	case "memory/list":
		return h.handleMemoryList(req)
	case "memory/read":
		return h.handleMemoryRead(req)
	case "memory/delete":
		return h.handleMemoryDelete(req)
	case "project/list":
		return h.handleProjectList(ctx, req)
	case "project/create":
		return h.handleProjectCreate(ctx, req)
	case "project/get":
		return h.handleProjectGet(ctx, req)
	case "project/update":
		return h.handleProjectUpdate(ctx, req)
	case "project/delete":
		return h.handleProjectDelete(ctx, req)
	case "project/transfer":
		return h.handleProjectTransfer(ctx, req)
	case "project/me":
		return h.handleProjectMe(ctx, req)
	case "project/invite":
		return h.handleProjectInvite(ctx, req)
	case "project/invite/list":
		return h.handleProjectInviteList(ctx, req)
	case "project/invite/cancel":
		return h.handleProjectInviteCancel(ctx, req)
	case "project/invite/list-for-me":
		return h.handleProjectInviteListForMe(ctx, req)
	case "project/invite/accept":
		return h.handleProjectInviteAccept(ctx, req)
	case "project/invite/decline":
		return h.handleProjectInviteDecline(ctx, req)
	case "project/member/list":
		return h.handleProjectMemberList(ctx, req)
	case "project/member/update-role":
		return h.handleProjectMemberUpdateRole(ctx, req)
	case "project/member/remove":
		return h.handleProjectMemberRemove(ctx, req)
	case "team/list":
		return h.handleTeamList(ctx, req)
	case "team/create":
		return h.handleTeamCreate(ctx, req)
	case "team/delete":
		return h.handleTeamDelete(ctx, req)
	case "team/member/list":
		return h.handleTeamMemberList(ctx, req)
	default:
		h.logger.Warn("unknown rpc method", "method", req.Method, "client_id", clientID)
		return h.methodNotFound(req.ID, req.Method)
	}
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