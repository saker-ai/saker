package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHandleHealth(t *testing.T) {
	t.Parallel()
	s := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	gin.SetMode(gin.TestMode)
	eng := gin.New()
	eng.GET("/health", s.handleHealth)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	eng.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	require.Equal(t, "ok", body["status"])
	require.NotEmpty(t, body["time"])

	// time should round-trip RFC3339-style.
	require.True(t, strings.Contains(body["time"].(string), "T"))
}
