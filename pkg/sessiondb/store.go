// Package sessiondb provides a SQLite-based session index with FTS5 full-text
// search. It runs alongside the existing JSON file persistence as an additive
// layer — JSON files remain the source of truth; this store enables cross-session
// search and metadata queries.
package sessiondb

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/message"

	// glebarez/go-sqlite is a fork of modernc.org/sqlite that registers the
	// "sqlite" driver name with database/sql. The pkg/project store uses the
	// matching glebarez/sqlite GORM dialect; importing the same driver in
	// both places avoids a duplicate sql.Register("sqlite", ...) panic.
	_ "github.com/glebarez/go-sqlite"
)

// Store wraps a SQLite database used for session indexing and search.
type Store struct {
	db *sql.DB
	mu sync.RWMutex
}

// SessionMeta holds summary information about a session.
type SessionMeta struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	MessageCount int       `json:"message_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// IndexedMessage is a single message stored in the index.
type IndexedMessage struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	ToolName  string    `json:"tool_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SearchResult represents a FTS5 search hit.
type SearchResult struct {
	SessionID string  `json:"session_id"`
	Role      string  `json:"role"`
	Snippet   string  `json:"snippet"`
	Rank      float64 `json:"rank"`
	CreatedAt string  `json:"created_at"`
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    message_count INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL DEFAULT '',
    hash TEXT NOT NULL DEFAULT '',
    pos INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);
CREATE INDEX IF NOT EXISTS idx_messages_session_pos ON messages(session_id, pos);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content,
    content=messages,
    content_rowid=id
);

CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;

CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.id, old.content);
END;

CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.id, old.content);
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;
`

// Open creates or opens a SQLite database at the given path and applies the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("sessiondb: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite performs best with a single writer

	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("sessiondb: migrate: %w", err)
	}

	return &Store{db: db}, nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Index upserts a session and its messages into the index.
// It performs a diff-based update: unchanged messages (same position and
// hash) are skipped, changed messages are updated, new messages are
// inserted, and stale messages beyond the new range are removed.
//
// guarded by hash and position checks; splitting raises ceremony without
// reducing branches. Tracked as legacy debt.
//
//nolint:gocognit // Diff-based reindex with insert/update/delete branches
func (s *Store) Index(sessionID string, msgs []message.Message) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("sessiondb: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // After Commit, Rollback returns ErrTxDone — expected and ignorable.

	now := time.Now().UTC().Format(time.DateTime)

	// Derive a title from the first user message.
	title := deriveTitle(msgs)

	// Upsert session first (messages have a FK constraint on sessions).
	_, err = tx.Exec(`
		INSERT INTO sessions (id, title, message_count, created_at, updated_at)
		VALUES (?, ?, 0, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			updated_at = excluded.updated_at
	`, sessionID, title, now, now)
	if err != nil {
		return fmt.Errorf("sessiondb: upsert session: %w", err)
	}

	// Build new message entries with position and content hash.
	type msgEntry struct {
		pos      int
		hash     string
		role     string
		content  string
		toolName string
	}
	var newEntries []msgEntry
	for _, msg := range msgs {
		content := msg.Content
		if content == "" {
			content = blocksToText(msg.ContentBlocks)
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		toolName := ""
		if len(msg.ToolCalls) > 0 {
			toolName = msg.ToolCalls[0].Name
		}
		newEntries = append(newEntries, msgEntry{
			pos:      len(newEntries),
			hash:     msgHash(msg.Role, content, toolName),
			role:     msg.Role,
			content:  content,
			toolName: toolName,
		})
	}

	// Query existing messages for this session (pos and hash for diffing, id for updates/deletes).
	type existingRow struct {
		id   int64
		pos  int
		hash string
	}
	rows, err := tx.Query(`SELECT id, pos, hash FROM messages WHERE session_id = ? ORDER BY pos`, sessionID)
	if err != nil {
		return fmt.Errorf("sessiondb: query existing: %w", err)
	}
	var existing []existingRow
	for rows.Next() {
		var r existingRow
		if err := rows.Scan(&r.id, &r.pos, &r.hash); err != nil {
			rows.Close()
			return fmt.Errorf("sessiondb: scan existing: %w", err)
		}
		existing = append(existing, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sessiondb: existing rows: %w", err)
	}

	// Build a map of existing messages keyed by position for quick lookup.
	existingByPos := make(map[int]existingRow, len(existing))
	for _, r := range existing {
		existingByPos[r.pos] = r
	}

	// Detect migrated databases where all messages have pos=0 (the default).
	// In that case, fall back to a full delete+reinsert to assign correct positions.
	if len(existing) > 1 {
		allZero := true
		for _, r := range existing {
			if r.pos != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			if _, err := tx.Exec(`DELETE FROM messages WHERE session_id = ?`, sessionID); err != nil {
				return fmt.Errorf("sessiondb: delete old messages: %w", err)
			}
			stmt, err := tx.Prepare(`INSERT INTO messages (session_id, role, content, tool_name, hash, pos, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`)
			if err != nil {
				return fmt.Errorf("sessiondb: prepare insert: %w", err)
			}
			defer stmt.Close()
			for _, e := range newEntries {
				if _, err := stmt.Exec(sessionID, e.role, e.content, e.toolName, e.hash, e.pos, now); err != nil {
					return fmt.Errorf("sessiondb: insert message: %w", err)
				}
			}
			if _, err := tx.Exec(`UPDATE sessions SET message_count = ? WHERE id = ?`, len(newEntries), sessionID); err != nil {
				return fmt.Errorf("sessiondb: update message count: %w", err)
			}
			return tx.Commit()
		}
	}

	// Diff-based update: insert, update, or skip based on position and hash.
	for _, e := range newEntries {
		r, found := existingByPos[e.pos]
		if !found {
			// New message — insert.
			if _, err := tx.Exec(
				`INSERT INTO messages (session_id, role, content, tool_name, hash, pos, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				sessionID, e.role, e.content, e.toolName, e.hash, e.pos, now,
			); err != nil {
				return fmt.Errorf("sessiondb: insert message pos %d: %w", e.pos, err)
			}
		} else if r.hash != e.hash {
			// Changed message — update (FTS trigger handles the index change).
			if _, err := tx.Exec(
				`UPDATE messages SET role = ?, content = ?, tool_name = ?, hash = ?, created_at = ? WHERE id = ?`,
				e.role, e.content, e.toolName, e.hash, now, r.id,
			); err != nil {
				return fmt.Errorf("sessiondb: update message pos %d: %w", e.pos, err)
			}
		}
		// else: unchanged — skip entirely.
	}

	// Remove stale messages (positions beyond the new range).
	if len(newEntries) < len(existingByPos) {
		if _, err := tx.Exec(
			`DELETE FROM messages WHERE session_id = ? AND pos >= ?`,
			sessionID, len(newEntries),
		); err != nil {
			return fmt.Errorf("sessiondb: delete stale messages: %w", err)
		}
	}

	// Update session with the actual persisted message count.
	if _, err := tx.Exec(`UPDATE sessions SET message_count = ? WHERE id = ?`, len(newEntries), sessionID); err != nil {
		return fmt.Errorf("sessiondb: update message count: %w", err)
	}

	return tx.Commit()
}

// DeleteSession removes a session and all its messages from the index.
func (s *Store) DeleteSession(sessionID string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("sessiondb: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // After Commit, Rollback returns ErrTxDone — expected and ignorable.

	// Delete messages first (triggers will clean up FTS).
	if _, err := tx.Exec(`DELETE FROM messages WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("sessiondb: delete messages: %w", err)
	}

	// Delete session row.
	if _, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, sessionID); err != nil {
		return fmt.Errorf("sessiondb: delete session: %w", err)
	}

	return tx.Commit()
}

// Search performs a FTS5 full-text search across all sessions.
func (s *Store) Search(query string, limit int) ([]SearchResult, error) {
	if s == nil {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT m.session_id, m.role,
			snippet(messages_fts, 0, '**', '**', '...', 32) AS snippet,
			rank,
			m.created_at
		FROM messages_fts
		JOIN messages m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.SessionID, &r.Role, &r.Snippet, &r.Rank, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("sessiondb: scan result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ListSessions returns sessions ordered by most recently updated.
func (s *Store) ListSessions(limit, offset int) ([]SessionMeta, error) {
	if s == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, title, message_count, created_at, updated_at
		FROM sessions
		ORDER BY updated_at DESC, id ASC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionMeta
	for rows.Next() {
		var sm SessionMeta
		var createdAt, updatedAt string
		if err := rows.Scan(&sm.ID, &sm.Title, &sm.MessageCount, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("sessiondb: scan session: %w", err)
		}
		sm.CreatedAt = parseTime(createdAt)
		sm.UpdatedAt = parseTime(updatedAt)
		sessions = append(sessions, sm)
	}
	return sessions, rows.Err()
}

// GetSession retrieves all indexed messages for a session.
func (s *Store) GetSession(sessionID string) ([]IndexedMessage, error) {
	if s == nil {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, session_id, role, content, tool_name, created_at
		FROM messages
		WHERE session_id = ?
		ORDER BY id ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("sessiondb: get session: %w", err)
	}
	defer rows.Close()

	var msgs []IndexedMessage
	for rows.Next() {
		var m IndexedMessage
		var createdAt string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.ToolName, &createdAt); err != nil {
			return nil, fmt.Errorf("sessiondb: scan message: %w", err)
		}
		m.CreatedAt = parseTime(createdAt)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// parseTime parses a DateTime string, returning a zero-value time on failure.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.DateTime, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// msgHash returns a SHA-256 digest of a message's role, content, and tool name,
// used for diff-based indexing to detect unchanged rows.
func msgHash(role, content, toolName string) string {
	h := sha256.New()
	h.Write([]byte(role))
	h.Write([]byte{0})
	h.Write([]byte(content))
	h.Write([]byte{0})
	h.Write([]byte(toolName))
	return hex.EncodeToString(h.Sum(nil))
}

// deriveTitle extracts a short title from the first user message.
func deriveTitle(msgs []message.Message) string {
	for _, msg := range msgs {
		if msg.Role != "user" {
			continue
		}
		text := msg.Content
		if text == "" {
			text = blocksToText(msg.ContentBlocks)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		// Take first line, truncated to 100 chars.
		if idx := strings.IndexByte(text, '\n'); idx > 0 {
			text = text[:idx]
		}
		if len(text) > 100 {
			text = text[:97] + "..."
		}
		return text
	}
	return ""
}

// blocksToText concatenates text content blocks.
func blocksToText(blocks []message.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == message.ContentBlockText && b.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}
