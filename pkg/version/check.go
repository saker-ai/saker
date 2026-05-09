// Package version provides update checking for the saker CLI.
package version

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultCheckURL is the endpoint serving the latest version info.
	DefaultCheckURL = "https://agent.saker.run/api/v1/version.json"

	// CacheFileName is the local cache file name under ~/.saker/.
	CacheFileName = "update-check.json"

	// CacheTTL is how long a cached check result is considered fresh.
	CacheTTL = 24 * time.Hour

	// RequestTimeout is the HTTP request timeout.
	RequestTimeout = 1 * time.Second
)

// RemoteVersion is the JSON structure served by the version endpoint.
type RemoteVersion struct {
	Latest       string `json:"latest"`
	MinSupported string `json:"min_supported"`
	InstallURL   string `json:"install_url"`
	ReleaseURL   string `json:"release_url"`
	Message      string `json:"message"`
}

// UpdateInfo is the result of an update check.
type UpdateInfo struct {
	HasUpdate  bool
	Current    string
	Latest     string
	InstallURL string
	ReleaseURL string
	Message    string
}

// CachedCheck is the on-disk cache format.
type CachedCheck struct {
	CheckedAt  time.Time `json:"checked_at"`
	Latest     string    `json:"latest"`
	Current    string    `json:"current"`
	InstallURL string    `json:"install_url"`
	ReleaseURL string    `json:"release_url"`
	Message    string    `json:"message"`
}

// CheckForUpdate checks the remote endpoint for a newer version.
// It uses a local cache to avoid hitting the network on every invocation.
func CheckForUpdate(currentVersion string) (*UpdateInfo, error) {
	return CheckForUpdateWithURL(currentVersion, DefaultCheckURL)
}

// CheckForUpdateWithURL is like CheckForUpdate but accepts a custom URL.
func CheckForUpdateWithURL(currentVersion, checkURL string) (*UpdateInfo, error) {
	currentVersion = normalizeVersion(currentVersion)

	// Skip check for dev builds.
	if currentVersion == "" || currentVersion == "dev" {
		return nil, nil
	}

	// Try cache first.
	cachePath := cacheFilePathFunc()
	if cached, err := readCache(cachePath); err == nil {
		if time.Since(cached.CheckedAt) < CacheTTL && cached.Current == currentVersion {
			return buildUpdateInfo(currentVersion, cached.Latest, cached.InstallURL, cached.ReleaseURL, cached.Message), nil
		}
	}

	// Fetch remote.
	remote, err := fetchRemoteVersion(checkURL)
	if err != nil {
		return nil, err
	}

	// Write cache (best effort).
	_ = writeCache(cachePath, CachedCheck{
		CheckedAt:  time.Now(),
		Latest:     remote.Latest,
		Current:    currentVersion,
		InstallURL: remote.InstallURL,
		ReleaseURL: remote.ReleaseURL,
		Message:    remote.Message,
	})

	return buildUpdateInfo(currentVersion, remote.Latest, remote.InstallURL, remote.ReleaseURL, remote.Message), nil
}

// CheckForUpdateAsync starts a background update check and returns a channel
// that will receive at most one *UpdateInfo (or nil) and then close.
func CheckForUpdateAsync(currentVersion string) <-chan *UpdateInfo {
	ch := make(chan *UpdateInfo, 1)
	go func() {
		defer close(ch)
		info, err := CheckForUpdate(currentVersion)
		if err != nil || info == nil {
			return
		}
		ch <- info
	}()
	return ch
}

// FormatUpdateNotice returns a human-readable update notification string.
// Returns empty string if there is no update.
func FormatUpdateNotice(info *UpdateInfo) string {
	if info == nil || !info.HasUpdate {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Update available: v%s -> v%s", info.Current, info.Latest)
	if info.InstallURL != "" {
		fmt.Fprintf(&b, "\n  Run: curl -fsSL %s | bash", info.InstallURL)
	}
	if info.Message != "" {
		fmt.Fprintf(&b, "\n  %s", info.Message)
	}
	return b.String()
}

func fetchRemoteVersion(url string) (*RemoteVersion, error) {
	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("version check: HTTP %d", resp.StatusCode)
	}

	var remote RemoteVersion
	if err := json.NewDecoder(resp.Body).Decode(&remote); err != nil {
		return nil, fmt.Errorf("version check: decode: %w", err)
	}
	return &remote, nil
}

func buildUpdateInfo(current, latest, installURL, releaseURL, message string) *UpdateInfo {
	latest = normalizeVersion(latest)
	return &UpdateInfo{
		HasUpdate:  latest != "" && compareVersions(latest, current) > 0,
		Current:    current,
		Latest:     latest,
		InstallURL: installURL,
		ReleaseURL: releaseURL,
		Message:    message,
	}
}

// cacheFilePathFunc is the function used to resolve the cache file path.
// Tests can override this to use a temporary directory.
var cacheFilePathFunc = cacheFilePath

func cacheFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".saker", CacheFileName)
}

func readCache(path string) (*CachedCheck, error) {
	if path == "" {
		return nil, fmt.Errorf("no cache path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c CachedCheck
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func writeCache(path string, c CachedCheck) error {
	if path == "" {
		return fmt.Errorf("no cache path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// normalizeVersion strips a leading "v" prefix.
func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// compareVersions compares two semver-like version strings (major.minor.patch).
// Returns >0 if a > b, <0 if a < b, 0 if equal.
func compareVersions(a, b string) int {
	pa := parseVersionParts(a)
	pb := parseVersionParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] - pb[i]
		}
	}
	return 0
}

func parseVersionParts(v string) [3]int {
	var parts [3]int
	segs := strings.SplitN(v, ".", 3)
	for i, s := range segs {
		if i >= 3 {
			break
		}
		// Strip pre-release suffix (e.g. "1-beta").
		if idx := strings.IndexByte(s, '-'); idx >= 0 {
			s = s[:idx]
		}
		n := 0
		for _, c := range s {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		parts[i] = n
	}
	return parts
}
