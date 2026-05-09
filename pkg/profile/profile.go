// Package profile manages named profiles for multi-user/multi-tenant isolation.
// Each profile is a self-contained .saker/profiles/<name>/ directory with its
// own settings, memory, history, skills, and sessions.db.
package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// nameRE validates profile names: lowercase alphanumeric, hyphens, underscores.
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Well-known subdirectories created for each profile.
var profileDirs = []string{"memory", "history", "skills"}

// Sentinel errors.
var (
	ErrInvalidName   = errors.New("profile: invalid name")
	ErrAlreadyExists = errors.New("profile: already exists")
	ErrNotFound      = errors.New("profile: not found")
	ErrIsDefault     = errors.New("profile: cannot delete default profile")
)

// Info describes a profile entry.
type Info struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDefault bool   `json:"isDefault"`
	Model     string `json:"model,omitempty"` // from settings.json if present
}

// CreateOptions controls profile creation behavior.
type CreateOptions struct {
	CloneFrom string // source profile name to copy settings from (empty = fresh)
}

// Validate checks whether name is a legal profile name.
func Validate(name string) error {
	if name == "" || name == "default" {
		return nil // "default" is the implicit profile
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf("%w: %q (must match %s)", ErrInvalidName, name, nameRE.String())
	}
	return nil
}

// Dir returns the absolute path of a named profile directory.
// For "default" or "", it returns the base .saker directory itself.
func Dir(projectRoot, name string) string {
	base := filepath.Join(projectRoot, ".saker")
	if name == "" || name == "default" {
		return base
	}
	return filepath.Join(base, "profiles", name)
}

// Exists checks whether a named profile directory exists on disk.
func Exists(projectRoot, name string) bool {
	if name == "" || name == "default" {
		return true // default always "exists"
	}
	info, err := os.Stat(Dir(projectRoot, name))
	return err == nil && info.IsDir()
}

// List returns all profiles (default + named) found under projectRoot.
func List(projectRoot string) ([]Info, error) {
	var result []Info

	// Always include the default profile.
	defaultModel := readModelFromSettings(Dir(projectRoot, ""))
	result = append(result, Info{
		Name:      "default",
		Path:      Dir(projectRoot, ""),
		IsDefault: true,
		Model:     defaultModel,
	})

	// Scan profiles/ directory.
	profilesDir := filepath.Join(projectRoot, ".saker", "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return result, nil
		}
		return result, fmt.Errorf("profile: list: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if Validate(name) != nil {
			continue
		}
		model := readModelFromSettings(Dir(projectRoot, name))
		result = append(result, Info{
			Name:  name,
			Path:  Dir(projectRoot, name),
			Model: model,
		})
	}
	return result, nil
}

// Create creates a new named profile directory with standard subdirectories.
func Create(projectRoot, name string, opts CreateOptions) error {
	if err := Validate(name); err != nil {
		return err
	}
	if name == "" || name == "default" {
		return fmt.Errorf("%w: cannot create profile named %q", ErrInvalidName, name)
	}

	dir := Dir(projectRoot, name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, name)
	}

	// Create profile directory and subdirectories.
	for _, sub := range profileDirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return fmt.Errorf("profile: create %s: %w", name, err)
		}
	}

	// Clone settings from another profile if requested.
	if opts.CloneFrom != "" {
		srcDir := Dir(projectRoot, opts.CloneFrom)
		srcSettings := filepath.Join(srcDir, "settings.json")
		if data, err := os.ReadFile(srcSettings); err == nil {
			_ = os.WriteFile(filepath.Join(dir, "settings.json"), data, 0o644)
		}
	}

	return nil
}

// Delete removes a named profile directory entirely.
func Delete(projectRoot, name string) error {
	if name == "" || name == "default" {
		return ErrIsDefault
	}
	if err := Validate(name); err != nil {
		return err
	}
	dir := Dir(projectRoot, name)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}

	// Clear sticky active if it points to this profile.
	if active := GetActive(projectRoot); active == name {
		_ = SetActive(projectRoot, "")
	}

	return os.RemoveAll(dir)
}

// Resolve returns the ConfigRoot path for a profile name.
// For "default" or "", returns "" (meaning use the default .saker directory).
func Resolve(projectRoot, name string) string {
	if name == "" || name == "default" {
		return ""
	}
	return Dir(projectRoot, name)
}

// EnsureExists creates the profile directory if it doesn't already exist.
// This is idempotent — safe to call on every request.
func EnsureExists(projectRoot, name string) error {
	if name == "" || name == "default" {
		return nil
	}
	if err := Validate(name); err != nil {
		return err
	}
	if Exists(projectRoot, name) {
		return nil
	}
	return Create(projectRoot, name, CreateOptions{})
}

// GetActive reads the sticky active profile name from .saker/active_profile.
// Returns "" if no sticky profile is set.
func GetActive(projectRoot string) string {
	path := filepath.Join(projectRoot, ".saker", "active_profile")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SetActive writes the sticky active profile name.
// Pass "" to clear the sticky profile (revert to default).
func SetActive(projectRoot, name string) error {
	if name != "" && name != "default" {
		if err := Validate(name); err != nil {
			return err
		}
		if !Exists(projectRoot, name) {
			return fmt.Errorf("%w: %s", ErrNotFound, name)
		}
	}
	path := filepath.Join(projectRoot, ".saker", "active_profile")
	if name == "" || name == "default" {
		_ = os.Remove(path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(name+"\n"), 0o644)
}

// readModelFromSettings extracts the model field from a profile's settings.json.
func readModelFromSettings(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return ""
	}
	var s struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(data, &s) != nil {
		return ""
	}
	return s.Model
}
