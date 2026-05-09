package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SaveSettingsLocal writes a Settings struct to .saker/settings.local.json.
// Only non-nil/non-zero fields are persisted (via json omitempty tags).
// The file is created with 0600 permissions (owner read/write only) to protect
// sensitive data such as API keys.
func SaveSettingsLocal(projectRoot string, s *Settings) error {
	dir := filepath.Join(projectRoot, ".saker")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	path := filepath.Join(dir, "settings.local.json")
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write settings.local.json: %w", err)
	}
	return nil
}

// LoadSettingsLocal reads the .saker/settings.local.json file.
// Returns (nil, nil) if the file does not exist.
func LoadSettingsLocal(projectRoot string) (*Settings, error) {
	path := filepath.Join(projectRoot, ".saker", "settings.local.json")
	return loadJSONFile(path, nil)
}
