package server

// SkillHub RPC bridge — exposes pkg/skillhub Client over WebSocket JSON-RPC
// so the web UI can browse, install, and publish without ever seeing the
// bearer token. Same code path as `saker skill` CLI; mutates project config
// via skillhub.SaveToProject and triggers a runtime reload after install/uninstall.
//
// Token security invariant: the token never leaves the server process. The
// frontend learns only `loggedIn: bool`, `handle`, and `registry`.
//
// Device flow: skillhub returns a `deviceCode` that must stay server-side
// (it's a credential-equivalent secret the user pastes into the browser via
// `userCode`). We mint a `sessionId`, keep `deviceCode` in an in-memory map
// with a 10-minute TTL, and let the frontend poll by `sessionId`.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/skillhub"
	"github.com/google/uuid"
)

const (
	skillhubLoginSessionTTL   = 10 * time.Minute
	skillhubDefaultRPCTimeout = 30 * time.Second
	skillhubInstallTimeout    = 5 * time.Minute
	skillhubPublishTimeout    = 2 * time.Minute

	// skillhubAuthRequiredCode is a custom JSON-RPC error code returned when
	// install/sync of a private skill needs an authenticated session. The
	// frontend keys off this code to open the device-flow login modal.
	// JSON-RPC reserves -32000..-32099 for server-defined errors.
	skillhubAuthRequiredCode = -32010
)

// loginSession holds the server-side half of an in-flight device flow.
// deviceCode is never sent to the frontend.
type loginSession struct {
	deviceCode string
	registry   string
	expiresAt  time.Time
}

// skillhubLoginSessions maps sessionId → loginSession with TTL.
// Package-scoped so one instance is shared across handler calls; the handler
// adds bookkeeping methods, the map is only mutated under skillhubLoginMu.
var (
	skillhubLoginMu       sync.Mutex
	skillhubLoginSessions = map[string]*loginSession{}
)

// gcLoginSessions removes expired login sessions. Called on every login/start
// and login/poll — cheap because the map stays tiny (one entry per active flow).
func gcLoginSessions(now time.Time) {
	for k, s := range skillhubLoginSessions {
		if now.After(s.expiresAt) {
			delete(skillhubLoginSessions, k)
		}
	}
}

// --- Dispatcher entry — wired from handler.go ------------------------------

// dispatchSkillhub routes a `skillhub/*` method. Returns (response, true) when
// handled, or (zero, false) when the method is not a skillhub method. Lets the
// main switch in HandleRequest stay flat without a second sub-switch per call.
func (h *Handler) dispatchSkillhub(ctx context.Context, req Request) (Response, bool) {
	switch req.Method {
	case "skillhub/config/get":
		return h.handleSkillhubConfigGet(req), true
	case "skillhub/config/update":
		return h.handleSkillhubConfigUpdate(ctx, req), true
	case "skillhub/login/start":
		return h.handleSkillhubLoginStart(ctx, req), true
	case "skillhub/login/poll":
		return h.handleSkillhubLoginPoll(ctx, req), true
	case "skillhub/login/cancel":
		return h.handleSkillhubLoginCancel(req), true
	case "skillhub/categories":
		return h.handleSkillhubCategories(req), true
	case "skillhub/logout":
		return h.handleSkillhubLogout(req), true
	case "skillhub/whoami":
		return h.handleSkillhubWhoAmI(ctx, req), true
	case "skillhub/search":
		return h.handleSkillhubSearch(ctx, req), true
	case "skillhub/list":
		return h.handleSkillhubList(ctx, req), true
	case "skillhub/get":
		return h.handleSkillhubGet(ctx, req), true
	case "skillhub/versions":
		return h.handleSkillhubVersions(ctx, req), true
	case "skillhub/install":
		return h.handleSkillhubInstall(ctx, req), true
	case "skillhub/uninstall":
		return h.handleSkillhubUninstall(req), true
	case "skillhub/sync":
		return h.handleSkillhubSync(ctx, req), true
	case "skillhub/publish-learned":
		return h.handleSkillhubPublishLearned(ctx, req), true
	}
	return Response{}, false
}

// --- Helpers ---------------------------------------------------------------

// loadSkillhubConfig reads the merged settings.json + settings.local.json
// for the current project root. Returns a usable (possibly empty) Config.
func (h *Handler) loadSkillhubConfig() (skillhub.Config, error) {
	return skillhub.LoadFromProject(h.runtime.ProjectRoot())
}

// saveSkillhubConfig writes back to settings.local.json under the settings lock.
func (h *Handler) saveSkillhubConfig(cfg skillhub.Config) error {
	h.settingsMu.Lock()
	defer h.settingsMu.Unlock()
	return skillhub.SaveToProject(h.runtime.ProjectRoot(), cfg)
}

// newSkillhubClient builds an authed client for the current config. Returns
// an error if offline or no registry is configured. Token (if any) is added
// transparently — callers never see it.
func (h *Handler) newSkillhubClient(cfg skillhub.Config) (*skillhub.Client, error) {
	if cfg.Offline {
		return nil, errors.New("skillhub is in offline mode")
	}
	if strings.TrimSpace(cfg.Registry) == "" {
		return nil, errors.New("no skillhub registry configured")
	}
	opts := []skillhub.ClientOption{}
	if cfg.Token != "" {
		opts = append(opts, skillhub.WithToken(cfg.Token))
	}
	return skillhub.New(cfg.Registry, opts...), nil
}

// publicConfig strips secrets so it's safe to send to the frontend.
// Token is replaced with a boolean; nothing else is removed.
func publicSkillhubConfig(cfg skillhub.Config) map[string]any {
	registry := cfg.Registry
	if registry == "" {
		registry = skillhub.DefaultRegistry
	}
	out := map[string]any{
		"registry":           registry,
		"handle":             cfg.Handle,
		"loggedIn":           cfg.Token != "",
		"offline":            cfg.Offline,
		"autoSync":           cfg.AutoSync,
		"syncInterval":       cfg.SyncInterval,
		"learnedAutoPublish": cfg.LearnedAutoPublish,
		"learnedVisibility":  cfg.LearnedVisibility,
		"subscriptions":      append([]string(nil), cfg.Subscriptions...),
		"lastSyncStatus":     cfg.LastSyncStatus,
	}
	if !cfg.LastSyncAt.IsZero() {
		out["lastSyncAt"] = cfg.LastSyncAt.Format(time.RFC3339)
	}
	return out
}

// --- config/get & config/update --------------------------------------------

func (h *Handler) handleSkillhubConfigGet(req Request) Response {
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, "load skillhub config: "+err.Error())
	}
	return h.success(req.ID, publicSkillhubConfig(cfg.Resolved()))
}

// handleSkillhubConfigUpdate persists registry / autoSync / learnedAutoPublish /
// learnedVisibility / offline. It deliberately ignores any token/handle/sub
// fields the client might send — those are managed via login/install flows.
func (h *Handler) handleSkillhubConfigUpdate(ctx context.Context, req Request) Response {
	if resp, ok := h.requireAdmin(ctx, req.ID); !ok {
		return resp
	}
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, "load skillhub config: "+err.Error())
	}

	if v, ok := req.Params["registry"].(string); ok {
		cfg.Registry = strings.TrimRight(strings.TrimSpace(v), "/")
	}
	if v, ok := req.Params["autoSync"].(bool); ok {
		cfg.AutoSync = v
	}
	if v, ok := req.Params["syncInterval"].(string); ok {
		cfg.SyncInterval = v
	}
	if v, ok := req.Params["learnedAutoPublish"].(bool); ok {
		cfg.LearnedAutoPublish = v
	}
	if v, ok := req.Params["learnedVisibility"].(string); ok {
		cfg.LearnedVisibility = v
	}
	if v, ok := req.Params["offline"].(bool); ok {
		cfg.Offline = v
	}

	if err := h.saveSkillhubConfig(cfg); err != nil {
		return h.internalError(req.ID, "save skillhub config: "+err.Error())
	}
	return h.success(req.ID, publicSkillhubConfig(cfg.Resolved()))
}

// --- login/start & login/poll & logout -------------------------------------

func (h *Handler) handleSkillhubLoginStart(ctx context.Context, req Request) Response {
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, "load skillhub config: "+err.Error())
	}
	resolved := cfg.Resolved()

	registry := resolved.Registry
	if v, ok := req.Params["registry"].(string); ok {
		s := strings.TrimRight(strings.TrimSpace(v), "/")
		if s != "" {
			registry = s
		}
	}
	if registry == "" {
		registry = skillhub.DefaultRegistry
	}

	client := skillhub.New(registry)
	rpcCtx, cancel := context.WithTimeout(ctx, skillhubDefaultRPCTimeout)
	defer cancel()

	dc, err := client.RequestDeviceCode(rpcCtx)
	if err != nil {
		return h.internalError(req.ID, "request device code: "+err.Error())
	}

	sessionID := uuid.New().String()
	skillhubLoginMu.Lock()
	gcLoginSessions(time.Now())
	skillhubLoginSessions[sessionID] = &loginSession{
		deviceCode: dc.DeviceCode,
		registry:   registry,
		expiresAt:  time.Now().Add(skillhubLoginSessionTTL),
	}
	skillhubLoginMu.Unlock()

	// Note: deviceCode intentionally NOT returned — only userCode + URL.
	return h.success(req.ID, map[string]any{
		"sessionId":       sessionID,
		"userCode":        dc.UserCode,
		"verificationUrl": dc.VerificationURL,
		"expiresIn":       dc.ExpiresIn,
		"interval":        dc.Interval,
		"registry":        registry,
	})
}

// handleSkillhubLoginPoll runs ONE poll attempt and returns immediately.
// The frontend is responsible for the loop (it can show progress / cancel).
func (h *Handler) handleSkillhubLoginPoll(ctx context.Context, req Request) Response {
	sessionID, _ := req.Params["sessionId"].(string)
	if sessionID == "" {
		return h.invalidParams(req.ID, "sessionId is required")
	}

	skillhubLoginMu.Lock()
	gcLoginSessions(time.Now())
	sess, ok := skillhubLoginSessions[sessionID]
	skillhubLoginMu.Unlock()
	if !ok {
		return h.invalidParams(req.ID, "login session expired or not found")
	}

	client := skillhub.New(sess.registry)
	// Single poll: 1s interval ensures PollDeviceToken returns after one HTTP roundtrip
	// regardless of skillhub's "pending" response (we cancel immediately on pending).
	pollCtx, cancel := context.WithTimeout(ctx, skillhubDefaultRPCTimeout)
	defer cancel()

	type result struct {
		token string
		err   error
	}
	resCh := make(chan result, 1)
	go func() {
		// Use a tight inner timeout so pending returns fast.
		innerCtx, innerCancel := context.WithTimeout(pollCtx, 5*time.Second)
		defer innerCancel()
		token, err := client.PollDeviceToken(innerCtx, sess.deviceCode, 1*time.Second)
		resCh <- result{token, err}
	}()

	select {
	case r := <-resCh:
		if r.err != nil {
			// PollDeviceToken loops on pending; deadline hit means still pending.
			if errors.Is(r.err, context.DeadlineExceeded) || errors.Is(r.err, context.Canceled) {
				return h.success(req.ID, map[string]any{"status": "pending"})
			}
			// Surface API errors verbatim — caller decides how to display.
			return h.success(req.ID, map[string]any{
				"status": "error",
				"error":  r.err.Error(),
			})
		}

		// Token granted: persist and clean up the session.
		client.SetToken(r.token)
		whoCtx, whoCancel := context.WithTimeout(ctx, skillhubDefaultRPCTimeout)
		defer whoCancel()
		who, _ := client.WhoAmI(whoCtx) // best-effort

		cfg, err := h.loadSkillhubConfig()
		if err != nil {
			return h.internalError(req.ID, "load skillhub config: "+err.Error())
		}
		cfg.Registry = sess.registry
		cfg.Token = r.token
		if who != nil && who.Handle != "" {
			cfg.Handle = who.Handle
		}
		if err := h.saveSkillhubConfig(cfg); err != nil {
			return h.internalError(req.ID, "save skillhub config: "+err.Error())
		}

		skillhubLoginMu.Lock()
		delete(skillhubLoginSessions, sessionID)
		skillhubLoginMu.Unlock()

		out := map[string]any{
			"status":   "ok",
			"handle":   cfg.Handle,
			"registry": cfg.Registry,
		}
		if who != nil {
			out["user"] = map[string]any{
				"id":     who.ID,
				"handle": who.Handle,
				"role":   who.Role,
				"email":  who.Email,
			}
		}
		return h.success(req.ID, out)

	case <-ctx.Done():
		return h.success(req.ID, map[string]any{"status": "pending"})
	}
}

func (h *Handler) handleSkillhubLogout(req Request) Response {
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, "load skillhub config: "+err.Error())
	}
	if cfg.Token == "" {
		return h.success(req.ID, publicSkillhubConfig(cfg.Resolved()))
	}
	cfg.Token = ""
	// keep handle as a hint for the next login UI
	if err := h.saveSkillhubConfig(cfg); err != nil {
		return h.internalError(req.ID, "save skillhub config: "+err.Error())
	}
	return h.success(req.ID, publicSkillhubConfig(cfg.Resolved()))
}

// --- whoami / search / list / get / versions ------------------------------

func (h *Handler) handleSkillhubWhoAmI(ctx context.Context, req Request) Response {
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, "load skillhub config: "+err.Error())
	}
	client, err := h.newSkillhubClient(cfg.Resolved())
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}
	rpcCtx, cancel := context.WithTimeout(ctx, skillhubDefaultRPCTimeout)
	defer cancel()
	who, err := client.WhoAmI(rpcCtx)
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	return h.success(req.ID, who)
}

func (h *Handler) handleSkillhubSearch(ctx context.Context, req Request) Response {
	q, _ := req.Params["q"].(string)
	if strings.TrimSpace(q) == "" {
		return h.invalidParams(req.ID, "q is required")
	}
	limit := 20
	if v, ok := req.Params["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	client, err := h.newSkillhubClient(cfg.Resolved())
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}
	rpcCtx, cancel := context.WithTimeout(ctx, skillhubDefaultRPCTimeout)
	defer cancel()
	res, err := client.Search(rpcCtx, q, limit)
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	return h.success(req.ID, res)
}

func (h *Handler) handleSkillhubList(ctx context.Context, req Request) Response {
	category, _ := req.Params["category"].(string)
	sort, _ := req.Params["sort"].(string)
	cursor, _ := req.Params["cursor"].(string)
	limit := 20
	if v, ok := req.Params["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	client, err := h.newSkillhubClient(cfg.Resolved())
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}
	rpcCtx, cancel := context.WithTimeout(ctx, skillhubDefaultRPCTimeout)
	defer cancel()
	res, err := client.List(rpcCtx, category, sort, cursor, limit)
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	return h.success(req.ID, res)
}

func (h *Handler) handleSkillhubGet(ctx context.Context, req Request) Response {
	slug, _ := req.Params["slug"].(string)
	if slug == "" {
		return h.invalidParams(req.ID, "slug is required")
	}
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	client, err := h.newSkillhubClient(cfg.Resolved())
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}
	rpcCtx, cancel := context.WithTimeout(ctx, skillhubDefaultRPCTimeout)
	defer cancel()
	s, err := client.Get(rpcCtx, slug)
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	return h.success(req.ID, s)
}

func (h *Handler) handleSkillhubVersions(ctx context.Context, req Request) Response {
	slug, _ := req.Params["slug"].(string)
	if slug == "" {
		return h.invalidParams(req.ID, "slug is required")
	}
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	client, err := h.newSkillhubClient(cfg.Resolved())
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}
	rpcCtx, cancel := context.WithTimeout(ctx, skillhubDefaultRPCTimeout)
	defer cancel()
	versions, err := client.Versions(rpcCtx, slug)
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	return h.success(req.ID, map[string]any{"versions": versions})
}

// --- install / uninstall / sync --------------------------------------------

func (h *Handler) handleSkillhubInstall(ctx context.Context, req Request) Response {
	slug, _ := req.Params["slug"].(string)
	if slug == "" {
		return h.invalidParams(req.ID, "slug is required")
	}
	version, _ := req.Params["version"].(string)

	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	client, err := h.newSkillhubClient(cfg.Resolved())
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}

	rpcCtx, cancel := context.WithTimeout(ctx, skillhubInstallTimeout)
	defer cancel()
	root := skillhub.SubscribedDir(h.runtime.ProjectRoot())
	res, err := client.Install(rpcCtx, slug, skillhub.InstallOptions{
		Dir:     root,
		Version: version,
	})
	if err != nil {
		// Surface auth errors with a stable code so the frontend can prompt login
		// without parsing English error strings. Public skills never hit this branch.
		var apiErr *skillhub.APIError
		if errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403) {
			return Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &Error{
					Code:    skillhubAuthRequiredCode,
					Message: "skillhub: login required for " + slug,
					Data:    map[string]any{"slug": slug, "httpStatus": apiErr.Status},
				},
			}
		}
		return h.internalError(req.ID, err.Error())
	}

	// Persist subscription (dedup) so `sync` will refresh it.
	if !slices.Contains(cfg.Subscriptions, slug) {
		cfg.Subscriptions = append(cfg.Subscriptions, slug)
		if err := h.saveSkillhubConfig(cfg); err != nil {
			h.logger.Warn("skillhub: failed to persist subscription", "slug", slug, "error", err)
		}
	}

	// Fire skill runtime reload so the new skill shows up in skill/list.
	if errs := h.runtime.ReloadSkills(); len(errs) > 0 {
		h.logger.Warn("skillhub: reload skills had warnings", "slug", slug, "errors", len(errs))
	}

	return h.success(req.ID, map[string]any{
		"slug":        res.Slug,
		"version":     res.Version,
		"dir":         res.Dir,
		"filesCount":  res.FilesCount,
		"notModified": res.NotModified,
	})
}

func (h *Handler) handleSkillhubUninstall(req Request) Response {
	slug, _ := req.Params["slug"].(string)
	if slug == "" {
		return h.invalidParams(req.ID, "slug is required")
	}

	root := skillhub.SubscribedDir(h.runtime.ProjectRoot())
	if err := skillhub.Uninstall(root, slug); err != nil {
		return h.internalError(req.ID, err.Error())
	}

	cfg, err := h.loadSkillhubConfig()
	if err == nil {
		cfg.Subscriptions = slices.DeleteFunc(cfg.Subscriptions, func(s string) bool {
			return s == slug
		})
		_ = h.saveSkillhubConfig(cfg)
	}

	if errs := h.runtime.ReloadSkills(); len(errs) > 0 {
		h.logger.Warn("skillhub: reload skills had warnings after uninstall", "slug", slug, "errors", len(errs))
	}
	return h.success(req.ID, map[string]any{"ok": true})
}

func (h *Handler) handleSkillhubSync(ctx context.Context, req Request) Response {
	results, lastSyncAt, lastSyncStatus, err := h.runSkillhubSync(ctx)
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	out := map[string]any{
		"results":        results,
		"lastSyncStatus": lastSyncStatus,
	}
	if !lastSyncAt.IsZero() {
		out["lastSyncAt"] = lastSyncAt.Format(time.RFC3339)
	}
	return h.success(req.ID, out)
}

// runSkillhubSync performs a single sync pass over all subscribed skills,
// persists LastSyncAt/LastSyncStatus, and triggers a runtime reload.
// Shared between the JSON-RPC handler and the auto-sync background loop.
func (h *Handler) runSkillhubSync(ctx context.Context) (results []map[string]any, lastSyncAt time.Time, lastSyncStatus string, err error) {
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return nil, time.Time{}, "", err
	}
	resolved := cfg.Resolved()
	if len(resolved.Subscriptions) == 0 {
		return []map[string]any{}, time.Time{}, "", nil
	}
	client, err := h.newSkillhubClient(resolved)
	if err != nil {
		return nil, time.Time{}, "", err
	}

	rpcCtx, cancel := context.WithTimeout(ctx, skillhubInstallTimeout)
	defer cancel()
	root := skillhub.SubscribedDir(h.runtime.ProjectRoot())

	results = make([]map[string]any, 0, len(resolved.Subscriptions))
	errCount := 0
	for _, slug := range resolved.Subscriptions {
		etag := readSkillOriginETag(root, slug)
		res, ierr := client.Install(rpcCtx, slug, skillhub.InstallOptions{Dir: root, ETag: etag})
		if ierr != nil {
			errCount++
			results = append(results, map[string]any{
				"slug":   slug,
				"status": "error",
				"error":  ierr.Error(),
			})
			continue
		}
		if res.NotModified {
			results = append(results, map[string]any{
				"slug":   slug,
				"status": "up-to-date",
			})
			continue
		}
		results = append(results, map[string]any{
			"slug":       slug,
			"status":     "updated",
			"version":    res.Version,
			"filesCount": res.FilesCount,
		})
	}

	if errs := h.runtime.ReloadSkills(); len(errs) > 0 {
		h.logger.Warn("skillhub: reload skills had warnings after sync", "errors", len(errs))
	}

	lastSyncAt = time.Now().UTC()
	switch {
	case errCount == 0:
		lastSyncStatus = "ok"
	case errCount < len(resolved.Subscriptions):
		lastSyncStatus = "partial"
	default:
		lastSyncStatus = "error"
	}
	cfg.LastSyncAt = lastSyncAt
	cfg.LastSyncStatus = lastSyncStatus
	if serr := h.saveSkillhubConfig(cfg); serr != nil {
		h.logger.Warn("skillhub: failed to persist lastSyncAt", "error", serr)
	}
	return results, lastSyncAt, lastSyncStatus, nil
}

// RunSkillhubAutoSync is the public entry point used by the auto-sync goroutine
// in server.go. It refuses to run when offline / no subscriptions / no config.
// Returns nil on a no-op skip; non-nil on real failure.
func (h *Handler) RunSkillhubAutoSync(ctx context.Context) error {
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return err
	}
	resolved := cfg.Resolved()
	if !resolved.AutoSync || resolved.Offline || len(resolved.Subscriptions) == 0 {
		return nil
	}
	_, _, _, err = h.runSkillhubSync(ctx)
	return err
}

// SkillhubSyncInterval reports the current configured interval. Falls back to
// 15 minutes when unset. Used by the auto-sync goroutine to size its ticker.
func (h *Handler) SkillhubSyncInterval() time.Duration {
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return 15 * time.Minute
	}
	return cfg.Resolved().SyncIntervalDuration()
}

// SkillhubAutoSyncEnabled returns whether the project currently opts into the
// background sync ticker. Lets the goroutine cheaply skip wake-ups when off.
func (h *Handler) SkillhubAutoSyncEnabled() bool {
	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return false
	}
	return cfg.Resolved().AutoSync
}

// --- publish-learned -------------------------------------------------------

func (h *Handler) handleSkillhubPublishLearned(ctx context.Context, req Request) Response {
	name, _ := req.Params["name"].(string)
	if name == "" {
		return h.invalidParams(req.ID, "name is required")
	}

	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	resolved := cfg.Resolved()
	if resolved.Handle == "" {
		return h.invalidParams(req.ID, "no skillhub handle configured; log in first")
	}
	client, err := h.newSkillhubClient(resolved)
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}

	skillDir := filepath.Join(skillhub.LearnedDir(h.runtime.ProjectRoot()), name)

	rpcCtx, cancel := context.WithTimeout(ctx, skillhubPublishTimeout)
	defer cancel()
	resp, err := client.PublishLearned(rpcCtx, skillDir, skillhub.PublishLearnedOptions{
		Handle:     resolved.Handle,
		Visibility: resolved.LearnedVisibility,
	})
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	return h.success(req.ID, map[string]any{
		"slug":        resp.Skill.Slug,
		"version":     resp.Version.Version,
		"fingerprint": resp.Version.Fingerprint,
	})
}

// --- login/cancel ---------------------------------------------------------

// handleSkillhubLoginCancel removes a pending device-flow session so its
// deviceCode can no longer be polled. Idempotent: missing sessions return ok.
func (h *Handler) handleSkillhubLoginCancel(req Request) Response {
	sessionID, _ := req.Params["sessionId"].(string)
	if sessionID == "" {
		return h.invalidParams(req.ID, "sessionId is required")
	}
	skillhubLoginMu.Lock()
	delete(skillhubLoginSessions, sessionID)
	skillhubLoginMu.Unlock()
	return h.success(req.ID, map[string]any{"ok": true})
}

// --- categories -----------------------------------------------------------

// defaultSkillhubCategories is the fallback list when the registry doesn't
// expose a /categories endpoint. Keep in sync with the marketing site so users
// see consistent labels in chips.
var defaultSkillhubCategories = []string{
	"general", "code", "productivity", "writing", "research", "data", "ops",
}

// handleSkillhubCategories returns category slugs the UI can render as chips.
// Today this is a static list; once the registry exposes /api/v1/categories
// we can swap to a live call without changing the frontend contract.
func (h *Handler) handleSkillhubCategories(req Request) Response {
	return h.success(req.ID, map[string]any{
		"categories": defaultSkillhubCategories,
	})
}

// --- readSkillOriginETag --------------------------------------------------

// readSkillOriginETag reads the etag= line from the .skillhub-origin sidecar
// written by skillhub.Install. Empty result forces a fresh download.
func readSkillOriginETag(root, slug string) string {
	dir := strings.ReplaceAll(slug, "/", "__")
	data, err := os.ReadFile(filepath.Join(root, dir, ".skillhub-origin"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "etag=") {
			return strings.TrimPrefix(line, "etag=")
		}
	}
	return ""
}
