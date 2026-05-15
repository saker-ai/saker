// skillhub_install.go: install/uninstall/sync/publish endpoints + auto-sync.
package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/skillhub"
)

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
