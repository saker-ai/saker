package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// handleHealth responds with a simple liveness check.
//
// @Summary Health check
// @Description Returns server liveness status and current UTC timestamp
// @Tags health
// @Produce json
// @Success 200 {object} map[string]string "ok"
// @Failure 503 {object} map[string]string "unavailable"
// @Router /health [get]
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// handleServeFile serves local files for media preview (images, audio, video).
// URL format: /api/files/<path> — absolute or relative to project root.
//
// @Summary Serve local file
// @Description Serves local files for media preview (images, audio, video). Resolves absolute paths or paths relative to project root. Blocks path traversal above project root.
// @Tags files
// @Produce octet-stream
// @Param path path string true "File path (absolute or relative to project root)"
// @Success 200 {file} file "File content"
// @Failure 400 {string} string "bad request — file path required or not a file"
// @Failure 403 {string} string "path outside project root"
// @Failure 404 {string} string "file not found"
// @Router /api/files/{path} [get]
func (s *Server) handleServeFile(w http.ResponseWriter, r *http.Request) {
	filePath := strings.TrimPrefix(r.URL.Path, "/api/files/")
	filePath = strings.TrimPrefix(filePath, "/api/files")
	if filePath == "" || filePath == "/" {
		http.Error(w, "file path required", http.StatusBadRequest)
		return
	}

	// Resolve path: try as absolute first (leading / consumed by route prefix),
	// then fall back to project-relative.
	resolved := filepath.Clean(filePath)
	if !filepath.IsAbs(resolved) {
		absCandidate := "/" + resolved
		if _, err := os.Stat(absCandidate); err == nil {
			resolved = absCandidate
		} else {
			projectRoot := s.runtime.ProjectRoot()
			resolved = filepath.Join(projectRoot, resolved)
			// Block path traversal above project root for relative paths.
			if !strings.HasPrefix(resolved, projectRoot) {
				http.Error(w, "path outside project root", http.StatusForbidden)
				return
			}
		}
	}

	info, err := os.Stat(resolved)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		http.Error(w, "not a file", http.StatusBadRequest)
		return
	}

	// Set cache headers for media files.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeFile(w, r, resolved)
}
