package server

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/conversation"
	"github.com/google/uuid"
)

// SessionStore manages thread state as an in-memory cache backed by
// conversation.Store. Mutations are mirrored to conversation.Store via
// the attached convTee; startup state is populated via LoadFromConversation.
type SessionStore struct {
	mu      sync.RWMutex
	threads []Thread
	items   map[string][]ThreadItem // threadID → items
	tee     *convTee
}

// AttachConvTee wires the dual-write tee into this SessionStore. Safe to
// call once after construction; subsequent calls overwrite (last wins). Pass
// nil to detach. The tee is held by reference: callers may share one
// conversation.Store across many SessionStores by binding distinct projectIDs.
func (s *SessionStore) AttachConvTee(tee *convTee) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.tee = tee
	s.mu.Unlock()
}

// NewSessionStore creates an empty in-memory store. Call LoadFromConversation
// after construction to populate startup state from conversation.Store.
func NewSessionStore() (*SessionStore, error) {
	return &SessionStore{
		threads: make([]Thread, 0),
		items:   make(map[string][]ThreadItem),
	}, nil
}

// LoadFromConversation populates in-memory threads and items from the
// conversation.Store. No-op when store is nil. Called once after construction,
// before AttachConvTee, so the initial state is visible before dual-write
// begins.
func (s *SessionStore) LoadFromConversation(store *conversation.Store, projectID string) error {
	if s == nil || store == nil {
		return nil
	}
	ctx := context.Background()
	threads, err := store.ListThreads(ctx, projectID, conversation.ListThreadsOpts{
		Limit: conversation.MaxListLimit,
	})
	if err != nil {
		return fmt.Errorf("load threads: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ct := range threads {
		t := Thread{
			ID:        ct.ID,
			Title:     ct.Title,
			CreatedAt: ct.CreatedAt,
			UpdatedAt: ct.UpdatedAt,
		}
		s.threads = append(s.threads, t)
		msgs, err := store.GetMessages(ctx, ct.ID, conversation.GetMessagesOpts{
			Limit: conversation.MaxListLimit,
		})
		if err != nil {
			s.items[ct.ID] = make([]ThreadItem, 0)
			continue
		}
		items := make([]ThreadItem, 0, len(msgs))
		for _, m := range msgs {
			items = append(items, ThreadItem{
				ID:        strconv.FormatInt(m.ID, 10),
				ThreadID:  m.ThreadID,
				TurnID:    m.TurnID,
				Role:      m.Role,
				Content:   m.Content,
				CreatedAt: m.CreatedAt,
			})
		}
		s.items[ct.ID] = items
	}
	return nil
}

// CreateThread creates a new conversation thread.
func (s *SessionStore) CreateThread(title string) Thread {
	s.mu.Lock()
	now := time.Now()
	t := Thread{
		ID:        uuid.New().String(),
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.threads = append(s.threads, t)
	s.items[t.ID] = make([]ThreadItem, 0)
	tee := s.tee
	s.mu.Unlock()
	tee.recordThreadCreate(t.ID, title)
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
	for i := range s.threads {
		if s.threads[i].ID == threadID {
			s.threads[i].Title = title
			s.threads[i].UpdatedAt = time.Now()
			tee := s.tee
			s.mu.Unlock()
			tee.recordThreadTitleUpdate(threadID, title)
			return true
		}
	}
	s.mu.Unlock()
	return false
}

// DeleteThread removes a thread and its items from the in-memory cache.
func (s *SessionStore) DeleteThread(threadID string) bool {
	s.mu.Lock()
	found := false
	for i := range s.threads {
		if s.threads[i].ID == threadID {
			s.threads = append(s.threads[:i], s.threads[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return false
	}
	delete(s.items, threadID)
	tee := s.tee
	s.mu.Unlock()
	tee.recordThreadDelete(threadID)
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
	tee := s.tee
	s.mu.Unlock()
	tee.recordItem(threadID, role, content, turnID, nil)
	return item
}

// AppendItemWithArtifacts adds a message with media artifacts to a thread.
func (s *SessionStore) AppendItemWithArtifacts(threadID, role, content, turnID string, artifacts []Artifact) ThreadItem {
	item := s.appendItemFull(threadID, role, "", content, turnID, artifacts)
	s.mu.RLock()
	tee := s.tee
	s.mu.RUnlock()
	tee.recordItem(threadID, role, content, turnID, artifacts)
	return item
}

// AppendToolItem adds a tool result item with an explicit tool name.
func (s *SessionStore) AppendToolItem(threadID, toolName, content, turnID string, artifacts []Artifact) ThreadItem {
	item := s.appendItemFull(threadID, "tool", toolName, content, turnID, artifacts)
	s.mu.RLock()
	tee := s.tee
	s.mu.RUnlock()
	tee.recordToolItem(threadID, toolName, content, turnID, artifacts)
	return item
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

