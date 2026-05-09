package cache

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/tool"
)

const defaultMaxEntries = 1000

// FileStore persists cache entries as JSON on disk with bounded capacity.
// When the number of entries exceeds maxEntries, oldest entries are evicted.
type FileStore struct {
	mu         sync.RWMutex
	path       string
	entries    map[artifact.CacheKey]*tool.ToolResult
	order      []artifact.CacheKey // insertion order for eviction
	maxEntries int
}

func NewFileStore(path string) (*FileStore, error) {
	store := &FileStore{
		path:       filepath.Clean(path),
		entries:    map[artifact.CacheKey]*tool.ToolResult{},
		maxEntries: defaultMaxEntries,
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (f *FileStore) Load(_ context.Context, key artifact.CacheKey) (*tool.ToolResult, bool, error) {
	if f == nil {
		return nil, false, nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	result, ok := f.entries[key]
	if !ok {
		return nil, false, nil
	}
	return cloneToolResult(result), true, nil
}

func (f *FileStore) Save(_ context.Context, key artifact.CacheKey, result *tool.ToolResult) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.entries[key]; !exists {
		f.order = append(f.order, key)
	}
	f.entries[key] = cloneToolResult(result)
	f.evictLocked()
	return f.flushLocked()
}

func (f *FileStore) evictLocked() {
	for len(f.entries) > f.maxEntries && len(f.order) > 0 {
		oldest := f.order[0]
		f.order = f.order[1:]
		delete(f.entries, oldest)
	}
}

func (f *FileStore) load() error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &f.entries); err != nil {
		return err
	}
	// rebuild insertion order from loaded entries
	f.order = make([]artifact.CacheKey, 0, len(f.entries))
	for k := range f.entries {
		f.order = append(f.order, k)
	}
	return nil
}

func (f *FileStore) flushLocked() error {
	data, err := json.MarshalIndent(f.entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, f.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
