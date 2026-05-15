package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/apps"
)

// Routes for the apps REST API are mounted at /api/apps/ and registered
// onto the gin engine in registerAppsRoutes (gin_routes_apps.go).
// Per-handler bodies live in this file and the apps_rest_*.go siblings.

// classifyAppsError maps an apps package error to (status, public message).
// 4xx covers sentinel errors and validation failures whose strings are safe
// to surface verbatim. Anything else is 500 with the original error returned
// as the second result so callers can decide whether to log or echo it.
func classifyAppsError(err error) (status int, msg string) {
	switch {
	case errors.Is(err, apps.ErrAppNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, apps.ErrNotPublished):
		return http.StatusConflict, err.Error()
	case errors.Is(err, apps.ErrInvalidAppID):
		return http.StatusBadRequest, err.Error()
	}
	// ValidateInputs returns a plain fmt.Errorf wrapping "app inputs
	// invalid: ..." (see pkg/apps/extract.go). Match by substring because
	// it is not a sentinel; same logic for the publish-time validation
	// strings emitted by Store.PublishVersion.
	m := err.Error()
	if strings.Contains(m, "app inputs invalid") ||
		strings.Contains(m, "canvas has no appInput nodes") ||
		strings.Contains(m, "canvas has no appOutput nodes") ||
		strings.Contains(m, "version not found") {
		return http.StatusUnprocessableEntity, m
	}
	return http.StatusInternalServerError, m
}

// writeAppsError maps apps package sentinel errors and validation failures
// to appropriate HTTP status codes. All other errors bubble up as 500 with
// the underlying error message — fine for cookie-authenticated admins.
func writeAppsError(w http.ResponseWriter, err error) {
	status, msg := classifyAppsError(err)
	http.Error(w, msg, status)
}

// writeAppsErrorPublic is the anonymous-share variant: 4xx responses keep
// their public-safe message, 5xx responses are redacted to a stable
// "internal error" body with an 8-char ref tag that is also logged with the
// original error so operators can correlate the report. Avoids leaking
// internal paths, model IDs, or stack fragments to the public internet.
func writeAppsErrorPublic(w http.ResponseWriter, err error, logger *slog.Logger, where string) {
	status, msg := classifyAppsError(err)
	if status < 500 {
		http.Error(w, msg, status)
		return
	}
	ref := publicErrorRef()
	if logger != nil {
		logger.Error("apps public 5xx", "where", where, "ref", ref, "error", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "internal error",
		"ref":   ref,
	})
}

// publicErrorRef returns an 8-char hex tag identifying a single 5xx so the
// client report ("got internal error ref ab12cd34") can be grep'd against
// the server log line that recorded the underlying cause.
func publicErrorRef() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read on Linux's getrandom never fails in practice; if it
		// somehow does, fall back to a clock-derived tag — still unique
		// enough to correlate within a 1ns window.
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}
