package subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Transcript records a subagent's conversation for persistence and resume.
type Transcript struct {
	AgentID    string              `json:"agent_id"`
	SessionID  string              `json:"session_id"`
	Profile    string              `json:"profile"`
	Messages   []TranscriptMessage `json:"messages"`
	StartedAt  time.Time           `json:"started_at"`
	FinishedAt *time.Time          `json:"finished_at,omitempty"`
	Usage      map[string]any      `json:"usage,omitempty"`
	Status     Status              `json:"status"`
}

// TranscriptMessage is a simplified message record for transcript persistence.
type TranscriptMessage struct {
	Role    string    `json:"role"`
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
}

// TranscriptStore persists and retrieves subagent transcripts.
type TranscriptStore interface {
	Save(t Transcript) error
	Load(agentID string) (*Transcript, error)
	List(sessionID string) ([]Transcript, error)
}

// MemoryTranscriptStore implements TranscriptStore in memory (for testing).
type MemoryTranscriptStore struct {
	mu          sync.RWMutex
	transcripts map[string]Transcript
}

// NewMemoryTranscriptStore creates an in-memory transcript store.
func NewMemoryTranscriptStore() *MemoryTranscriptStore {
	return &MemoryTranscriptStore{transcripts: map[string]Transcript{}}
}

func (s *MemoryTranscriptStore) Save(t Transcript) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transcripts[t.AgentID] = t
	return nil
}

func (s *MemoryTranscriptStore) Load(agentID string) (*Transcript, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.transcripts[agentID]
	if !ok {
		return nil, fmt.Errorf("transcript not found: %s", agentID)
	}
	return &t, nil
}

func (s *MemoryTranscriptStore) List(sessionID string) ([]Transcript, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Transcript
	for _, t := range s.transcripts {
		if t.SessionID == sessionID {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartedAt.Before(result[j].StartedAt)
	})
	return result, nil
}

// FileTranscriptStore persists transcripts as JSON files on disk.
type FileTranscriptStore struct {
	dir string
}

// NewFileTranscriptStore creates a file-based transcript store.
// Files are stored as {dir}/{agentID}.json.
func NewFileTranscriptStore(dir string) (*FileTranscriptStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create transcript dir: %w", err)
	}
	return &FileTranscriptStore{dir: dir}, nil
}

func (s *FileTranscriptStore) Save(t Transcript) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal transcript: %w", err)
	}
	path := filepath.Join(s.dir, t.AgentID+".json")
	return os.WriteFile(path, data, 0o644)
}

func (s *FileTranscriptStore) Load(agentID string) (*Transcript, error) {
	path := filepath.Join(s.dir, agentID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("transcript not found: %s", agentID)
		}
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	var t Transcript
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("unmarshal transcript: %w", err)
	}
	return &t, nil
}

func (s *FileTranscriptStore) List(sessionID string) ([]Transcript, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read transcript dir: %w", err)
	}
	var result []Transcript
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var t Transcript
		if err := json.Unmarshal(data, &t); err != nil {
			continue
		}
		if t.SessionID == sessionID {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartedAt.Before(result[j].StartedAt)
	})
	return result, nil
}
