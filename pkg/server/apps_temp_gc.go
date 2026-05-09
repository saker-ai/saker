package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/apps"
)

// recordAppTempThread is the OnTempThread callback the apps Runner invokes
// after writing <dataDir>/canvas/{threadID}.json. We remember the
// (threadID → dataDir) pair so the canvas/run-finished Notify hook can
// drain the temp file once the run reaches a terminal state.
//
// The same map is also consulted by drainAppTempThread to know where on
// disk the temp document lives — Notify only carries threadID, not dataDir,
// and the executor's own DataDir might have been switched between scopes
// since the run was launched.
func (h *Handler) recordAppTempThread(threadID, dataDir string) {
	if threadID == "" || dataDir == "" {
		return
	}
	h.appTempThreads.Store(threadID, dataDir)
}

// drainAppTempThread is invoked from the canvas/run-finished Notify hook
// (see initPerProjectRegistries) when a run reaches a terminal state.
// It removes the temp document the apps Runner wrote into the dataDir and
// then forgets the entry. Best-effort: a missing file is treated as a
// success (the orphan sweep already cleaned it, or the run was cancelled
// before Save returned).
func (h *Handler) drainAppTempThread(threadID string) {
	if threadID == "" {
		return
	}
	v, ok := h.appTempThreads.LoadAndDelete(threadID)
	if !ok {
		return
	}
	dataDir, _ := v.(string)
	if dataDir == "" {
		return
	}
	path := filepath.Join(dataDir, "canvas", threadID+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		h.logger.Warn("apps temp thread cleanup failed",
			"thread_id", threadID, "path", path, "error", err)
	}
}

// runAppTempThreadSweep walks every project's canvas directory at startup
// and removes app-run-* temp files older than 24h. This catches files that
// were never drained because:
//
//   - the server crashed mid-run before canvas/run-finished fired
//   - the Notify hook was stripped by a misconfigured per_project_components
//     wiring
//   - a deploy upgraded across the boundary where this GC was added
//
// One-shot at startup is enough — the live Notify hook handles the
// steady-state case for new runs.
func (s *Server) runAppTempThreadSweep() {
	dirs := s.appTempCanvasDirs(context.Background())
	cutoff := time.Now().Add(-24 * time.Hour)
	var swept, errored int
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				s.logger.Warn("apps temp sweep: read dir failed",
					"dir", dir, "error", err)
				errored++
			}
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), "app-run-") || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(cutoff) {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				s.logger.Warn("apps temp sweep: remove failed",
					"path", path, "error", err)
				errored++
				continue
			}
			swept++
		}
	}
	if swept > 0 || errored > 0 {
		s.logger.Info("apps temp thread sweep done",
			"swept", swept, "errored", errored, "dirs", len(dirs))
	}
}

// appTempCanvasDirs returns every <root>/canvas directory the sweep should
// scan: the legacy single-project root (always present) and, when multi-
// tenant is wired, every project's per-tenant canvas dir.
func (s *Server) appTempCanvasDirs(ctx context.Context) []string {
	var dirs []string
	if s.opts.DataDir != "" {
		dirs = append(dirs, filepath.Join(s.opts.DataDir, "canvas"))
	}
	if s.handler == nil || s.handler.projects == nil {
		return dirs
	}
	projects, err := s.handler.projects.ListAllProjects(ctx)
	if err != nil {
		s.logger.Warn("apps temp sweep: list projects failed", "error", err)
		return dirs
	}
	for _, p := range projects {
		dirs = append(dirs, filepath.Join(s.opts.DataDir, "projects", p.ID, "canvas"))
	}
	return dirs
}

// runAppVersionRetention is the daily ticker that calls
// apps.Store.PruneOldVersions on every app in every project, capped at
// MaxVersionsPerApp. PublishVersion already prunes inline — this loop is
// the safety net that catches apps which haven't been republished in a
// while but still have stale snapshots from before the cap existed.
func (s *Server) runAppVersionRetention(ctx context.Context) {
	// Run once at startup so a fresh deploy immediately enforces the cap,
	// then settle into the daily cadence.
	s.sweepAppVersionsOnce(ctx)
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepAppVersionsOnce(ctx)
		}
	}
}

func (s *Server) sweepAppVersionsOnce(ctx context.Context) {
	roots := s.appsScopeRoots(ctx)
	var pruned, scanned int
	for _, root := range roots {
		store := apps.New(root)
		metas, err := store.List(ctx)
		if err != nil {
			continue
		}
		for _, m := range metas {
			scanned++
			if err := store.PruneOldVersions(ctx, m.ID, apps.MaxVersionsPerApp); err != nil {
				s.logger.Warn("apps version retention failed",
					"app_id", m.ID, "root", root, "error", err)
				continue
			}
			pruned++
		}
	}
	if scanned > 0 {
		s.logger.Info("apps version retention done",
			"scanned", scanned, "pruned", pruned, "roots", len(roots))
	}
}

// appsScopeRoots returns every data-root an apps.Store should be created
// against — the legacy single-project root plus per-project roots in
// multi-tenant mode.
func (s *Server) appsScopeRoots(ctx context.Context) []string {
	var roots []string
	if s.opts.DataDir != "" {
		roots = append(roots, s.opts.DataDir)
	}
	if s.handler == nil || s.handler.projects == nil {
		return roots
	}
	projects, err := s.handler.projects.ListAllProjects(ctx)
	if err != nil {
		s.logger.Warn("apps version retention: list projects failed", "error", err)
		return roots
	}
	for _, p := range projects {
		roots = append(roots, filepath.Join(s.opts.DataDir, "projects", p.ID))
	}
	return roots
}
