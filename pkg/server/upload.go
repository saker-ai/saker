package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const uploadMaxAge = 24 * time.Hour

const maxUploadSize = 50 << 20 // 50 MB

// allowedMediaPrefixes lists the MIME type prefixes accepted for upload.
var allowedMediaPrefixes = []string{"image/", "video/", "audio/", "application/pdf"}

// uploadResponse is returned on successful upload.
type uploadResponse struct {
	Path      string `json:"path"`       // URL path to access the file via /api/files/
	Name      string `json:"name"`       // original filename
	MediaType string `json:"media_type"` // detected MIME type
	Size      int64  `json:"size"`       // file size in bytes
}

// handleUpload accepts a multipart file upload and saves it to the uploads directory.
// POST /api/upload  —  form field "file".
//
// @Summary Upload file
// @Description Accepts a multipart file upload (max 50 MB) for media files (images, video, audio, PDF). The file is saved with a UUID-prefixed name and a response with the URL path is returned.
// @Tags files
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "File to upload"
// @Success 200 {object} uploadResponse "Upload successful"
// @Failure 400 {string} string "missing file field or unsupported file type"
// @Failure 413 {string} string "file too large (max 50MB)"
// @Failure 500 {string} string "internal error"
// @Router /api/upload [post]
func (s *Server) handleUpload(c *gin.Context) {
	r := c.Request
	w := c.Writer

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+1024) // small overhead for headers
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		http.Error(w, "file too large (max 50MB)", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Detect MIME type from file content first (content-based, harder to spoof),
	// then fall back to extension-based detection.
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	mediaType := http.DetectContentType(buf[:n])
	if mediaType == "application/octet-stream" {
		ext := strings.ToLower(filepath.Ext(header.Filename))
		mediaType = mime.TypeByExtension(ext)
	}
	if mediaType == "" {
		mediaType = header.Header.Get("Content-Type")
	}
	// Seek back to start so the full file is written to disk.
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !isAllowedMedia(mediaType) {
		http.Error(w, fmt.Sprintf("unsupported file type: %s", mediaType), http.StatusBadRequest)
		return
	}

	// Ensure uploads directory exists.
	uploadsDir := filepath.Join(s.opts.DataDir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		s.logger.Error("failed to create uploads dir", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Save with a UUID prefix to avoid collisions.
	safeFilename := sanitizeFilename(header.Filename)
	diskName := uuid.New().String()[:8] + "-" + safeFilename
	diskPath := filepath.Join(uploadsDir, diskName)

	dst, err := os.Create(diskPath)
	if err != nil {
		s.logger.Error("failed to create upload file", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		os.Remove(diskPath)
		s.logger.Error("failed to write upload file", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.logger.Info("file uploaded", "name", header.Filename, "size", written, "media_type", mediaType, "disk_path", diskPath)

	// Lazy cleanup: occasionally remove old uploads in the background.
	if s.uploadCount.Add(1)%100 == 0 {
		go cleanupUploads(s.opts.DataDir, s.logger)
	}

	resp := uploadResponse{
		Path:      "/api/files/" + diskName,
		Name:      header.Filename,
		MediaType: mediaType,
		Size:      written,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func isAllowedMedia(mediaType string) bool {
	for _, prefix := range allowedMediaPrefixes {
		if strings.HasPrefix(mediaType, prefix) {
			return true
		}
	}
	return false
}

// cleanupUploads removes uploaded files older than uploadMaxAge. Called once
// at server start and lazily during uploads. Uploads land under
// <DataDir>/uploads regardless of the requesting project, so a single
// server-wide sweep is correct in both single- and multi-tenant modes.
func cleanupUploads(dataDir string, logger *slog.Logger) {
	uploadsDir := filepath.Join(dataDir, "uploads")
	entries, err := os.ReadDir(uploadsDir)
	if err != nil {
		return // directory may not exist yet
	}
	cutoff := time.Now().Add(-uploadMaxAge)
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(uploadsDir, e.Name())); err == nil {
				removed++
			}
		}
	}
	if removed > 0 {
		logger.Info("cleaned up old uploads", "removed", removed)
	}
}

// sanitizeFilename removes path separators and other dangerous characters.
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "..", "")
	// Keep only safe characters.
	var b strings.Builder
	for _, r := range name {
		if r == '/' || r == '\\' || r == '\x00' {
			continue
		}
		b.WriteRune(r)
	}
	result := b.String()
	if result == "" {
		result = "upload"
	}
	return result
}
