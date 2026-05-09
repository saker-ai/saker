package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SessionStore manages thread state and persistence.
type SessionStore struct {
	mu      sync.RWMutex
	threads []Thread
	items   map[string][]ThreadItem // threadID → items
	dataDir string
}

// NewSessionStore creates a store, loading any persisted threads from dataDir.
func NewSessionStore(dataDir string) (*SessionStore, error) {
	s := &SessionStore{
		threads: make([]Thread, 0),
		items:   make(map[string][]ThreadItem),
		dataDir: dataDir,
	}
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, err
		}
		if err := s.load(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// CreateThread creates a new conversation thread.
func (s *SessionStore) CreateThread(title string) Thread {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	t := Thread{
		ID:        uuid.New().String(),
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.threads = append(s.threads, t)
	s.items[t.ID] = make([]ThreadItem, 0)
	s.persist()
	return t
}

// ListThreads returns all threads ordered by creation time.
func (s *SessionStore) ListThreads() []Thread {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Thread, len(s.threads))
	copy(out, s.threads)
	return out
}

// UpdateThreadTitle updates the title of an existing thread.
func (s *SessionStore) UpdateThreadTitle(threadID, title string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.threads {
		if s.threads[i].ID == threadID {
			s.threads[i].Title = title
			s.threads[i].UpdatedAt = time.Now()
			s.persist()
			return true
		}
	}
	return false
}

// DeleteThread removes a thread and its items, including the persisted file.
func (s *SessionStore) DeleteThread(threadID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for i := range s.threads {
		if s.threads[i].ID == threadID {
			s.threads = append(s.threads[:i], s.threads[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return false
	}
	delete(s.items, threadID)
	if s.dataDir != "" {
		_ = os.Remove(filepath.Join(s.dataDir, threadID+".json"))
	}
	return true
}

// GetThread returns a single thread by ID.
func (s *SessionStore) GetThread(threadID string) (Thread, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.threads {
		if t.ID == threadID {
			return t, true
		}
	}
	return Thread{}, false
}

// AppendItem adds a message to a thread and returns the created item.
func (s *SessionStore) AppendItem(threadID, role, content, turnID string) ThreadItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := ThreadItem{
		ID:        uuid.New().String(),
		ThreadID:  threadID,
		TurnID:    turnID,
		Role:      role,
		Content:   content,
		CreatedAt: time.Now(),
	}
	s.items[threadID] = append(s.items[threadID], item)

	// Update thread timestamp.
	for i := range s.threads {
		if s.threads[i].ID == threadID {
			s.threads[i].UpdatedAt = item.CreatedAt
			break
		}
	}
	s.persist()
	return item
}

// AppendItemWithArtifacts adds a message with media artifacts to a thread.
func (s *SessionStore) AppendItemWithArtifacts(threadID, role, content, turnID string, artifacts []Artifact) ThreadItem {
	return s.appendItemFull(threadID, role, "", content, turnID, artifacts)
}

// AppendToolItem adds a tool result item with an explicit tool name.
func (s *SessionStore) AppendToolItem(threadID, toolName, content, turnID string, artifacts []Artifact) ThreadItem {
	return s.appendItemFull(threadID, "tool", toolName, content, turnID, artifacts)
}

func (s *SessionStore) appendItemFull(threadID, role, toolName, content, turnID string, artifacts []Artifact) ThreadItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := ThreadItem{
		ID:        uuid.New().String(),
		ThreadID:  threadID,
		TurnID:    turnID,
		Role:      role,
		ToolName:  toolName,
		Content:   content,
		Artifacts: artifacts,
		CreatedAt: time.Now(),
	}
	s.items[threadID] = append(s.items[threadID], item)

	for i := range s.threads {
		if s.threads[i].ID == threadID {
			s.threads[i].UpdatedAt = item.CreatedAt
			break
		}
	}
	s.persist()
	return item
}

// GetItem returns a single item by ID across all threads.
func (s *SessionStore) GetItem(itemID string) (ThreadItem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, items := range s.items {
		for _, item := range items {
			if item.ID == itemID {
				return item, true
			}
		}
	}
	return ThreadItem{}, false
}

// UpdateItemArtifact replaces an artifact URL within an item. Returns true if updated.
func (s *SessionStore) UpdateItemArtifact(itemID, oldURL, newURL string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tid, items := range s.items {
		for i, item := range items {
			if item.ID != itemID {
				continue
			}
			for j, a := range item.Artifacts {
				if a.URL == oldURL {
					s.items[tid][i].Artifacts[j].URL = newURL
					s.persistThreadLocked(tid)
					return true
				}
			}
			return false
		}
	}
	return false
}

// GetItems returns all items for a thread.
func (s *SessionStore) GetItems(threadID string) []ThreadItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := s.items[threadID]
	out := make([]ThreadItem, len(items))
	copy(out, items)
	return out
}

// Persistence — one JSON file per thread.

type persistedThread struct {
	Thread Thread       `json:"thread"`
	Items  []ThreadItem `json:"items"`
}

func (s *SessionStore) persist() {
	if s.dataDir == "" {
		return
	}
	for _, t := range s.threads {
		s.persistThreadLocked(t.ID)
	}
}

// persistThreadLocked writes a single thread to disk. Caller must hold s.mu.
func (s *SessionStore) persistThreadLocked(threadID string) {
	if s.dataDir == "" {
		return
	}
	var thread Thread
	found := false
	for _, t := range s.threads {
		if t.ID == threadID {
			thread = t
			found = true
			break
		}
	}
	if !found {
		return
	}
	data, err := json.MarshalIndent(persistedThread{
		Thread: thread,
		Items:  s.items[threadID],
	}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.dataDir, threadID+".json"), data, 0o644)
}

func (s *SessionStore) load() error {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dataDir, e.Name()))
		if err != nil {
			continue
		}
		var pt persistedThread
		if err := json.Unmarshal(raw, &pt); err != nil {
			continue
		}
		s.threads = append(s.threads, pt.Thread)
		s.items[pt.Thread.ID] = pt.Items
	}
	return nil
}
