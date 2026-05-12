package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func runHandler(handler gin.HandlerFunc) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	handler(c)
	return rec
}

func decodeEnvelope(t *testing.T, body []byte) ErrorEnvelope {
	t.Helper()
	var env ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody=%s", err, string(body))
	}
	return env
}

func TestErrorHelpers_StatusAndType(t *testing.T) {
	cases := []struct {
		name       string
		fn         func(*gin.Context)
		wantStatus int
		wantType   string
	}{
		{"InvalidRequest", func(c *gin.Context) { InvalidRequest(c, "bad") }, http.StatusBadRequest, ErrTypeInvalidRequest},
		{"Unauthorized", func(c *gin.Context) { Unauthorized(c, "no key") }, http.StatusUnauthorized, ErrTypeAuthentication},
		{"Forbidden", func(c *gin.Context) { Forbidden(c, "nope") }, http.StatusForbidden, ErrTypePermission},
		{"NotFound", func(c *gin.Context) { NotFound(c, "missing") }, http.StatusNotFound, ErrTypeNotFound},
		{"RateLimited", func(c *gin.Context) { RateLimited(c, "too fast") }, http.StatusTooManyRequests, ErrTypeRateLimit},
		{"ServerError", func(c *gin.Context) { ServerError(c, "boom") }, http.StatusInternalServerError, ErrTypeServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := runHandler(c.fn)
			if rec.Code != c.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, c.wantStatus)
			}
			env := decodeEnvelope(t, rec.Body.Bytes())
			if env.Error.Type != c.wantType {
				t.Errorf("type = %q, want %q", env.Error.Type, c.wantType)
			}
			if env.Error.Message == "" {
				t.Error("message must be populated")
			}
		})
	}
}

func TestInvalidRequestField_PopulatesParam(t *testing.T) {
	rec := runHandler(func(c *gin.Context) { InvalidRequestField(c, "model", "missing model") })
	env := decodeEnvelope(t, rec.Body.Bytes())
	if env.Error.Param != "model" {
		t.Errorf("param = %q, want model", env.Error.Param)
	}
	if env.Error.Type != ErrTypeInvalidRequest {
		t.Errorf("type = %q, want invalid_request_error", env.Error.Type)
	}
}

func TestAbortWith_AbortsHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	_ = AbortWith(c, http.StatusTeapot, ErrTypeServerError, "stop here")
	// Mimic the next-middleware probe: gin.Context.IsAborted should be true.
	if !c.IsAborted() {
		t.Error("expected gin context to be marked aborted")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418", rec.Code)
	}
}
