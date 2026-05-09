package server

import "context"

// startBackgroundLoops launches the long-running goroutines that the server
// owns: upload cleanup, media cache GC, and skillhub auto-sync.
//
// It runs once during ListenAndServe (after the HTTP server is wired but
// before Serve blocks). Shutdown is handled per-loop:
//   - cleanupUploads / runMediaCacheCleanup are one-shot at startup
//   - runSkillhubAutoSyncLoop respects autoSyncCancel, which Shutdown calls
//
// Splitting this out of server.go keeps the listener boot sequence in
// ListenAndServe focused on "build mux, attach middleware, listen".
func (s *Server) startBackgroundLoops() {
	// Clean up old uploaded files and unreferenced media cache on startup.
	// Uploads live under <DataDir>/uploads — server-wide, not per-project,
	// so a single sweep is correct in both single- and multi-tenant modes.
	go cleanupUploads(s.opts.DataDir, s.logger)
	// Media cache lives under <projectRoot>/.saker/canvas-media (still a
	// single shared directory). In multi-tenant mode each project owns its
	// own SessionStore, so the union of references across all projects
	// must be collected before we decide which files are safe to remove.
	go s.runMediaCacheCleanup()

	// Skillhub auto-sync background loop.
	autoCtx, autoCancel := context.WithCancel(context.Background())
	s.autoSyncCancel = autoCancel
	go s.runSkillhubAutoSyncLoop(autoCtx)

	// Apps temp-thread orphan sweep — one-shot at startup. Catches
	// app-run-* canvas files left over from crashed runs or Notify drops.
	go s.runAppTempThreadSweep()

	// Apps version retention — initial sweep + 24h ticker. PublishVersion
	// already prunes inline, but this loop catches apps that haven't been
	// republished since the cap was added (or since MaxVersionsPerApp grew).
	go s.runAppVersionRetention(autoCtx)
}
