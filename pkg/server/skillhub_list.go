// skillhub_list.go: list/browse endpoints + config, login, whoami, categories.
package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/skillhub"
	"github.com/google/uuid"
)

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

// --- whoami / list / get / versions ----------------------------------------

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
