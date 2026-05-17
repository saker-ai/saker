package skillhub

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config captures skillhub-related settings read from .saker/settings.json
// (key: "skillhub") plus environment overrides.
type Config struct {
	Registry           string    `json:"registry,omitempty"`
	Token              string    `json:"token,omitempty"`
	Handle             string    `json:"handle,omitempty"`
	AutoSync           bool      `json:"autoSync,omitempty"`
	SyncInterval       string    `json:"syncInterval,omitempty"`
	Subscriptions      []string  `json:"subscriptions,omitempty"`
	LearnedAutoPublish bool      `json:"learnedAutoPublish,omitempty"`
	LearnedVisibility  string    `json:"learnedVisibility,omitempty"`
	Offline            bool      `json:"offline,omitempty"`
	RemoteMode         bool      `json:"remoteMode,omitempty"`
	RemoteRegistry     string    `json:"remoteRegistry,omitempty"`
	RemoteSlugs        []string  `json:"remoteSlugs,omitempty"`
	LastSyncAt         time.Time `json:"lastSyncAt,omitempty"`
	LastSyncStatus     string    `json:"lastSyncStatus,omitempty"` // "ok" | "partial" | "error"
}

// Resolved returns the effective configuration with defaults filled in
// and environment overrides applied.
func (c Config) Resolved() Config {
	out := c
	if v := strings.TrimSpace(os.Getenv("SKILLHUB_REGISTRY")); v != "" {
		out.Registry = v
	}
	if v := strings.TrimSpace(os.Getenv("SKILLHUB_TOKEN")); v != "" {
		out.Token = v
	}
	if strings.EqualFold(os.Getenv("SKILLHUB_OFFLINE"), "1") ||
		strings.EqualFold(os.Getenv("SKILLHUB_OFFLINE"), "true") {
		out.Offline = true
	}
	if out.Registry == "" {
		out.Registry = DefaultRegistry
	}
	if out.LearnedVisibility == "" {
		out.LearnedVisibility = "private"
	}
	if v := strings.TrimSpace(os.Getenv("SKILLHUB_REMOTE_REGISTRY")); v != "" {
		out.RemoteRegistry = v
		out.RemoteMode = true
	}
	out.Registry = strings.TrimRight(out.Registry, "/")
	if out.RemoteRegistry == "" && out.Registry != "" {
		out.RemoteRegistry = out.Registry
	}
	if out.RemoteRegistry != "" && !out.Offline {
		out.RemoteMode = true
	}
	return out
}

// SyncIntervalDuration parses SyncInterval or returns a sensible default.
func (c Config) SyncIntervalDuration() time.Duration {
	if d, err := time.ParseDuration(c.SyncInterval); err == nil && d > 0 {
		return d
	}
	return 15 * time.Minute
}

// LoadFromProject reads skillhub section from <projectRoot>/.saker/settings.json,
// merged with settings.local.json overrides if present.
// Returns a zero Config (not error) if no settings file exists.
func LoadFromProject(projectRoot string) (Config, error) {
	merged := map[string]any{}
	files := []string{
		filepath.Join(projectRoot, ".saker", "settings.json"),
		filepath.Join(projectRoot, ".saker", "settings.local.json"),
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Config{}, fmt.Errorf("read %s: %w", path, err)
		}
		var one map[string]any
		if err := json.Unmarshal(data, &one); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", path, err)
		}
		if sh, ok := one["skillhub"].(map[string]any); ok {
			for k, v := range sh {
				merged[k] = v
			}
		}
	}
	if len(merged) == 0 {
		return Config{}, nil
	}
	raw, _ := json.Marshal(merged)
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode skillhub config: %w", err)
	}
	return cfg, nil
}

// SaveToProject writes the skillhub section into <projectRoot>/.saker/settings.local.json.
// Existing keys are preserved; only the "skillhub" key is replaced.
// This keeps tokens out of the checked-in settings.json.
func SaveToProject(projectRoot string, cfg Config) error {
	dir := filepath.Join(projectRoot, ".saker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "settings.local.json")

	existing := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &existing)
	}
	existing["skillhub"] = cfg

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	// Write atomically via temp file to avoid half-written JSON.
	tmp, err := os.CreateTemp(dir, ".settings.local.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	// Tokens are sensitive — narrow perms.
	_ = os.Chmod(tmpName, 0o600)
	return os.Rename(tmpName, path)
}

// SubscribedDir returns the absolute path where subscribed skills are installed.
func SubscribedDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".saker", "subscribed-skills")
}

// LearnedDir returns the absolute path where auto-learned skills live.
func LearnedDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".saker", "learned-skills")
}
