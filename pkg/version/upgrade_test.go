package version

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseVersionParts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		v    string
		want [3]int
	}{
		{name: "standard semver", v: "1.2.3", want: [3]int{1, 2, 3}},
		{name: "zero version", v: "0.0.0", want: [3]int{0, 0, 0}},
		{name: "large numbers", v: "99.99.99", want: [3]int{99, 99, 99}},
		{name: "pre-release suffix stripped", v: "1.2.3-beta", want: [3]int{1, 2, 3}},
		{name: "pre-release with dash", v: "0.1.0-rc1", want: [3]int{0, 1, 0}},
		{name: "missing patch defaults to 0", v: "1.2", want: [3]int{1, 2, 0}},
		{name: "single segment defaults rest to 0", v: "5", want: [3]int{5, 0, 0}},
		{name: "empty string defaults all to 0", v: "", want: [3]int{0, 0, 0}},
		{name: "version with non-numeric chars", v: "v1.2.3", want: [3]int{1, 2, 3}},
		{name: "version with mixed chars", v: "1a.2b.3c", want: [3]int{1, 2, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseVersionParts(tt.v)
			if got != tt.want {
				t.Errorf("parseVersionParts(%q) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}

func TestFormatUpdateNoticeWithInstallURL(t *testing.T) {
	t.Parallel()
	info := &UpdateInfo{
		HasUpdate:  true,
		Current:    "0.1.0",
		Latest:     "0.2.0",
		InstallURL: "https://example.com/install.sh",
		Message:    "Important security fix",
	}
	notice := FormatUpdateNotice(info)
	if !contains(notice, "Update available") {
		t.Errorf("notice missing 'Update available': %q", notice)
	}
	if !contains(notice, "v0.1.0 -> v0.2.0") {
		t.Errorf("notice missing version transition: %q", notice)
	}
	if !contains(notice, "https://example.com/install.sh") {
		t.Errorf("notice missing install URL: %q", notice)
	}
	if !contains(notice, "Important security fix") {
		t.Errorf("notice missing message: %q", notice)
	}
}

func TestFormatUpdateNoticeNoInstallURL(t *testing.T) {
	t.Parallel()
	info := &UpdateInfo{
		HasUpdate: true,
		Current:   "0.1.0",
		Latest:    "0.2.0",
	}
	notice := FormatUpdateNotice(info)
	if !contains(notice, "Update available") {
		t.Errorf("notice missing 'Update available': %q", notice)
	}
	// Should not contain "Run:" line since InstallURL is empty.
	if contains(notice, "Run:") {
		t.Errorf("notice should not contain 'Run:' without InstallURL: %q", notice)
	}
}

func TestBuildUpdateInfo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		current       string
		latest        string
		installURL    string
		releaseURL    string
		message       string
		wantHasUpdate bool
	}{
		{
			name:          "newer version available",
			current:       "0.1.0",
			latest:        "0.2.0",
			wantHasUpdate: true,
		},
		{
			name:          "same version",
			current:       "0.1.0",
			latest:        "0.1.0",
			wantHasUpdate: false,
		},
		{
			name:          "older remote version",
			current:       "0.2.0",
			latest:        "0.1.0",
			wantHasUpdate: false,
		},
		{
			name:          "empty latest means no update",
			current:       "0.1.0",
			latest:        "",
			wantHasUpdate: false,
		},
		{
			name:          "with install and release URLs",
			current:       "0.1.0",
			latest:        "0.2.0",
			installURL:    "https://install.sh",
			releaseURL:    "https://github.com/releases",
			message:       "bug fix",
			wantHasUpdate: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			info := buildUpdateInfo(tt.current, tt.latest, tt.installURL, tt.releaseURL, tt.message)
			if info.HasUpdate != tt.wantHasUpdate {
				t.Errorf("HasUpdate = %v, want %v", info.HasUpdate, tt.wantHasUpdate)
			}
			if info.Current != normalizeVersion(tt.current) {
				t.Errorf("Current = %q, want %q", info.Current, normalizeVersion(tt.current))
			}
			if info.InstallURL != tt.installURL {
				t.Errorf("InstallURL = %q, want %q", info.InstallURL, tt.installURL)
			}
			if info.Message != tt.message {
				t.Errorf("Message = %q, want %q", info.Message, tt.message)
			}
		})
	}
}

func TestCachedCheckRoundTrip(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, CacheFileName)

	c := CachedCheck{
		CheckedAt:  time.Now(),
		Latest:     "0.3.0",
		Current:    "0.2.0",
		InstallURL: "https://install.sh",
		ReleaseURL: "https://github.com/releases",
		Message:    "new features",
	}

	if err := writeCache(cachePath, c); err != nil {
		t.Fatalf("writeCache: %v", err)
	}

	got, err := readCache(cachePath)
	if err != nil {
		t.Fatalf("readCache: %v", err)
	}
	if got.Latest != "0.3.0" {
		t.Errorf("Latest = %q, want 0.3.0", got.Latest)
	}
	if got.Current != "0.2.0" {
		t.Errorf("Current = %q, want 0.2.0", got.Current)
	}
	if got.InstallURL != "https://install.sh" {
		t.Errorf("InstallURL = %q, want https://install.sh", got.InstallURL)
	}
}

func TestReadCacheEmptyPath(t *testing.T) {
	t.Parallel()
	_, err := readCache("")
	if err == nil {
		t.Error("expected error for empty cache path")
	}
}

func TestWriteCacheEmptyPath(t *testing.T) {
	t.Parallel()
	err := writeCache("", CachedCheck{})
	if err == nil {
		t.Error("expected error for empty cache path")
	}
}

func TestReadCacheNonexistent(t *testing.T) {
	t.Parallel()
	_, err := readCache("/nonexistent/path/cache.json")
	if err == nil {
		t.Error("expected error for nonexistent cache file")
	}
}

func TestReadCacheCorrupted(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "corrupted.json")
	os.WriteFile(path, []byte("this is not json"), 0o644)

	_, err := readCache(path)
	if err == nil {
		t.Error("expected error for corrupted cache file")
	}
}

func TestDetectPlatform(t *testing.T) {
	t.Parallel()
	osName, archName := detectPlatform()
	if osName != "linux" && osName != "darwin" {
		t.Errorf("osName = %q, want linux or darwin", osName)
	}
	if archName != "amd64" && archName != "arm64" {
		t.Errorf("archName = %q, want amd64 or arm64", archName)
	}
}

func TestSelfUpgradeEmptyVersion(t *testing.T) {
	t.Parallel()
	err := SelfUpgrade("", nil)
	if err == nil {
		t.Error("expected error for empty version")
	}
}

func TestRemoteVersionJSON(t *testing.T) {
	t.Parallel()
	rv := RemoteVersion{
		Latest:       "0.3.0",
		MinSupported: "0.1.0",
		InstallURL:   "https://install.sh",
		ReleaseURL:   "https://github.com/releases",
		Message:      "security fix",
	}
	data, err := json.Marshal(rv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got RemoteVersion
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Latest != "0.3.0" {
		t.Errorf("Latest = %q, want 0.3.0", got.Latest)
	}
	if got.MinSupported != "0.1.0" {
		t.Errorf("MinSupported = %q, want 0.1.0", got.MinSupported)
	}
}

func TestUpdateInfoStruct(t *testing.T) {
	t.Parallel()
	info := UpdateInfo{
		HasUpdate:  true,
		Current:    "0.1.0",
		Latest:     "0.2.0",
		InstallURL: "https://example.com",
		ReleaseURL: "https://github.com",
		Message:    "test",
	}
	if !info.HasUpdate {
		t.Error("HasUpdate should be true")
	}
	if info.Current != "0.1.0" {
		t.Errorf("Current = %q, want 0.1.0", info.Current)
	}
}

func TestExtractBinaryFromArchive(t *testing.T) {
	t.Parallel()
	// Create a tar.gz archive with a fake binary.
	tmpDir := t.TempDir()

	// Create a fake binary content.
	binaryContent := []byte("fake binary data for testing")
	binaryPath := filepath.Join(tmpDir, "saker")
	if err := os.WriteFile(binaryPath, binaryContent, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create tar.gz archive.
	archivePath := filepath.Join(tmpDir, "saker-test.tar.gz")
	if err := createTestArchive(t, archivePath, "saker", binaryContent); err != nil {
		t.Fatalf("create archive: %v", err)
	}

	// Extract the binary.
	extractedPath, err := extractBinary(archivePath, tmpDir, "saker")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if extractedPath != filepath.Join(tmpDir, "saker") {
		t.Errorf("extractedPath = %q, want %q", extractedPath, filepath.Join(tmpDir, "saker"))
	}

	// Verify extracted content matches.
	data, err := os.ReadFile(extractedPath)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(data) != string(binaryContent) {
		t.Errorf("extracted content mismatch")
	}
}

func TestExtractBinaryNotFoundInArchive(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "empty.tar.gz")
	if err := createTestArchive(t, archivePath, "other-binary", []byte("data")); err != nil {
		t.Fatalf("create archive: %v", err)
	}

	_, err := extractBinary(archivePath, tmpDir, "saker")
	if err == nil {
		t.Error("expected error when binary not found in archive")
	}
	if !strings.Contains(err.Error(), "not found in archive") {
		t.Errorf("error = %q, want 'not found in archive'", err.Error())
	}
}

func TestCopyFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "source.txt")
	dstPath := filepath.Join(tmpDir, "dest.txt")

	content := []byte("test content for copy")
	if err := os.WriteFile(srcPath, content, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(srcPath, dstPath); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	data, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("copied content mismatch")
	}

	// Verify permissions were preserved.
	srcInfo, _ := os.Stat(srcPath)
	dstInfo, _ := os.Stat(dstPath)
	if srcInfo.Mode() != dstInfo.Mode() {
		t.Errorf("mode mismatch: src=%v dst=%v", srcInfo.Mode(), dstInfo.Mode())
	}
}

func TestCopyFileNonexistentSource(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	err := copyFile("/nonexistent/file", filepath.Join(tmpDir, "dst.txt"))
	if err == nil {
		t.Error("expected error for nonexistent source")
	}
}

// Helper function to check if a string contains a substring.
func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

// createTestArchive creates a tar.gz archive containing a single file with
// the given name and content.
func createTestArchive(t *testing.T, archivePath, fileName string, content []byte) error {
	t.Helper()
	f, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzw := gzip.NewWriter(f)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	hdr := &tar.Header{
		Name:     fileName,
		Size:     int64(len(content)),
		Mode:     0o755,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write(content); err != nil {
		return err
	}
	return nil
}
