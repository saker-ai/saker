package openai

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cinience/saker/pkg/project"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newTestGateway constructs a Gateway whose authMiddleware is exercised
// directly without mounting any route group. ProjectStore is optional.
func newTestGateway(t *testing.T, opts Options, store *project.Store) *Gateway {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	return &Gateway{
		deps: Deps{
			ProjectStore: store,
			Logger:       logger,
			Options:      opts,
		},
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Log(strings.TrimSpace(string(p))); return len(p), nil }

func runAuth(t *testing.T, g *Gateway, header string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	router := gin.New()
	var seen Identity
	router.Use(g.authMiddleware())
	router.GET("/probe", func(c *gin.Context) {
		seen = IdentityFromContext(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{"user": seen.Username, "bypass": seen.Bypass})
	})
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	router.ServeHTTP(rec, req)
	return rec
}

func TestAuth_NoHeaderRejected(t *testing.T) {
	g := newTestGateway(t, Options{}, nil)
	rec := runAuth(t, g, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("body not OpenAI envelope: %v\nbody=%s", err, rec.Body.String())
	}
	if env.Error.Type != ErrTypeAuthentication {
		t.Errorf("err.type = %q, want authentication_error", env.Error.Type)
	}
}

func TestAuth_NonBearerSchemeRejected(t *testing.T) {
	g := newTestGateway(t, Options{}, nil)
	rec := runAuth(t, g, "Basic dXNlcjpwYXNz")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAuth_DevBypass(t *testing.T) {
	g := newTestGateway(t, Options{DevBypassAuth: true}, nil)
	rec := runAuth(t, g, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with dev bypass", rec.Code)
	}
	var body struct {
		User   string `json:"user"`
		Bypass bool   `json:"bypass"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.User != "localhost" || !body.Bypass {
		t.Errorf("identity = %+v, want localhost/bypass", body)
	}
}

func TestAuth_BearerWithoutStore_Rejects(t *testing.T) {
	g := newTestGateway(t, Options{}, nil)
	rec := runAuth(t, g, "Bearer ak_anything")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (nil store without dev bypass must reject)", rec.Code)
	}
}

func TestAuth_BearerWithoutStore_DevBypassAccepts(t *testing.T) {
	g := newTestGateway(t, Options{DevBypassAuth: true}, nil)
	rec := runAuth(t, g, "Bearer ak_anything")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (nil store with dev bypass)", rec.Code)
	}
	var body struct {
		User string `json:"user"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.User != "anonymous" {
		t.Errorf("expected anonymous identity, got %q", body.User)
	}
}

func TestExtractBearer(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"Bearer abc", "abc"},
		{"bearer abc", "abc"},
		{"BEARER abc", "abc"},
		{"  Bearer   tok  ", "tok"},
		{"Basic xyz", ""},
		{"Bearer", ""},
		{"Bearer ", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := extractBearer(c.in); got != c.want {
				t.Errorf("extractBearer(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
