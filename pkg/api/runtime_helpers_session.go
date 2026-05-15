package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/saker-ai/saker/pkg/message"
)

// runtime_helpers_session.go owns the per-session state caches: history
// store, gate (mutex-per-session), and the on-disk per-session output dirs.
// Loader/composition lives in runtime_helpers_loader.go; pure data
// conversion in runtime_helpers_convert.go.

type historyStore struct {
	mu       sync.Mutex
	data     map[string]*message.History
	lastUsed map[string]time.Time
	maxSize  int
	onEvict  func(string)
	loader   func(string) ([]message.Message, error)
}

func newHistoryStore(maxSize int) *historyStore {
	if maxSize <= 0 {
		maxSize = defaultMaxSessions
	}
	return &historyStore{
		data:     map[string]*message.History{},
		lastUsed: map[string]time.Time{},
		maxSize:  maxSize,
	}
}

func (s *historyStore) Get(id string) *message.History {
	if strings.TrimSpace(id) == "" {
		id = defaultSessionID(defaultEntrypoint)
	}
	s.mu.Lock()
	now := time.Now()
	if hist, ok := s.data[id]; ok {
		s.lastUsed[id] = now
		s.mu.Unlock()
		return hist
	}
	hist := message.NewHistory()
	s.data[id] = hist
	s.lastUsed[id] = now
	onEvict := s.onEvict
	loader := s.loader
	evicted := ""
	if len(s.data) > s.maxSize {
		evicted = s.evictOldest()
	}
	s.mu.Unlock()
	if loader != nil {
		if loaded, err := loader(id); err == nil && len(loaded) > 0 {
			hist.Replace(loaded)
		}
	}
	if evicted != "" {
		cleanupToolOutputSessionDir(evicted) //nolint:errcheck
		if onEvict != nil {
			onEvict(evicted)
		}
	}
	return hist
}

func (s *historyStore) evictOldest() string {
	if len(s.data) <= s.maxSize {
		return ""
	}
	var oldestKey string
	var oldestTime time.Time
	first := true
	for id, ts := range s.lastUsed {
		if first || ts.Before(oldestTime) {
			oldestKey = id
			oldestTime = ts
			first = false
		}
	}
	if oldestKey == "" {
		return ""
	}
	delete(s.data, oldestKey)
	delete(s.lastUsed, oldestKey)
	return oldestKey
}

func (s *historyStore) SessionIDs() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.data))
	for id := range s.data {
		ids = append(ids, id)
	}
	return ids
}

func bashOutputSessionDir(sessionID string) string {
	return filepath.Join(bashOutputBaseDir(), sanitizePathComponent(sessionID))
}

func cleanupBashOutputSessionDir(sessionID string) error {
	return os.RemoveAll(bashOutputSessionDir(sessionID))
}

func toolOutputSessionDir(sessionID string) string {
	return filepath.Join(toolOutputBaseDir(), sanitizePathComponent(sessionID))
}

func cleanupToolOutputSessionDir(sessionID string) error {
	return os.RemoveAll(toolOutputSessionDir(sessionID))
}

func sanitizePathComponent(value string) string {
	const fallback = "default"
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	sanitized := strings.Trim(b.String(), "-")
	if sanitized == "" {
		return fallback
	}
	return sanitized
}

type sessionGate struct {
	gates  sync.Map // map[string]*gateEntry
	stopCh chan struct{}
}

type gateEntry struct {
	ch         chan struct{}
	acquiredAt time.Time
}

func newSessionGate() *sessionGate {
	sg := &sessionGate{stopCh: make(chan struct{})}
	go sg.cleanupLoop()
	return sg
}

// Close stops the background gate cleanup goroutine.
func (g *sessionGate) Close() {
	if g.stopCh != nil {
		close(g.stopCh)
	}
}

func (g *sessionGate) Acquire(ctx context.Context, sessionID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		entry := &gateEntry{ch: make(chan struct{}), acquiredAt: time.Now()}
		existing, loaded := g.gates.LoadOrStore(sessionID, entry)
		if !loaded {
			if err := ctx.Err(); err != nil {
				g.Release(sessionID)
				return err
			}
			return nil
		}

		held := existing.(*gateEntry) //nolint:errcheck // sync.Map guarantees type safety for stored values
		select {
		case <-held.ch:
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (g *sessionGate) Release(sessionID string) {
	if g == nil {
		return
	}
	existing, ok := g.gates.LoadAndDelete(sessionID)
	if !ok {
		return
	}
	close(existing.(*gateEntry).ch) //nolint:errcheck // sync.Map guarantees type safety for stored values
}

// cleanupLoop periodically removes abandoned gate entries (held longer than
// 1 hour) whose holders have likely crashed without releasing.
func (g *sessionGate) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-g.stopCh:
			return
		case <-ticker.C:
			now := time.Now()
			cutoff := now.Add(-1 * time.Hour)
			g.gates.Range(func(key, value any) bool {
				entry := value.(*gateEntry)
				// If the channel is already closed (drained), the entry
				// was abandoned after Release failed to clean it up.
				select {
				case <-entry.ch:
					// Channel closed but entry still present — remove it.
					g.gates.Delete(key)
				default:
					// Channel still open. If held too long, force-release.
					if entry.acquiredAt.Before(cutoff) {
						g.gates.Delete(key)
						close(entry.ch)
					}
				}
				return true
			})
		}
	}
}
