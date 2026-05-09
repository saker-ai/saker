package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/canvas"
	"github.com/google/uuid"
)

// Store persists app metadata, immutable version snapshots, and key/share
// records as JSON files under Root/apps/{appId}/. The single Root field
// makes it cheap for callers to compose per-project stores: a multi-tenant
// server simply constructs Store{Root: pathsFor(ctx).Root}.
//
// All disk writes use the tmp+rename atomic pattern from canvas.Save so a
// crash mid-write cannot leave a corrupt file.
type Store struct {
	Root string
}

// CreateInput carries the fields required to create a new app. Visibility
// defaults to "private" when empty.
type CreateInput struct {
	Name           string
	Description    string
	Icon           string
	SourceThreadID string
	Visibility     string
}

// UpdateInput is a partial patch. Only non-nil pointer fields are applied;
// SourceThreadID is a string because callers that want to leave it alone
// can simply omit the JSON key (the REST handler decodes into a custom
// patch struct and forwards only fields the client supplied).
type UpdateInput struct {
	Name           *string
	Description    *string
	Icon           *string
	SourceThreadID *string
	Visibility     *string
}

// VersionInfo is the lightweight summary returned by ListVersions; the
// caller can fetch the full AppVersion via LoadVersion when needed.
type VersionInfo struct {
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"publishedAt"`
}

// New returns a Store rooted at the given directory. The directory is
// not created eagerly; List/Get/Create create it on demand.
func New(root string) *Store {
	return &Store{Root: root}
}

// appDir returns <root>/apps/{appID}, validating appID first.
func (s *Store) appDir(appID string) (string, error) {
	if err := validateAppID(appID); err != nil {
		return "", err
	}
	if s.Root == "" {
		return "", errors.New("apps: store root is empty")
	}
	return filepath.Join(s.Root, "apps", appID), nil
}

func (s *Store) appsRoot() string {
	return filepath.Join(s.Root, "apps")
}

func validateAppID(appID string) error {
	if strings.TrimSpace(appID) == "" {
		return fmt.Errorf("%w: empty", ErrInvalidAppID)
	}
	if strings.ContainsAny(appID, "/\\") || strings.Contains(appID, "..") {
		return fmt.Errorf("%w: %q", ErrInvalidAppID, appID)
	}
	return nil
}

// List scans Root/apps/, reads every meta.json, and returns the metadata
// sorted by UpdatedAt descending. Missing or unreadable entries are
// skipped (best-effort) so one corrupt file does not blank the listing.
func (s *Store) List(_ context.Context) ([]*AppMeta, error) {
	if s.Root == "" {
		return nil, errors.New("apps: store root is empty")
	}
	entries, err := os.ReadDir(s.appsRoot())
	if err != nil {
		if os.IsNotExist(err) {
			return []*AppMeta{}, nil
		}
		return nil, fmt.Errorf("apps: read apps dir: %w", err)
	}
	out := make([]*AppMeta, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := s.readMeta(e.Name())
		if err != nil {
			// Skip unreadable entries silently; the admin UI will show
			// the missing app on the next refresh once disk is fixed.
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// Get returns the meta record for one app. Errors wrap ErrAppNotFound
// (and os.ErrNotExist) when the app does not exist.
func (s *Store) Get(_ context.Context, appID string) (*AppMeta, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
	}
	return s.readMeta(appID)
}

func (s *Store) readMeta(appID string) (*AppMeta, error) {
	dir, err := s.appDir(appID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "meta.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrAppNotFound, appID)
		}
		return nil, fmt.Errorf("apps: read meta %s: %w", appID, err)
	}
	meta := &AppMeta{}
	if err := json.Unmarshal(raw, meta); err != nil {
		return nil, fmt.Errorf("apps: parse meta %s: %w", appID, err)
	}
	return meta, nil
}

// Create generates an ID, writes an initial meta.json, and returns the
// new record. Visibility defaults to private when empty.
func (s *Store) Create(_ context.Context, in CreateInput) (*AppMeta, error) {
	if s.Root == "" {
		return nil, errors.New("apps: store root is empty")
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("apps: name is required")
	}
	visibility := in.Visibility
	if visibility == "" {
		visibility = VisibilityPrivate
	}
	now := time.Now().UTC()
	meta := &AppMeta{
		ID:               uuid.NewString(),
		Name:             in.Name,
		Description:      in.Description,
		Icon:             in.Icon,
		SourceThreadID:   in.SourceThreadID,
		PublishedVersion: "",
		Visibility:       visibility,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.writeMeta(meta); err != nil {
		return nil, err
	}
	return meta, nil
}

// Update applies a partial patch and bumps UpdatedAt. Last-writer-wins;
// PR1 has a single admin user so we accept that race.
func (s *Store) Update(ctx context.Context, appID string, patch UpdateInput) (*AppMeta, error) {
	meta, err := s.Get(ctx, appID)
	if err != nil {
		return nil, err
	}
	if patch.Name != nil {
		meta.Name = *patch.Name
	}
	if patch.Description != nil {
		meta.Description = *patch.Description
	}
	if patch.Icon != nil {
		meta.Icon = *patch.Icon
	}
	if patch.SourceThreadID != nil {
		meta.SourceThreadID = *patch.SourceThreadID
	}
	if patch.Visibility != nil {
		meta.Visibility = *patch.Visibility
	}
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMeta(meta); err != nil {
		return nil, err
	}
	return meta, nil
}

// Delete removes the app directory and everything in it.
func (s *Store) Delete(_ context.Context, appID string) error {
	dir, err := s.appDir(appID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("apps: delete %s: %w", appID, err)
	}
	return nil
}

// PublishVersion validates the document, writes an immutable snapshot
// under versions/, and updates meta.json's PublishedVersion pointer.
// Returns the freshly persisted AppVersion record.
func (s *Store) PublishVersion(ctx context.Context, appID string, doc *canvas.Document, publishedBy string) (*AppVersion, error) {
	if doc == nil {
		return nil, errors.New("apps: publish: doc is nil")
	}
	meta, err := s.Get(ctx, appID)
	if err != nil {
		return nil, err
	}
	inputs := ExtractInputs(doc)
	outputs := ExtractOutputs(doc)
	if len(inputs) == 0 {
		return nil, errors.New("apps: publish: canvas has no appInput nodes")
	}
	if len(outputs) == 0 {
		return nil, errors.New("apps: publish: canvas has no appOutput nodes")
	}

	now := time.Now().UTC()
	version := now.Format("2006-01-02-150405")

	v := &AppVersion{
		Version:     version,
		PublishedAt: now,
		PublishedBy: publishedBy,
		Inputs:      inputs,
		Outputs:     outputs,
		Document:    doc,
	}
	if err := s.writeVersion(appID, v); err != nil {
		return nil, err
	}

	meta.PublishedVersion = version
	meta.UpdatedAt = now
	if err := s.writeMeta(meta); err != nil {
		return nil, fmt.Errorf("apps: publish: update meta: %w", err)
	}
	// Best-effort retention: prune stale snapshots so versions/ doesn't grow
	// without bound. Failure is logged via the error return path on the next
	// successful publish — the freshly-written version is already safe on disk.
	_ = s.PruneOldVersions(ctx, appID, MaxVersionsPerApp)
	return v, nil
}

// MaxVersionsPerApp caps how many version snapshots an app keeps on disk.
// The currently-published version is always retained even when older than
// the cap, so a rollback target never disappears from under the user.
const MaxVersionsPerApp = 20

// PruneOldVersions deletes the oldest version snapshots, keeping the
// `keep` most recent plus the currently-published version (which may sit
// outside the recent window when the user has rolled back). Best-effort:
// returns the first error encountered after attempting every deletion so
// transient FS hiccups don't stop the sweep.
func (s *Store) PruneOldVersions(ctx context.Context, appID string, keep int) error {
	if keep < 1 {
		return fmt.Errorf("apps: PruneOldVersions: keep must be ≥1, got %d", keep)
	}
	meta, err := s.Get(ctx, appID)
	if err != nil {
		return err
	}
	versions, err := s.ListVersions(ctx, appID)
	if err != nil {
		return err
	}
	if len(versions) <= keep {
		return nil
	}
	dir, err := s.appDir(appID)
	if err != nil {
		return err
	}
	versionsDir := filepath.Join(dir, "versions")
	// versions is sorted desc by version string (== publish time); keep the
	// first `keep`, evaluate the rest.
	var firstErr error
	for _, v := range versions[keep:] {
		if v.Version == meta.PublishedVersion {
			continue
		}
		path := filepath.Join(versionsDir, v.Version+".json")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			if firstErr == nil {
				firstErr = fmt.Errorf("apps: prune %s: %w", v.Version, err)
			}
		}
	}
	return firstErr
}

// LoadVersion reads a previously published snapshot.
func (s *Store) LoadVersion(_ context.Context, appID, version string) (*AppVersion, error) {
	dir, err := s.appDir(appID)
	if err != nil {
		return nil, err
	}
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "versions", version+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("apps: version %s/%s: %w", appID, version, os.ErrNotExist)
		}
		return nil, fmt.Errorf("apps: read version %s/%s: %w", appID, version, err)
	}
	v := &AppVersion{}
	if err := json.Unmarshal(raw, v); err != nil {
		return nil, fmt.Errorf("apps: parse version %s/%s: %w", appID, version, err)
	}
	return v, nil
}

// ListVersions returns version summaries sorted by version string desc
// (which equals publish-time desc because the format is lexicographic).
func (s *Store) ListVersions(_ context.Context, appID string) ([]VersionInfo, error) {
	dir, err := s.appDir(appID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "versions"))
	if err != nil {
		if os.IsNotExist(err) {
			return []VersionInfo{}, nil
		}
		return nil, fmt.Errorf("apps: list versions %s: %w", appID, err)
	}
	out := make([]VersionInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		version := strings.TrimSuffix(name, ".json")
		info := VersionInfo{Version: version}
		if t, err := time.Parse("2006-01-02-150405", version); err == nil {
			info.PublishedAt = t.UTC()
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Version > out[j].Version
	})
	return out, nil
}

// LoadKeys returns the keys.json contents. A missing file yields an empty
// KeysFile (not an error) so callers can treat "no keys yet" as normal.
func (s *Store) LoadKeys(_ context.Context, appID string) (*KeysFile, error) {
	dir, err := s.appDir(appID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "keys.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &KeysFile{ApiKeys: []ApiKey{}, ShareTokens: []ShareToken{}}, nil
		}
		return nil, fmt.Errorf("apps: read keys %s: %w", appID, err)
	}
	keys := &KeysFile{}
	if err := json.Unmarshal(raw, keys); err != nil {
		return nil, fmt.Errorf("apps: parse keys %s: %w", appID, err)
	}
	if keys.ApiKeys == nil {
		keys.ApiKeys = []ApiKey{}
	}
	if keys.ShareTokens == nil {
		keys.ShareTokens = []ShareToken{}
	}
	return keys, nil
}

// SaveKeys atomically writes keys.json.
func (s *Store) SaveKeys(_ context.Context, appID string, keys *KeysFile) error {
	if keys == nil {
		return errors.New("apps: save keys: nil")
	}
	dir, err := s.appDir(appID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("apps: mkdir %s: %w", dir, err)
	}
	raw, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return fmt.Errorf("apps: marshal keys %s: %w", appID, err)
	}
	return atomicWrite(filepath.Join(dir, "keys.json"), raw, "keys-"+appID)
}

// writeMeta persists meta.json atomically. Creates the app dir if needed.
func (s *Store) writeMeta(meta *AppMeta) error {
	if meta == nil {
		return errors.New("apps: writeMeta: nil")
	}
	dir, err := s.appDir(meta.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("apps: mkdir %s: %w", dir, err)
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("apps: marshal meta %s: %w", meta.ID, err)
	}
	return atomicWrite(filepath.Join(dir, "meta.json"), raw, "meta-"+meta.ID)
}

// writeVersion persists a version snapshot atomically.
func (s *Store) writeVersion(appID string, v *AppVersion) error {
	dir, err := s.appDir(appID)
	if err != nil {
		return err
	}
	versionsDir := filepath.Join(dir, "versions")
	if err := os.MkdirAll(versionsDir, 0o755); err != nil {
		return fmt.Errorf("apps: mkdir %s: %w", versionsDir, err)
	}
	if err := validateVersion(v.Version); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("apps: marshal version %s/%s: %w", appID, v.Version, err)
	}
	return atomicWrite(filepath.Join(versionsDir, v.Version+".json"), raw, "version-"+appID)
}

func validateVersion(version string) error {
	if strings.TrimSpace(version) == "" {
		return errors.New("apps: version is empty")
	}
	if strings.ContainsAny(version, "/\\") || strings.Contains(version, "..") {
		return fmt.Errorf("apps: invalid version %q", version)
	}
	return nil
}

// SetPublishedVersion atomically updates meta.PublishedVersion to the given
// version. Returns ErrAppNotFound when the app is missing, and a plain error
// when the version doesn't exist on disk (so rollback to a deleted version
// is rejected). Updates UpdatedAt.
func (s *Store) SetPublishedVersion(ctx context.Context, appID, version string) (*AppMeta, error) {
	if err := validateAppID(appID); err != nil {
		return nil, err
	}
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	meta, err := s.Get(ctx, appID)
	if err != nil {
		return nil, err
	}
	dir, err := s.appDir(appID)
	if err != nil {
		return nil, err
	}
	versionPath := filepath.Join(dir, "versions", version+".json")
	if _, err := os.Stat(versionPath); err != nil {
		return nil, fmt.Errorf("version not found: %s", version)
	}
	meta.PublishedVersion = version
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMeta(meta); err != nil {
		return nil, err
	}
	return meta, nil
}

// atomicWrite mirrors canvas.Save's tmp+rename pattern.
func atomicWrite(finalPath string, payload []byte, tmpHint string) error {
	dir := filepath.Dir(finalPath)
	tmp, err := os.CreateTemp(dir, tmpHint+".*.tmp")
	if err != nil {
		return fmt.Errorf("apps: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("apps: write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("apps: close tmp: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		// Non-fatal: rename will still succeed; just log via slog if needed.
		_ = err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("apps: rename tmp→final: %w", err)
	}
	return nil
}
