// skillhub_handler.go: SkillHub RPC dispatcher + shared helpers/state.
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
	"strings"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/skillhub"
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
