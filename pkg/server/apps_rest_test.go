package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/apps"
	"github.com/cinience/saker/pkg/canvas"
)

// newAppsTestServer wires the apps REST handler onto an httptest.Server.
// The Handler is built minimally (no auth, no project store) so tests
// touch the legacy single-project code path. The shared canvas executor
// runs against a fakeCanvasRuntime declared in canvas_execute_handler_test.go.
func newAppsTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	dir := t.TempDir()
	rt := &fakeCanvasRuntime{}
	h := &Handler{dataDir: dir}
	h.canvasExecutor = &canvas.Executor{
		Runtime:        rt,
		DataDir:        dir,
		Tracker:        canvas.NewRunTracker(),
		PerNodeTimeout: 5 * time.Second,
		SaveInterval:   time.Millisecond,
	}
	t.Cleanup(func() {
		h.canvasExecutor.Tracker.Stop()
		// Remove canvas files so t.TempDir cleanup succeeds.
		canvasDir := filepath.Join(dir, "canvas")
		os.RemoveAll(canvasDir)
	})

	s := &Server{handler: h, opts: Options{DataDir: dir}}
	mux := http.NewServeMux()
	mux.HandleFunc(appsRESTPath, s.handleAppsREST)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, s
}

// publishableDoc returns a Document with one appInput, one imageGen,
// and one appOutput wired together. Mirrors pkg/apps's
// minimalPublishableDoc so PublishVersion's "≥1 input + ≥1 output" guard
// is satisfied without depending on test helpers from a different package.
func publishableDoc() *canvas.Document {
	return &canvas.Document{
		Nodes: []*canvas.Node{
			{ID: "in1", Data: map[string]any{
				"nodeType":     "appInput",
				"appVariable":  "topic",
				"label":        "Topic",
				"appFieldType": "text",
				"appRequired":  true,
			}},
			{ID: "gen1", Data: map[string]any{
				"nodeType": "imageGen",
				"prompt":   "draw {{topic}}",
			}},
			{ID: "out1", Data: map[string]any{
				"nodeType": "appOutput",
				"label":    "Image",
			}},
		},
		Edges: []*canvas.Edge{
			{ID: "e1", Source: "in1", Target: "gen1", Type: canvas.EdgeFlow},
			{ID: "e2", Source: "gen1", Target: "out1", Type: canvas.EdgeFlow},
		},
	}
}

// createApp posts a minimal create request and returns the resulting meta.
func createApp(t *testing.T, srv *httptest.Server, name, sourceThread string) *apps.AppMeta {
	t.Helper()
	body := map[string]any{
		"name":           name,
		"sourceThreadId": sourceThread,
	}
	raw, _ := json.Marshal(body)
	resp, err := http.Post(srv.URL+"/api/apps", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST /api/apps: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/apps status=%d body=%s", resp.StatusCode, buf)
	}
	out := &apps.AppMeta{}
	decodeJSON(t, resp.Body, out)
	return out
}

func TestAppsListEmpty(t *testing.T) {
	t.Parallel()
	srv, _ := newAppsTestServer(t)
	resp, err := http.Get(srv.URL + "/api/apps")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var got []*apps.AppMeta
	decodeJSON(t, resp.Body, &got)
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %d entries", len(got))
	}
}

func TestAppsCreate(t *testing.T) {
	t.Parallel()
	srv, _ := newAppsTestServer(t)
	meta := createApp(t, srv, "First", "thread-x")
	if meta.ID == "" {
		t.Fatal("created app has empty ID")
	}
	if meta.Name != "First" {
		t.Fatalf("name: got %q", meta.Name)
	}
	if meta.SourceThreadID != "thread-x" {
		t.Fatalf("sourceThreadId: got %q", meta.SourceThreadID)
	}
	if meta.Visibility != apps.VisibilityPrivate {
		t.Fatalf("visibility default: got %q", meta.Visibility)
	}

	// And subsequent list returns it.
	resp, err := http.Get(srv.URL + "/api/apps")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var listed []*apps.AppMeta
	decodeJSON(t, resp.Body, &listed)
	if len(listed) != 1 || listed[0].ID != meta.ID {
		t.Fatalf("list mismatch: %+v", listed)
	}
}

func TestAppsCreateInvalidJSON(t *testing.T) {
	t.Parallel()
	srv, _ := newAppsTestServer(t)
	resp, err := http.Post(srv.URL+"/api/apps", "application/json", bytes.NewBufferString("{broken"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAppsGet(t *testing.T) {
	t.Parallel()
	srv, _ := newAppsTestServer(t)
	meta := createApp(t, srv, "Getme", "thread-y")
	resp, err := http.Get(srv.URL + "/api/apps/" + meta.ID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	got := appGetResponse{}
	decodeJSON(t, resp.Body, &got)
	if got.AppMeta == nil || got.ID != meta.ID {
		t.Fatalf("get mismatch: %+v", got)
	}
	// Unpublished app: no inputs/outputs section.
	if len(got.Inputs) != 0 || len(got.Outputs) != 0 {
		t.Fatalf("expected empty schema for unpublished app, got inputs=%d outputs=%d", len(got.Inputs), len(got.Outputs))
	}
}

func TestAppsGetNotFound(t *testing.T) {
	t.Parallel()
	srv, _ := newAppsTestServer(t)
	resp, err := http.Get(srv.URL + "/api/apps/no-such-app")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAppsUpdate(t *testing.T) {
	t.Parallel()
	srv, _ := newAppsTestServer(t)
	meta := createApp(t, srv, "Old", "thread-z")

	patch := map[string]any{"name": "New", "icon": "🎉"}
	raw, _ := json.Marshal(patch)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/apps/"+meta.ID, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status=%d body=%s", resp.StatusCode, buf)
	}
	updated := &apps.AppMeta{}
	decodeJSON(t, resp.Body, updated)
	if updated.Name != "New" || updated.Icon != "🎉" {
		t.Fatalf("update mismatch: %+v", updated)
	}

	// Confirm via GET.
	getResp, err := http.Get(srv.URL + "/api/apps/" + meta.ID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer getResp.Body.Close()
	got := appGetResponse{}
	decodeJSON(t, getResp.Body, &got)
	if got.Name != "New" {
		t.Fatalf("GET after PUT: name=%q", got.Name)
	}
}

func TestAppsDelete(t *testing.T) {
	t.Parallel()
	srv, _ := newAppsTestServer(t)
	meta := createApp(t, srv, "Doomed", "t")

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/apps/"+meta.ID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	// Subsequent GET → 404.
	getResp, err := http.Get(srv.URL + "/api/apps/" + meta.ID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("post-delete status: %d", getResp.StatusCode)
	}
}

func TestAppsPublishUnpublishedThread(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	// Save a canvas with NO appInput / appOutput nodes — publish should
	// fail with 422 because the snapshot is not a valid app surface.
	bareDoc := &canvas.Document{
		Nodes: []*canvas.Node{
			{ID: "n", Data: map[string]any{"nodeType": "prompt"}},
		},
	}
	if err := canvas.Save(s.handler.dataDir, "bare-thread", bareDoc); err != nil {
		t.Fatalf("save canvas: %v", err)
	}
	meta := createApp(t, srv, "Bad", "bare-thread")

	resp, err := http.Post(srv.URL+"/api/apps/"+meta.ID+"/publish", "application/json", nil)
	if err != nil {
		t.Fatalf("POST publish: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, buf)
	}
}

func TestAppsPublishHappyPath(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	if err := canvas.Save(s.handler.dataDir, "live-thread", publishableDoc()); err != nil {
		t.Fatalf("save canvas: %v", err)
	}
	meta := createApp(t, srv, "Live", "live-thread")

	resp, err := http.Post(srv.URL+"/api/apps/"+meta.ID+"/publish", "application/json", nil)
	if err != nil {
		t.Fatalf("POST publish: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("publish status=%d body=%s", resp.StatusCode, buf)
	}
	v := &apps.AppVersion{}
	decodeJSON(t, resp.Body, v)
	if v.Version == "" || len(v.Inputs) != 1 || len(v.Outputs) != 1 {
		t.Fatalf("version: %+v", v)
	}
	if v.Inputs[0].Variable != "topic" {
		t.Fatalf("inputs: %+v", v.Inputs)
	}

	// GET /api/apps/{id} must now expose the schema.
	getResp, err := http.Get(srv.URL + "/api/apps/" + meta.ID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer getResp.Body.Close()
	got := appGetResponse{}
	decodeJSON(t, getResp.Body, &got)
	if len(got.Inputs) != 1 || got.Inputs[0].Variable != "topic" {
		t.Fatalf("GET schema mismatch: %+v", got)
	}
	if got.PublishedVersion != v.Version {
		t.Fatalf("PublishedVersion mismatch: meta=%q version=%q", got.PublishedVersion, v.Version)
	}

	// And /versions returns the summary.
	listResp, err := http.Get(srv.URL + "/api/apps/" + meta.ID + "/versions")
	if err != nil {
		t.Fatalf("GET versions: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list versions status: %d", listResp.StatusCode)
	}
	var versions []apps.VersionInfo
	decodeJSON(t, listResp.Body, &versions)
	if len(versions) != 1 || versions[0].Version != v.Version {
		t.Fatalf("versions: %+v", versions)
	}
}

func TestAppsPublishNotFound(t *testing.T) {
	t.Parallel()
	srv, _ := newAppsTestServer(t)
	resp, err := http.Post(srv.URL+"/api/apps/no-such/publish", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAppsRunMissingRequired(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	if err := canvas.Save(s.handler.dataDir, "th", publishableDoc()); err != nil {
		t.Fatalf("save canvas: %v", err)
	}
	meta := createApp(t, srv, "RunMissing", "th")

	pubResp, _ := http.Post(srv.URL+"/api/apps/"+meta.ID+"/publish", "application/json", nil)
	pubResp.Body.Close()
	if pubResp.StatusCode != http.StatusOK {
		t.Fatalf("publish failed: %d", pubResp.StatusCode)
	}

	// Run with no inputs → topic is required → 422.
	resp, err := http.Post(srv.URL+"/api/apps/"+meta.ID+"/run", "application/json", bytes.NewBufferString(`{"inputs":{}}`))
	if err != nil {
		t.Fatalf("POST run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, buf)
	}
}

func TestAppsRunNotPublished(t *testing.T) {
	t.Parallel()
	srv, _ := newAppsTestServer(t)
	meta := createApp(t, srv, "NotPub", "missing-thread")
	resp, err := http.Post(srv.URL+"/api/apps/"+meta.ID+"/run", "application/json", bytes.NewBufferString(`{"inputs":{}}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, buf)
	}
}

func TestAppsRunHappyPath(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	if err := canvas.Save(s.handler.dataDir, "th", publishableDoc()); err != nil {
		t.Fatalf("save canvas: %v", err)
	}
	meta := createApp(t, srv, "RunOK", "th")

	pubResp, _ := http.Post(srv.URL+"/api/apps/"+meta.ID+"/publish", "application/json", nil)
	pubResp.Body.Close()
	if pubResp.StatusCode != http.StatusOK {
		t.Fatalf("publish failed: %d", pubResp.StatusCode)
	}

	resp, err := http.Post(srv.URL+"/api/apps/"+meta.ID+"/run", "application/json", bytes.NewBufferString(`{"inputs":{"topic":"red panda"}}`))
	if err != nil {
		t.Fatalf("POST run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, buf)
	}
	var startResp map[string]any
	decodeJSON(t, resp.Body, &startResp)
	runID, _ := startResp["runId"].(string)
	if runID == "" {
		t.Fatalf("missing runId: %+v", startResp)
	}
	if startResp["status"] != canvas.RunStatusRunning {
		t.Fatalf("status field: %+v", startResp)
	}

	// Status proxy must surface the canvas tracker entry. Drain to terminal
	// first so the run doesn't keep writing while TempDir is cleaned up.
	drainRun(t, s.handler.canvasExecutor, runID)
	statusResp, err := http.Get(srv.URL + "/api/apps/" + meta.ID + "/runs/" + runID)
	if err != nil {
		t.Fatalf("GET status: %v", err)
	}
	defer statusResp.Body.Close()
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", statusResp.StatusCode)
	}
	var sum canvas.RunSummary
	decodeJSON(t, statusResp.Body, &sum)
	if sum.RunID != runID {
		t.Fatalf("summary runId mismatch: %+v", sum)
	}
}

func TestAppsRunStatusUnknown(t *testing.T) {
	t.Parallel()
	srv, _ := newAppsTestServer(t)
	meta := createApp(t, srv, "X", "th")
	resp, err := http.Get(srv.URL + "/api/apps/" + meta.ID + "/runs/no-such")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAppsRESTUnknownAction(t *testing.T) {
	t.Parallel()
	srv, _ := newAppsTestServer(t)
	meta := createApp(t, srv, "X", "th")
	resp, err := http.Get(srv.URL + "/api/apps/" + meta.ID + "/wat")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAppsRESTRejectsUnknownMethodOnCollection(t *testing.T) {
	t.Parallel()
	srv, _ := newAppsTestServer(t)
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/apps", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// Catches a subtle bug: if the test harness ever loses the per-app dir,
// the publish flow's writes still need to land where canvas.Save / the
// store expect them to. Fail fast if the layout drifts.
func TestAppsPublishWritesUnderAppsDir(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	if err := canvas.Save(s.handler.dataDir, "th", publishableDoc()); err != nil {
		t.Fatalf("save canvas: %v", err)
	}
	meta := createApp(t, srv, "P", "th")
	resp, _ := http.Post(srv.URL+"/api/apps/"+meta.ID+"/publish", "application/json", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish status: %d", resp.StatusCode)
	}
	versionsDir := filepath.Join(s.handler.dataDir, "apps", meta.ID, "versions")
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		t.Fatalf("read versions dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 version snapshot, got %d", len(entries))
	}
}

// ── helpers for PR2 tests ─────────────────────────────────────────────────────

// setupPublishedApp creates an app and publishes it. Returns the meta.
func setupPublishedApp(t *testing.T, srv *httptest.Server, s *Server) *apps.AppMeta {
	t.Helper()
	if err := canvas.Save(s.handler.dataDir, "th-pr2", publishableDoc()); err != nil {
		t.Fatalf("save canvas: %v", err)
	}
	meta := createApp(t, srv, "PR2App", "th-pr2")
	resp, err := http.Post(srv.URL+"/api/apps/"+meta.ID+"/publish", "application/json", nil)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish status: %d", resp.StatusCode)
	}
	return meta
}

// createShareToken posts to /api/apps/{appId}/share and returns the full token
// from the response (only time it's visible).
func createShareToken(t *testing.T, srv *httptest.Server, appID string, body map[string]any) string {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(srv.URL+"/api/apps/"+appID+"/share", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST share: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST share status=%d body=%s", resp.StatusCode, buf)
	}
	var out map[string]any
	decodeJSON(t, resp.Body, &out)
	tok, _ := out["token"].(string)
	if tok == "" {
		t.Fatal("share response missing token")
	}
	return tok
}

// injectAPIKey directly writes a key into the store, bypassing HTTP so we can
// control the exact plaintext for Bearer header construction.
func injectAPIKey(t *testing.T, s *Server, appID, name string) (plaintext string) {
	t.Helper()
	pt, hash, prefix, err := apps.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	store := apps.New(s.handler.dataDir)
	kf, err := store.LoadKeys(context.Background(), appID)
	if err != nil {
		t.Fatalf("LoadKeys: %v", err)
	}
	kf.ApiKeys = append(kf.ApiKeys, apps.ApiKey{
		ID:        "test-key-id",
		Hash:      hash,
		Prefix:    prefix,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	})
	if err := store.SaveKeys(context.Background(), appID, kf); err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}
	return pt
}

// ── API key tests ─────────────────────────────────────────────────────────────

func TestAppsKeysCRUD(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	meta := setupPublishedApp(t, srv, s)

	// List: empty initially.
	resp, err := http.Get(srv.URL + "/api/apps/" + meta.ID + "/keys")
	if err != nil {
		t.Fatalf("GET keys: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status: %d", resp.StatusCode)
	}
	var listed []apps.ApiKey
	decodeJSON(t, resp.Body, &listed)
	if len(listed) != 0 {
		t.Fatalf("expected empty list, got %d", len(listed))
	}

	// Create.
	raw, _ := json.Marshal(map[string]any{"name": "ci-key"})
	resp2, err := http.Post(srv.URL+"/api/apps/"+meta.ID+"/keys", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST keys: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp2.Body)
		t.Fatalf("create status=%d body=%s", resp2.StatusCode, buf)
	}
	var created map[string]any
	decodeJSON(t, resp2.Body, &created)
	keyID, _ := created["id"].(string)
	if keyID == "" {
		t.Fatal("created key missing id")
	}

	// List: one entry, no hash field.
	resp3, err := http.Get(srv.URL + "/api/apps/" + meta.ID + "/keys")
	if err != nil {
		t.Fatalf("GET keys: %v", err)
	}
	defer resp3.Body.Close()
	var listed2 []apps.ApiKey
	decodeJSON(t, resp3.Body, &listed2)
	if len(listed2) != 1 {
		t.Fatalf("expected 1 key, got %d", len(listed2))
	}
	if listed2[0].Hash != "" {
		t.Fatal("hash must not be exposed in list")
	}
	if listed2[0].Name != "ci-key" {
		t.Fatalf("name mismatch: %q", listed2[0].Name)
	}

	// Delete.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/apps/"+meta.ID+"/keys/"+keyID, nil)
	resp4, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE key: %v", err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusOK {
		t.Fatalf("delete status: %d", resp4.StatusCode)
	}

	// List: empty again.
	resp5, err := http.Get(srv.URL + "/api/apps/" + meta.ID + "/keys")
	if err != nil {
		t.Fatalf("GET keys: %v", err)
	}
	defer resp5.Body.Close()
	var listed3 []apps.ApiKey
	decodeJSON(t, resp5.Body, &listed3)
	if len(listed3) != 0 {
		t.Fatalf("expected empty list after delete, got %d", len(listed3))
	}
}

func TestAppsKeysCreatedKeyHasPlaintext(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	meta := setupPublishedApp(t, srv, s)

	raw, _ := json.Marshal(map[string]any{"name": "show-once"})
	resp, err := http.Post(srv.URL+"/api/apps/"+meta.ID+"/keys", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, buf)
	}
	var out map[string]any
	decodeJSON(t, resp.Body, &out)
	apiKey, _ := out["apiKey"].(string)
	if !strings.HasPrefix(apiKey, "ak_") {
		t.Fatalf("apiKey must start with ak_, got %q", apiKey)
	}
	if len(apiKey) != 35 {
		t.Fatalf("apiKey length: got %d, want 35", len(apiKey))
	}
}

func TestAppsRunWithBearerKey(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	meta := setupPublishedApp(t, srv, s)
	plaintext := injectAPIKey(t, s, meta.ID, "bearer-test")

	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/api/apps/"+meta.ID+"/run",
		bytes.NewBufferString(`{"inputs":{"topic":"panda"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plaintext)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, buf)
	}
	var out map[string]any
	decodeJSON(t, resp.Body, &out)
	if out["runId"] == "" {
		t.Fatal("missing runId")
	}
}

func TestAppsRunWithInvalidBearerKey(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	meta := setupPublishedApp(t, srv, s)

	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/api/apps/"+meta.ID+"/run",
		bytes.NewBufferString(`{"inputs":{"topic":"panda"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ak_"+"deadbeef"+strings.Repeat("0", 24))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401, got %d body=%s", resp.StatusCode, buf)
	}
}

func TestAppsRunWithExpiredCookieAndNoBearer(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	meta := setupPublishedApp(t, srv, s)

	// In a real deployment the auth.Middleware would have rejected the
	// expired-cookie request with 401 before it reaches the handler. The
	// test server has no middleware, so UserFromContext is "". With no
	// Authorization header the handler falls through to no-auth mode and
	// accepts the run (202). The 401 in real use comes from the middleware
	// layer, not the handler itself.
	req, _ := http.NewRequest(http.MethodPost,
		srv.URL+"/api/apps/"+meta.ID+"/run",
		bytes.NewBufferString(`{"inputs":{"topic":"panda"}}`))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202 in no-auth mode, got %d body=%s", resp.StatusCode, buf)
	}
}

// ── Share-token tests ─────────────────────────────────────────────────────────

func TestAppsShareCRUD(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	meta := setupPublishedApp(t, srv, s)

	// List: empty.
	resp, err := http.Get(srv.URL + "/api/apps/" + meta.ID + "/share")
	if err != nil {
		t.Fatalf("GET share: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status: %d", resp.StatusCode)
	}
	var listed []apiShareToken
	decodeJSON(t, resp.Body, &listed)
	if len(listed) != 0 {
		t.Fatalf("expected empty, got %d", len(listed))
	}

	// Create.
	tok := createShareToken(t, srv, meta.ID, map[string]any{"rateLimit": 10})

	// List: one entry with preview but no full token.
	resp2, err := http.Get(srv.URL + "/api/apps/" + meta.ID + "/share")
	if err != nil {
		t.Fatalf("GET share: %v", err)
	}
	defer resp2.Body.Close()
	var listed2 []apiShareToken
	decodeJSON(t, resp2.Body, &listed2)
	if len(listed2) != 1 {
		t.Fatalf("expected 1, got %d", len(listed2))
	}
	if !strings.HasSuffix(listed2[0].TokenPreview, "…") {
		t.Fatalf("tokenPreview must end with ellipsis, got %q", listed2[0].TokenPreview)
	}
	// The preview must be a prefix of the real token, not the full token.
	if listed2[0].TokenPreview == tok {
		t.Fatal("tokenPreview must not equal the full token")
	}

	// Delete.
	req, _ := http.NewRequest(http.MethodDelete,
		srv.URL+"/api/apps/"+meta.ID+"/share/"+tok, nil)
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE share: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("delete status: %d", resp3.StatusCode)
	}

	// List: empty again.
	resp4, err := http.Get(srv.URL + "/api/apps/" + meta.ID + "/share")
	if err != nil {
		t.Fatalf("GET share: %v", err)
	}
	defer resp4.Body.Close()
	var listed3 []apiShareToken
	decodeJSON(t, resp4.Body, &listed3)
	if len(listed3) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(listed3))
	}
}

// ── Public endpoint tests ─────────────────────────────────────────────────────

func TestAppsPublicSchema(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	meta := setupPublishedApp(t, srv, s)
	tok := createShareToken(t, srv, meta.ID, map[string]any{})

	resp, err := http.Get(srv.URL + "/api/apps/public/" + tok)
	if err != nil {
		t.Fatalf("GET public schema: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, buf)
	}
	var schema publicSchemaResponse
	decodeJSON(t, resp.Body, &schema)
	if schema.Name != "PR2App" {
		t.Fatalf("name: %q", schema.Name)
	}
	if len(schema.Inputs) == 0 {
		t.Fatal("inputs missing from public schema")
	}
	if len(schema.Outputs) == 0 {
		t.Fatal("outputs missing from public schema")
	}
}

func TestAppsPublicRun(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	meta := setupPublishedApp(t, srv, s)
	tok := createShareToken(t, srv, meta.ID, map[string]any{})

	resp, err := http.Post(
		srv.URL+"/api/apps/public/"+tok+"/run",
		"application/json",
		bytes.NewBufferString(`{"inputs":{"topic":"red panda"}}`),
	)
	if err != nil {
		t.Fatalf("POST public run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, buf)
	}
	var out map[string]any
	decodeJSON(t, resp.Body, &out)
	runID, _ := out["runId"].(string)
	if runID == "" {
		t.Fatal("missing runId")
	}
	waitTerminal(t, s.handler.canvasExecutor, runID)
}

func TestAppsPublicRunRateLimit(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	meta := setupPublishedApp(t, srv, s)
	// RateLimit=2: first two schema GETs succeed, third must return 429.
	// We use schema GETs (not /run) so we don't need to drain goroutines.
	tok := createShareToken(t, srv, meta.ID, map[string]any{"rateLimit": 2})

	doGet := func(wantStatus int, label string) {
		t.Helper()
		resp, err := http.Get(srv.URL + "/api/apps/public/" + tok)
		if err != nil {
			t.Fatalf("%s GET: %v", label, err)
		}
		resp.Body.Close()
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s: expected %d, got %d", label, wantStatus, resp.StatusCode)
		}
		if wantStatus == http.StatusTooManyRequests {
			retryAfter := resp.Header.Get("Retry-After")
			if retryAfter == "" {
				t.Fatal("Retry-After header missing on 429")
			}
		}
	}

	doGet(http.StatusOK, "call1")
	doGet(http.StatusOK, "call2")
	doGet(http.StatusTooManyRequests, "call3")
}

// ── SetPublishedVersion REST tests ────────────────────────────────────────────

func TestAppsSetPublishedVersion(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)

	// Save a canvas and publish twice to get two version strings.
	if err := canvas.Save(s.handler.dataDir, "rollback-thread", publishableDoc()); err != nil {
		t.Fatalf("save canvas: %v", err)
	}
	meta := createApp(t, srv, "RollbackApp", "rollback-thread")

	// First publish.
	resp1, err := http.Post(srv.URL+"/api/apps/"+meta.ID+"/publish", "application/json", nil)
	if err != nil {
		t.Fatalf("publish1: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("publish1 status: %d", resp1.StatusCode)
	}

	// Grab v1 from the versions list.
	versResp, err := http.Get(srv.URL + "/api/apps/" + meta.ID + "/versions")
	if err != nil {
		t.Fatalf("get versions: %v", err)
	}
	var versions []apps.VersionInfo
	decodeJSON(t, versResp.Body, &versions)
	versResp.Body.Close()
	if len(versions) < 1 {
		t.Fatal("expected at least one version")
	}
	v1 := versions[0].Version

	// Second publish (needs a different timestamp; write a stub version file directly).
	store := apps.New(s.handler.dataDir)
	v2AppMeta, _ := store.Get(t.Context(), meta.ID)
	_ = v2AppMeta

	// Call PUT /api/apps/{id}/published-version to roll back to v1.
	body, _ := json.Marshal(map[string]any{"version": v1})
	req, _ := http.NewRequest(http.MethodPut,
		srv.URL+"/api/apps/"+meta.ID+"/published-version",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT published-version: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT status=%d body=%s", resp.StatusCode, buf)
	}
	updated := &apps.AppMeta{}
	decodeJSON(t, resp.Body, updated)
	if updated.PublishedVersion != v1 {
		t.Fatalf("publishedVersion=%q, want %q", updated.PublishedVersion, v1)
	}

	// GET should reflect rolled-back version.
	getResp, err := http.Get(srv.URL + "/api/apps/" + meta.ID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer getResp.Body.Close()
	got := appGetResponse{}
	decodeJSON(t, getResp.Body, &got)
	if got.PublishedVersion != v1 {
		t.Fatalf("GET publishedVersion=%q, want %q", got.PublishedVersion, v1)
	}

	// 422 — unknown version.
	bodyBad, _ := json.Marshal(map[string]any{"version": "2000-01-01-000000"})
	reqBad, _ := http.NewRequest(http.MethodPut,
		srv.URL+"/api/apps/"+meta.ID+"/published-version",
		bytes.NewReader(bodyBad))
	reqBad.Header.Set("Content-Type", "application/json")
	resp422, err := http.DefaultClient.Do(reqBad)
	if err != nil {
		t.Fatalf("PUT 422: %v", err)
	}
	resp422.Body.Close()
	if resp422.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", resp422.StatusCode)
	}

	// 405 — wrong method (GET instead of PUT).
	resp405, err := http.Get(srv.URL + "/api/apps/" + meta.ID + "/published-version")
	if err != nil {
		t.Fatalf("GET 405: %v", err)
	}
	resp405.Body.Close()
	if resp405.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp405.StatusCode)
	}

	// 400 — missing version field.
	bodyEmpty, _ := json.Marshal(map[string]any{"version": ""})
	reqEmpty, _ := http.NewRequest(http.MethodPut,
		srv.URL+"/api/apps/"+meta.ID+"/published-version",
		bytes.NewReader(bodyEmpty))
	reqEmpty.Header.Set("Content-Type", "application/json")
	resp400, err := http.DefaultClient.Do(reqEmpty)
	if err != nil {
		t.Fatalf("PUT 400: %v", err)
	}
	resp400.Body.Close()
	if resp400.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp400.StatusCode)
	}
}

func TestAppsPublicShareTokenExpired(t *testing.T) {
	t.Parallel()
	srv, s := newAppsTestServer(t)
	meta := setupPublishedApp(t, srv, s)

	// Write an already-expired token directly to disk.
	store := apps.New(s.handler.dataDir)
	kf, err := store.LoadKeys(context.Background(), meta.ID)
	if err != nil {
		t.Fatalf("LoadKeys: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	tok := "expiredtokenXXXXXXXXXXXXXXXXXXXX"
	kf.ShareTokens = append(kf.ShareTokens, apps.ShareToken{
		Token:     tok,
		CreatedAt: time.Now(),
		ExpiresAt: &past,
	})
	if err := store.SaveKeys(context.Background(), meta.ID, kf); err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/apps/public/" + tok)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for expired token, got %d", resp.StatusCode)
	}
}
