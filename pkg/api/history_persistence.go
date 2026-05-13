package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/message"
)

type diskHistoryPersister struct {
	dir string
}

type persistedHistory struct {
	Version   int               `json:"version"`
	SessionID string            `json:"session_id,omitempty"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
	Messages  []message.Message `json:"messages,omitempty"`
}

func newDiskHistoryPersister(projectRoot, configRoot string) *diskHistoryPersister {
	projectRoot = strings.TrimSpace(projectRoot)
	configRoot = strings.TrimSpace(configRoot)
	if projectRoot == "" && configRoot == "" {
		return nil
	}
	base := configRoot
	if base == "" {
		base = filepath.Join(projectRoot, ".saker")
	} else if !filepath.IsAbs(base) && projectRoot != "" {
		base = filepath.Join(projectRoot, base)
	}
	return &diskHistoryPersister{
		dir: filepath.Join(base, "history"),
	}
}

// PersistedHistoryFilePath returns the canonical on-disk history path for a
// project/session pair. Empty strings mean persistence is disabled or inputs are invalid.
func PersistedHistoryFilePath(projectRoot, sessionID string) string {
	p := newDiskHistoryPersister(projectRoot, "")
	if p == nil {
		return ""
	}
	return p.filePath(sessionID)
}

// LoadPersistedHistory loads a session history from disk using the runtime's
// canonical persistence format. The found flag is false when no persisted file exists.
func LoadPersistedHistory(projectRoot, sessionID string) ([]message.Message, bool, error) {
	p := newDiskHistoryPersister(projectRoot, "")
	if p == nil {
		return nil, false, nil
	}
	path := p.filePath(sessionID)
	if path == "" {
		return nil, false, nil
	}
	msgs, err := p.Load(sessionID)
	if err != nil {
		return nil, false, err
	}
	if msgs != nil {
		return msgs, true, nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read history: %w", err)
	}
	return nil, true, nil
}

// SavePersistedHistory stores a session history using the runtime's canonical
// persistence format. Empty project roots are treated as disabled persistence.
func SavePersistedHistory(projectRoot, sessionID string, msgs []message.Message) error {
	p := newDiskHistoryPersister(projectRoot, "")
	if p == nil {
		return nil
	}
	return p.Save(sessionID, msgs)
}

func (p *diskHistoryPersister) Load(sessionID string) ([]message.Message, error) {
	path := p.filePath(sessionID)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read history: %w", err)
	}
	var wrapper persistedHistory
	if err := json.Unmarshal(data, &wrapper); err == nil {
		if wrapper.Version != 0 || wrapper.SessionID != "" || !wrapper.UpdatedAt.IsZero() || wrapper.Messages != nil {
			return sanitizeHistory(message.CloneMessages(wrapper.Messages)), nil
		}
	}
	var msgs []message.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, fmt.Errorf("decode history: %w", err)
	}
	return sanitizeHistory(message.CloneMessages(msgs)), nil
}

// sanitizeHistory removes corrupted or duplicate consecutive same-role messages
// from persisted history to prevent API errors (e.g. "messages must alternate").
func sanitizeHistory(msgs []message.Message) []message.Message {
	if len(msgs) <= 1 {
		return msgs
	}
	cleaned := make([]message.Message, 0, len(msgs))
	for i, msg := range msgs {
		// Drop messages with corrupted serialization artifacts.
		if strings.HasPrefix(msg.Content, "map[") {
			slog.Warn("api: dropping corrupted history message", "index", i)
			continue
		}
		// Replace stripped image/document blocks with text placeholders so that
		// restored sessions don't send "[stripped]" as base64 to the model API.
		if len(msg.ContentBlocks) > 0 {
			for j := range msg.ContentBlocks {
				cb := &msg.ContentBlocks[j]
				if cb.Data == "[stripped]" {
					switch cb.Type {
					case message.ContentBlockImage:
						*cb = message.ContentBlock{Type: message.ContentBlockText, Text: "[image]"}
					case message.ContentBlockDocument:
						*cb = message.ContentBlock{Type: message.ContentBlockText, Text: "[document]"}
					}
				}
			}
		}
		// Merge consecutive same-role user messages to avoid API rejection.
		if len(cleaned) > 0 && msg.Role == cleaned[len(cleaned)-1].Role && msg.Role == "user" {
			slog.Warn("api: merging consecutive user message", "index", i)
			prev := &cleaned[len(cleaned)-1]
			prev.Content = strings.TrimSpace(prev.Content + "\n" + msg.Content)
			continue
		}
		cleaned = append(cleaned, msg)
	}
	return cleaned
}

func (p *diskHistoryPersister) Save(sessionID string, msgs []message.Message) error {
	path := p.filePath(sessionID)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return fmt.Errorf("mkdir history dir: %w", err)
	}
	payload := persistedHistory{
		Version:   1,
		SessionID: sessionID,
		UpdatedAt: time.Now().UTC(),
		Messages:  stripImageData(message.CloneMessages(msgs)),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode history: %w", err)
	}

	tmp, err := os.CreateTemp(p.dir, sanitizePathComponent(sessionID)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp history: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write history temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close history temp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// Windows can't rename over an existing file.
		_ = os.Remove(path)
		if retry := os.Rename(tmpPath, path); retry != nil {
			return fmt.Errorf("rename history: %w", retry)
		}
	}
	return nil
}

func (p *diskHistoryPersister) Cleanup(retainDays int) error {
	if p == nil {
		return nil
	}
	if retainDays <= 0 {
		return nil
	}
	dir := strings.TrimSpace(p.dir)
	if dir == "" {
		return nil
	}

	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read history dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("read history dir: %w", os.ErrInvalid)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read history dir: %w", err)
	}
	cutoff := time.Now().AddDate(0, 0, -retainDays)
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (p *diskHistoryPersister) filePath(sessionID string) string {
	if p == nil {
		return ""
	}
	dir := strings.TrimSpace(p.dir)
	if dir == "" {
		return ""
	}
	name := sanitizePathComponent(sessionID)
	if name == "" {
		return ""
	}
	return filepath.Join(dir, name+".json")
}

// stripImageData removes base64 image data from ContentBlocks before
// persisting to disk. This prevents history files from ballooning in size
// while preserving the image metadata (type, media_type, path in artifacts).
// Operates on an already-cloned slice so the in-memory history is unaffected.
func stripImageData(msgs []message.Message) []message.Message {
	for i := range msgs {
		if len(msgs[i].ContentBlocks) == 0 {
			continue
		}
		for j := range msgs[i].ContentBlocks {
			if msgs[i].ContentBlocks[j].Type == message.ContentBlockImage && msgs[i].ContentBlocks[j].Data != "" {
				msgs[i].ContentBlocks[j].Data = "[stripped]"
			}
		}
	}
	return msgs
}

// resolveConfigBase returns the .saker config directory path, or "" if disabled.
func resolveConfigBase(projectRoot, configRoot string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	configRoot = strings.TrimSpace(configRoot)
	if projectRoot == "" && configRoot == "" {
		return ""
	}
	base := configRoot
	if base == "" {
		base = filepath.Join(projectRoot, ".saker")
	} else if !filepath.IsAbs(base) && projectRoot != "" {
		base = filepath.Join(projectRoot, base)
	}
	return base
}

func (rt *Runtime) persistHistory(sessionID string, history *message.History) {
	if rt == nil || history == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	snapshot := history.All()
	if len(snapshot) == 0 {
		return
	}
	// Primary: event-sourced conversation log (SQLite/Postgres).
	rt.persistToConversation(sessionID, history)
	// Fallback: JSON history file when conversation store is unavailable.
	if rt.conversationStore == nil && rt.historyPersister != nil {
		if err := rt.historyPersister.Save(sessionID, snapshot); err != nil {
			slog.Error("api: persist history failed", "session_id", sessionID, "error", err)
		}
	}
}
