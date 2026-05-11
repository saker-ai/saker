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
// URL format: /api/files/<relative-path> — relative to the project root only.
//
// Hardened against path traversal: all requests are joined to the project
// root and the resulting absolute path must remain inside that root after
// cleaning AND symlink resolution. Absolute paths and any traversal segment
// (`..`) are rejected before any filesystem access.
//
// @Summary Serve local file
// @Description Serves local files for media preview (images, audio, video). Paths are project-root relative; absolute paths and traversal are blocked.
// @Tags files
// @Produce octet-stream
// @Param path path string true "File path (relative to project root)"
// @Success 200 {file} file "File content"
// @Failure 400 {string} string "bad request — file path required or not a file"
// @Failure 403 {string} string "path outside project root"
// @Failure 404 {string} string "file not found"
// @Router /api/files/{path} [get]
func (s *Server) handleServeFile(w http.ResponseWriter, r *http.Request) {
	filePath := strings.TrimPrefix(r.URL.Path, "/api/files/")
	filePath = strings.TrimPrefix(filePath, "/api/files")
	filePath = strings.TrimPrefix(filePath, "/")
	if filePath == "" {
		http.Error(w, "file path required", http.StatusBadRequest)
		return
	}

	projectRoot := s.runtime.ProjectRoot()
	if projectRoot == "" {
		http.Error(w, "server has no project root configured", http.StatusInternalServerError)
		return
	}
	cleanRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		http.Error(w, "invalid project root", http.StatusInternalServerError)
		return
	}

	// Reject absolute paths outright — this endpoint is project-relative.
	if filepath.IsAbs(filePath) {
		http.Error(w, "absolute paths are not allowed", http.StatusForbidden)
		return
	}

	// Clean and verify the join stays inside the project root.
	resolved := filepath.Clean(filepath.Join(cleanRoot, filePath))
	rel, err := filepath.Rel(cleanRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		http.Error(w, "path outside project root", http.StatusForbidden)
		return
	}

	// Defense-in-depth: block symlinks that escape the root.
	if eval, err := filepath.EvalSymlinks(resolved); err == nil {
		evalRel, err := filepath.Rel(cleanRoot, eval)
		if err != nil || evalRel == ".." || strings.HasPrefix(evalRel, ".."+string(filepath.Separator)) {
			http.Error(w, "path resolves outside project root via symlink", http.StatusForbidden)
			return
		}
		resolved = eval
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
