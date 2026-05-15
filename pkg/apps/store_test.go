package apps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/saker-ai/saker/pkg/canvas"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir())
}

// minimalPublishableDoc returns a Document with one appInput, one
// imageGen, one appOutput wired together. Enough to satisfy
// PublishVersion's "must have ≥1 input and ≥1 output" guard.
func minimalPublishableDoc() *canvas.Document {
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

func TestStoreCRUD(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	// Empty list on a fresh store should return nil/empty, not error.
	got, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List on empty store: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 apps, got %d", len(got))
	}

	created, err := s.Create(ctx, CreateInput{
		Name:           "TestApp",
		Description:    "desc",
		Icon:           "🦊",
		SourceThreadID: "thread-1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned empty ID")
	}
	if created.Visibility != VisibilityPrivate {
		t.Fatalf("expected default visibility=private, got %q", created.Visibility)
	}

	loaded, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.Name != "TestApp" || loaded.Icon != "🦊" {
		t.Fatalf("loaded mismatch: %+v", loaded)
	}

	newName := "Renamed"
	updated, err := s.Update(ctx, created.ID, UpdateInput{Name: &newName})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Renamed" {
		t.Fatalf("Update name: got %q", updated.Name)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) && !updated.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("UpdatedAt did not advance: %v vs %v", created.UpdatedAt, updated.UpdatedAt)
	}

	// List should now return one item.
	all, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].ID != created.ID {
		t.Fatalf("List returned %+v", all)
	}

	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, created.ID); !errors.Is(err, ErrAppNotFound) {
		t.Fatalf("expected ErrAppNotFound after delete, got %v", err)
	}
}

func TestStoreGetMissing(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	_, err := s.Get(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrAppNotFound) {
		t.Fatalf("expected ErrAppNotFound, got %v", err)
	}
}

func TestStoreInvalidAppID(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	cases := []string{"", "../escape", "a/b", `c\d`, ".."}
	for _, id := range cases {
		if _, err := s.Get(context.Background(), id); !errors.Is(err, ErrInvalidAppID) {
			t.Errorf("Get(%q): expected ErrInvalidAppID, got %v", id, err)
		}
	}
}

func TestStorePublishVersionRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	created, err := s.Create(ctx, CreateInput{Name: "Pub", SourceThreadID: "t1"})
	if err != nil {
		t.Fatal(err)
	}

	doc := minimalPublishableDoc()
	v, err := s.PublishVersion(ctx, created.ID, doc, "tester")
	if err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}
	if v.Version == "" {
		t.Fatal("PublishVersion returned empty version")
	}
	if v.PublishedBy != "tester" {
		t.Fatalf("PublishedBy = %q", v.PublishedBy)
	}
	if len(v.Inputs) != 1 || v.Inputs[0].Variable != "topic" {
		t.Fatalf("Inputs mismatch: %+v", v.Inputs)
	}
	if len(v.Outputs) != 1 || v.Outputs[0].Kind != "image" {
		t.Fatalf("Outputs mismatch: %+v", v.Outputs)
	}

	// meta.PublishedVersion should now point to v.Version.
	meta, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.PublishedVersion != v.Version {
		t.Fatalf("meta.PublishedVersion=%q, want %q", meta.PublishedVersion, v.Version)
	}

	// Round-trip via LoadVersion.
	loaded, err := s.LoadVersion(ctx, created.ID, v.Version)
	if err != nil {
		t.Fatalf("LoadVersion: %v", err)
	}
	if loaded.Version != v.Version || len(loaded.Inputs) != 1 || loaded.Document == nil {
		t.Fatalf("LoadVersion mismatch: %+v", loaded)
	}
	if len(loaded.Document.Nodes) != 3 {
		t.Fatalf("expected 3 nodes in snapshot, got %d", len(loaded.Document.Nodes))
	}

	// Snapshot file must exist on disk under versions/.
	versionPath := filepath.Join(s.Root, "apps", created.ID, "versions", v.Version+".json")
	if _, err := os.Stat(versionPath); err != nil {
		t.Fatalf("snapshot not on disk: %v", err)
	}
}

func TestStorePublishVersionRejectsBadDocs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)
	created, err := s.Create(ctx, CreateInput{Name: "X"})
	if err != nil {
		t.Fatal(err)
	}

	// No inputs, no outputs.
	if _, err := s.PublishVersion(ctx, created.ID, &canvas.Document{}, ""); err == nil {
		t.Fatal("expected error publishing empty doc")
	}

	// Inputs but no outputs.
	noOut := &canvas.Document{
		Nodes: []*canvas.Node{
			{ID: "i", Data: map[string]any{"nodeType": "appInput", "appVariable": "x"}},
		},
	}
	if _, err := s.PublishVersion(ctx, created.ID, noOut, ""); err == nil {
		t.Fatal("expected error publishing doc with no outputs")
	}
}

func TestStoreListVersionsSortedDesc(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)
	created, err := s.Create(ctx, CreateInput{Name: "V"})
	if err != nil {
		t.Fatal(err)
	}

	// Hand-write three version files with controlled names so we don't
	// depend on time.Now resolution between PublishVersion calls.
	versionsDir := filepath.Join(s.Root, "apps", created.ID, "versions")
	if err := os.MkdirAll(versionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"2026-01-01-100000.json",
		"2026-05-02-120000.json",
		"2026-03-15-090000.json",
	} {
		raw := []byte(`{"version":"` + name[:len(name)-5] + `"}`)
		if err := os.WriteFile(filepath.Join(versionsDir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.ListVersions(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(got))
	}
	want := []string{"2026-05-02-120000", "2026-03-15-090000", "2026-01-01-100000"}
	for i, w := range want {
		if got[i].Version != w {
			t.Errorf("ListVersions[%d]=%q, want %q", i, got[i].Version, w)
		}
	}
}

func TestStoreListSortedByUpdatedAtDesc(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	a, err := s.Create(ctx, CreateInput{Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	// Force ordering via Update bumping UpdatedAt.
	time.Sleep(2 * time.Millisecond)
	b, err := s.Create(ctx, CreateInput{Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	newName := "A-updated"
	if _, err := s.Update(ctx, a.ID, UpdateInput{Name: &newName}); err != nil {
		t.Fatal(err)
	}

	got, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 apps, got %d", len(got))
	}
	if got[0].ID != a.ID || got[1].ID != b.ID {
		t.Fatalf("expected order [A,B] (A is newer), got [%s,%s]", got[0].ID, got[1].ID)
	}
}

func TestStoreLoadKeysMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)
	created, err := s.Create(ctx, CreateInput{Name: "K"})
	if err != nil {
		t.Fatal(err)
	}

	keys, err := s.LoadKeys(ctx, created.ID)
	if err != nil {
		t.Fatalf("LoadKeys on missing file: %v", err)
	}
	if keys == nil || len(keys.ApiKeys) != 0 || len(keys.ShareTokens) != 0 {
		t.Fatalf("expected empty KeysFile, got %+v", keys)
	}
}

func TestStore_SetPublishedVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	created, err := s.Create(ctx, CreateInput{Name: "RollbackApp", SourceThreadID: "t1"})
	if err != nil {
		t.Fatal(err)
	}

	// Publish twice so we have two distinct versions.
	doc := minimalPublishableDoc()
	v1, err := s.PublishVersion(ctx, created.ID, doc, "tester")
	if err != nil {
		t.Fatalf("PublishVersion v1: %v", err)
	}
	// Small sleep to ensure v2 gets a different timestamp-based version string.
	time.Sleep(2 * time.Millisecond)
	v2, err := s.PublishVersion(ctx, created.ID, doc, "tester")
	if err != nil {
		t.Fatalf("PublishVersion v2: %v", err)
	}
	// After second publish, meta points to v2.
	meta, _ := s.Get(ctx, created.ID)
	if meta.PublishedVersion != v2.Version {
		t.Fatalf("expected v2 after second publish, got %q", meta.PublishedVersion)
	}

	// Happy path: roll back to v1.
	updated, err := s.SetPublishedVersion(ctx, created.ID, v1.Version)
	if err != nil {
		t.Fatalf("SetPublishedVersion: %v", err)
	}
	if updated.PublishedVersion != v1.Version {
		t.Fatalf("SetPublishedVersion: got %q, want %q", updated.PublishedVersion, v1.Version)
	}
	// Confirm via Get that the change persisted.
	reloaded, _ := s.Get(ctx, created.ID)
	if reloaded.PublishedVersion != v1.Version {
		t.Fatalf("Get after rollback: got %q, want %q", reloaded.PublishedVersion, v1.Version)
	}

	// Unknown app.
	if _, err := s.SetPublishedVersion(ctx, "does-not-exist", v1.Version); !errors.Is(err, ErrAppNotFound) {
		t.Fatalf("unknown app: expected ErrAppNotFound, got %v", err)
	}

	// Unknown version.
	if _, err := s.SetPublishedVersion(ctx, created.ID, "2000-01-01-000000"); err == nil {
		t.Fatal("unknown version: expected error, got nil")
	}
}

func TestStoreSaveLoadKeysRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)
	created, err := s.Create(ctx, CreateInput{Name: "K"})
	if err != nil {
		t.Fatal(err)
	}

	in := &KeysFile{
		ApiKeys: []ApiKey{{
			ID: "k1", Hash: "bcrypt:abc", Prefix: "ap_12345", Name: "ci", CreatedAt: time.Now().UTC(),
		}},
		ShareTokens: []ShareToken{{
			Token: "share-abc", CreatedAt: time.Now().UTC(),
		}},
	}
	if err := s.SaveKeys(ctx, created.ID, in); err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}
	out, err := s.LoadKeys(ctx, created.ID)
	if err != nil {
		t.Fatalf("LoadKeys: %v", err)
	}
	if len(out.ApiKeys) != 1 || out.ApiKeys[0].ID != "k1" {
		t.Fatalf("ApiKeys mismatch: %+v", out.ApiKeys)
	}
	if len(out.ShareTokens) != 1 || out.ShareTokens[0].Token != "share-abc" {
		t.Fatalf("ShareTokens mismatch: %+v", out.ShareTokens)
	}
}
