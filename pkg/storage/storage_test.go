package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mojatter/s2"
)

func TestOpen_OSFS_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	st, emb, err := Open(ctx, Config{Backend: BackendOSFS}, dir)
	if err != nil {
		t.Fatalf("Open osfs: %v", err)
	}
	if emb != nil {
		t.Fatalf("osfs backend should not return embedded server")
	}

	key := "p1/image/aa/aabbcc.png"
	body := []byte("hello-png")
	if err := st.Put(ctx, s2.NewObjectBytes(key, body)); err != nil {
		t.Fatalf("put: %v", err)
	}

	exists, err := st.Exists(ctx, key)
	if err != nil || !exists {
		t.Fatalf("exists got=%v err=%v", exists, err)
	}

	obj, err := st.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	rc, err := obj.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("readall: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("body got=%q want=%q", got, body)
	}

	// Verify the file actually landed under the configured root.
	wantPath := filepath.Join(dir, "media", key)
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected file on disk at %s: %v", wantPath, err)
	}
}

func TestOpen_MemFS_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	st, _, err := Open(ctx, Config{Backend: BackendMemFS}, "")
	if err != nil {
		t.Fatalf("Open memfs: %v", err)
	}

	key := "tenant-a/p1/audio/de/deadbeef.wav"
	if err := st.Put(ctx, s2.NewObjectBytes(key, []byte("wav-data"))); err != nil {
		t.Fatalf("put: %v", err)
	}
	obj, err := st.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if obj.Length() != uint64(len("wav-data")) {
		t.Fatalf("length got=%d", obj.Length())
	}
}

func TestOpen_UnknownBackend(t *testing.T) {
	t.Parallel()
	_, _, err := Open(context.Background(), Config{Backend: "nope"}, "")
	if err == nil {
		t.Fatalf("expected error for unknown backend")
	}
}

func TestConfig_Defaults(t *testing.T) {
	t.Parallel()
	cfg := Config{}.withDefaults("/data")
	if cfg.Backend != BackendOSFS {
		t.Errorf("backend default = %q", cfg.Backend)
	}
	if cfg.PublicBaseURL != DefaultPublicBaseURL {
		t.Errorf("publicBaseURL default = %q", cfg.PublicBaseURL)
	}
	if cfg.OSFS.Root != "/data/media" {
		t.Errorf("osfs root default = %q", cfg.OSFS.Root)
	}
	// Embedded mode defaults to "external" — no listener, so Addr stays empty.
	if cfg.Embedded.Mode != ModeExternal {
		t.Errorf("embedded mode default = %q want=%q", cfg.Embedded.Mode, ModeExternal)
	}
	if cfg.Embedded.Addr != "" {
		t.Errorf("embedded addr default = %q want empty (external mode)", cfg.Embedded.Addr)
	}
}

// TestConfig_Defaults_StandaloneFillsAddr verifies that opting into standalone
// mode flips Addr to the legacy 127.0.0.1:9100 default when unset.
func TestConfig_Defaults_StandaloneFillsAddr(t *testing.T) {
	t.Parallel()
	cfg := Config{Embedded: EmbeddedConfig{Mode: ModeStandalone}}.withDefaults("/data")
	if cfg.Embedded.Mode != ModeStandalone {
		t.Errorf("embedded mode = %q want=%q", cfg.Embedded.Mode, ModeStandalone)
	}
	if cfg.Embedded.Addr != "127.0.0.1:9100" {
		t.Errorf("embedded addr default = %q want=127.0.0.1:9100", cfg.Embedded.Addr)
	}
}

func TestConfig_PublicURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		base string
		key  string
		want string
	}{
		{"default", "/media", "p/image/aa/abc.png", "/media/p/image/aa/abc.png"},
		{"trailing slash", "/media/", "p/abc.png", "/media/p/abc.png"},
		{"absolute cdn", "https://cdn.example.com", "k.png", "https://cdn.example.com/k.png"},
		{"empty base", "", "k.png", "/k.png"},
		{"key with leading /", "/media", "/p/abc.png", "/media/p/abc.png"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := Config{PublicBaseURL: c.base}.PublicURL(c.key)
			if got != c.want {
				t.Errorf("got=%q want=%q", got, c.want)
			}
		})
	}
}

func TestConfig_Key(t *testing.T) {
	t.Parallel()
	cfg := Config{}
	got := cfg.Key("proj1", "image", "aabbccddee", ".png")
	want := "proj1/image/aa/aabbccddee.png"
	if got != want {
		t.Errorf("got=%q want=%q", got, want)
	}

	cfgT := Config{TenantPrefix: "tenant-x"}
	got = cfgT.Key("proj1", "video", "ff00aa", ".mp4")
	want = "tenant-x/proj1/video/ff/ff00aa.mp4"
	if got != want {
		t.Errorf("with tenant got=%q want=%q", got, want)
	}

	// Empty fields fall back to safe defaults.
	got = Config{}.Key("", "", "abc", ".bin")
	want = "_default/blob/ab/abc.bin"
	if got != want {
		t.Errorf("defaults got=%q want=%q", got, want)
	}
}
