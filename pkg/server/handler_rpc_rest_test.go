package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// newRPCRESTTestServer wires the /api/rpc/ adapter onto an httptest server
// via a minimal gin engine that registers the RPC routes the same way
// production does (registerRPCRoutes). No project store is wired in, which
// means the scope middleware is a no-op and reads return whatever the
// underlying handler chooses (or methodNotFound when the handler depends
// on uninitialised fields). The point of these tests is the adapter glue,
// not handler behaviour.
func newRPCRESTTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	h := &Handler{
		dataDir: t.TempDir(),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	s := &Server{handler: h, opts: Options{DataDir: h.dataDir}}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	authed := engine.Group("")
	s.registerRPCRoutes(authed)
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	return srv, s
}

func TestRPCREST_RejectsNonPost(t *testing.T) {
	t.Parallel()
	srv, _ := newRPCRESTTestServer(t)
	resp, err := http.Get(srv.URL + "/api/rpc/initialize")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestRPCREST_MissingMethod(t *testing.T) {
	t.Parallel()
	srv, _ := newRPCRESTTestServer(t)
	resp, err := http.Post(srv.URL+"/api/rpc/", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for missing method, got %d", resp.StatusCode)
	}
}

func TestRPCREST_StreamingMethodsBlocked(t *testing.T) {
	t.Parallel()
	srv, _ := newRPCRESTTestServer(t)
	for method := range methodsRequireWebsocket {
		method := method
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			resp, err := http.Post(srv.URL+"/api/rpc/"+method, "application/json", strings.NewReader("{}"))
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("%s: want 405, got %d", method, resp.StatusCode)
			}
			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if msg, _ := body["message"].(string); !strings.Contains(msg, "websocket") {
				t.Fatalf("unexpected error body: %+v", body)
			}
		})
	}
}

func TestRPCREST_InitializeReturnsClientID(t *testing.T) {
	t.Parallel()
	srv, _ := newRPCRESTTestServer(t)
	resp, err := http.Post(srv.URL+"/api/rpc/initialize", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, body %s", resp.StatusCode, buf)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cid, _ := out["clientId"].(string)
	if !strings.HasPrefix(cid, "http-") {
		t.Fatalf("clientId %q should be the http-prefixed token, got %q", cid, cid)
	}
}

func TestRPCREST_InvalidJSONBody(t *testing.T) {
	t.Parallel()
	srv, _ := newRPCRESTTestServer(t)
	resp, err := http.Post(srv.URL+"/api/rpc/initialize", "application/json", strings.NewReader("{ bad"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestRPCREST_UnknownMethodReturns404(t *testing.T) {
	t.Parallel()
	srv, _ := newRPCRESTTestServer(t)
	resp, err := http.Post(srv.URL+"/api/rpc/no/such/method", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d, body %s", resp.StatusCode, buf)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code, _ := body["code"].(float64); int(code) != ErrCodeMethodNotFound {
		t.Fatalf("code: %+v", body)
	}
}

func TestRPCREST_UnauthorizedScopeMapsTo401(t *testing.T) {
	t.Parallel()
	// With a project store wired in but no user in context, any non-skip
	// method must come back as -32002 → 401.
	store := newTestProjectStore(t)
	h := &Handler{
		dataDir:  t.TempDir(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		projects: store,
	}
	s := &Server{handler: h, opts: Options{DataDir: h.dataDir}}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	authed := engine.Group("")
	s.registerRPCRoutes(authed)
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)

	resp, err := http.Post(
		srv.URL+"/api/rpc/thread/list",
		"application/json",
		strings.NewReader(`{"projectId":"p1"}`),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 401, got %d (body=%s)", resp.StatusCode, buf)
	}
}

func TestRPCREST_MissingProjectIDMapsTo400(t *testing.T) {
	t.Parallel()
	store := newTestProjectStore(t)
	seedProjectUser(t, store, "alice")
	h := &Handler{
		dataDir:  t.TempDir(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		projects: store,
	}
	s := &Server{handler: h, opts: Options{DataDir: h.dataDir}}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	// Inject the user via a tiny middleware, mirroring AuthManager.
	engine.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(withUser(c.Request.Context(), "alice", "user"))
		c.Next()
	})
	authed := engine.Group("")
	s.registerRPCRoutes(authed)
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)

	resp, err := http.Post(
		srv.URL+"/api/rpc/thread/list",
		"application/json",
		strings.NewReader(`{}`),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 missing projectId, got %d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if code, _ := body["code"].(float64); int(code) != ErrCodeProjectMissing {
		t.Fatalf("code: %+v", body)
	}
}

func TestHTTPStatusForRPCError(t *testing.T) {
	t.Parallel()
	cases := map[int]int{
		ErrCodeUnauthorized:   http.StatusUnauthorized,
		ErrCodeProjectMissing: http.StatusBadRequest,
		ErrCodeProjectAccess:  http.StatusForbidden,
		ErrCodeProjectStore:   http.StatusInternalServerError,
		ErrCodeMethodNotFound: http.StatusNotFound,
		ErrCodeInvalidParams:  http.StatusBadRequest,
		ErrCodeInternal:       http.StatusInternalServerError,
		-99999:                http.StatusInternalServerError,
	}
	for code, want := range cases {
		if got := httpStatusForRPCError(code); got != want {
			t.Errorf("code=%d: want %d, got %d", code, want, got)
		}
	}
}

func TestDecodeRPCParams(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		body    string
		want    map[string]any
		wantErr bool
	}{
		{"empty", "", map[string]any{}, false},
		{"whitespace", "   ", map[string]any{}, false},
		{"null", "null", map[string]any{}, false},
		{"object", `{"x":1}`, map[string]any{"x": float64(1)}, false},
		{"array rejected", `[1,2,3]`, nil, true},
		{"invalid", `{ broken`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeRPCParams(bytes.NewBufferString(tc.body))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error for %q", tc.body)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %v want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("key %s: got %v want %v", k, got[k], v)
				}
			}
		})
	}
}
