package server

import (
	"context"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/saker-ai/saker/pkg/config"
	storagecfg "github.com/saker-ai/saker/pkg/storage"
	"github.com/gin-gonic/gin"
)

// storageConfigFromSettings translates the JSON settings shape into the
// runtime storage.Config consumed by storage.Open. Returns the zero Config
// (defaults to osfs at <dataDir>/media) when settings.Storage is nil.
func storageConfigFromSettings(s *config.Settings) storagecfg.Config {
	if s == nil || s.Storage == nil {
		return storagecfg.Config{}
	}
	src := s.Storage
	out := storagecfg.Config{
		Backend:       src.Backend,
		PublicBaseURL: src.PublicBaseURL,
		TenantPrefix:  src.TenantPrefix,
	}
	if src.OSFS != nil {
		out.OSFS = storagecfg.OSFSConfig{Root: src.OSFS.Root}
	}
	if src.Embedded != nil {
		out.Embedded = storagecfg.EmbeddedConfig{
			Mode:      src.Embedded.Mode,
			Addr:      src.Embedded.Addr,
			Root:      src.Embedded.Root,
			Bucket:    src.Embedded.Bucket,
			AccessKey: src.Embedded.AccessKey,
			SecretKey: src.Embedded.SecretKey,
		}
	}
	if src.S3 != nil {
		out.S3 = storagecfg.S3Config{
			Endpoint:        src.S3.Endpoint,
			Region:          src.S3.Region,
			Bucket:          src.S3.Bucket,
			AccessKeyID:     src.S3.AccessKeyID,
			SecretAccessKey: src.S3.SecretAccessKey,
			UsePathStyle:    src.S3.UsePathStyle,
			PublicBaseURL:   src.S3.PublicBaseURL,
		}
	}
	return out
}

// handleMediaServe streams an object out of the currently-configured object
// store.
//
// URL shape: GET /media/<key> where <key> matches the layout produced by
// storage.Config.Key (e.g. "<projectID>/image/ab/abcdef….png"). The route is
// always registered; if no object store is wired (legacy on-disk path), the
// handler returns 404 so /api/files/ remains the only path. Reads go through
// objectStoreSnapshot so a hot reload from /api/settings/update lands without
// tearing in-flight requests.
//
// @Summary Serve media object
// @Description Streams an object from the currently-configured object store (osfs, embedded S3, or external S3). Returns 404 when no backend is wired.
// @Tags media
// @Produce octet-stream
// @Param key path string true "Object key (e.g. projectID/image/abcdef.png)"
// @Success 200 {file} file "Object content"
// @Failure 400 {string} string "object key required"
// @Failure 404 {string} string "object not found or store not configured"
// @Failure 500 {string} string "fetch or open failed"
// @Router /media/{key} [get]
func (s *Server) handleMediaServe(c *gin.Context) {
	r := c.Request
	w := c.Writer
	if s.handler == nil {
		http.Error(w, "object store not configured", http.StatusNotFound)
		return
	}
	store, cfg := s.handler.objectStoreSnapshot()
	if store == nil {
		http.Error(w, "object store not configured", http.StatusNotFound)
		return
	}
	prefix := strings.TrimRight(cfg.PublicBaseURL, "/")
	if prefix == "" {
		prefix = storagecfg.DefaultPublicBaseURL
	}
	key := strings.TrimPrefix(r.URL.Path, prefix)
	key = strings.TrimPrefix(key, "/")
	if key == "" {
		http.Error(w, "object key required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	obj, err := store.Get(ctx, key)
	if err != nil {
		if storagecfg.IsNotExist(err) {
			http.Error(w, "object not found", http.StatusNotFound)
			return
		}
		s.logger.Warn("media serve: get failed", "key", key, "error", err)
		http.Error(w, "fetch failed", http.StatusInternalServerError)
		return
	}
	rc, err := obj.Open()
	if err != nil {
		s.logger.Warn("media serve: open failed", "key", key, "error", err)
		http.Error(w, "open failed", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	if md := obj.Metadata(); md != nil {
		if ct, ok := md.Get("Content-Type"); ok && ct != "" {
			w.Header().Set("Content-Type", ct)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		// Best-effort guess from extension; handlers should normally have
		// set Content-Type at upload time via cacheMediaToStore.
		if ext := strings.ToLower(path.Ext(key)); ext != "" {
			if ct := mimeFromExt(ext); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
	_ = ctx
}

// mimeFromExt returns a small fixed mapping for the extensions cacheMediaToStore
// can produce. Anything outside the set falls through to the empty string and
// the response goes out without an explicit Content-Type (browsers sniff).
func mimeFromExt(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	}
	return ""
}

// openObjectStore initializes the object store backend described by settings
// and wires it onto the handler. Returns the embedded server (when running
// the embedded backend) so the caller can stop it during Shutdown.
func (s *Server) openObjectStore(ctx context.Context) (*storagecfg.Embedded, error) {
	cfg := storageConfigFromSettings(s.runtime.Settings())
	st, emb, err := storagecfg.Open(ctx, cfg, s.opts.DataDir)
	if err != nil {
		return nil, err
	}
	// Re-derive cfg with defaults filled so PublicBaseURL/key shapes match
	// what storage.Open actually used internally. The Open call mutates
	// Embedded/OSFS roots; here we redo the same defaulting locally.
	resolved := cfg
	if resolved.PublicBaseURL == "" {
		resolved.PublicBaseURL = storagecfg.DefaultPublicBaseURL
	}
	s.handler.SetStorage(st, resolved)
	return emb, nil
}

// reloadObjectStore rebuilds the object store from the current settings and
// installs it on the handler atomically. Called from handleSettingsUpdate
// after settings.local.json has been re-read by the runtime, so
// storageConfigFromSettings sees the new shape.
//
// Strategy:
//  1. Open the new backend first. If it fails, leave the existing store wired
//     and return the error — the user gets a saved-but-not-applied state and
//     can retry / fix the config.
//  2. Swap onto the handler via SetStorage (RLock-protected hot path).
//  3. Stop the old embedded server (if any) AFTER the swap, so any in-flight
//     /_s3/ request finishes against the still-live old handler before its
//     listener goes away. The /_s3/ mux entry reads the latest handler via
//     embeddedHandler() so the next request hits the new server.
//
// Caller must hold no locks.
func (s *Server) reloadObjectStore(ctx context.Context) error {
	cfg := storageConfigFromSettings(s.runtime.Settings())
	st, emb, err := storagecfg.Open(ctx, cfg, s.opts.DataDir)
	if err != nil {
		return err
	}
	resolved := cfg
	if resolved.PublicBaseURL == "" {
		resolved.PublicBaseURL = storagecfg.DefaultPublicBaseURL
	}

	s.embeddedMu.Lock()
	old := s.embedded
	s.embedded = emb
	s.embeddedMu.Unlock()

	// Hot-swap the store seen by the handler. After this point new media
	// reads go to the new backend; old in-flight readers keep their handles
	// (s2.Object captures the underlying file/blob handle at Get time).
	s.handler.SetStorage(st, resolved)

	if old != nil && old != emb {
		if stopErr := old.Stop(); stopErr != nil {
			s.logger.Warn("storage reload: stop old embedded failed", "error", stopErr)
		}
	}
	return nil
}

// embeddedHandler returns the current embedded S3 handler, or nil if the
// active backend isn't embedded-external. Read under embeddedMu so that
// reloadObjectStore can swap it concurrently without tearing.
func (s *Server) embeddedHandler() http.Handler {
	s.embeddedMu.RLock()
	emb := s.embedded
	s.embeddedMu.RUnlock()
	if emb == nil {
		return nil
	}
	return emb.Handler()
}
