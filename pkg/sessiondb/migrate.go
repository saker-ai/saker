package sessiondb

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

// migration is a single forward-only schema step. SQL is split into
// statements at semicolons and applied in one transaction so partial
// application can never leave the DB in an in-between state.
//
// `apply` is an optional custom transaction body for steps that need
// schema-aware idempotence that SQLite DDL cannot express directly.
//
// `guard` is an optional pre-flight check. If it returns skip=true the
// migration body is not executed but the version is still recorded as
// applied — used when the destination state was already reached by an
// earlier `IF NOT EXISTS` schema apply on a freshly-opened database.
type migration struct {
	version int
	name    string
	sql     string
	apply   func(*sql.Tx) error
	guard   func(*sql.DB) (skip bool, err error)
}

// initialSchema is the v1 baseline as it existed before messages.hash and
// messages.pos were added. Keep it separate from store.go's current `schema`:
// replaying the current schema against a legacy messages table is unsafe
// because CREATE TABLE IF NOT EXISTS leaves the old table shape untouched while
// later index DDL may reference newer columns.
const initialSchema = `
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
    created_at DATETIME NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);

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

// migrations is the canonical, ordered list of schema steps. Append new
// migrations at the end with strictly increasing version numbers — never
// rewrite or reorder a published version, since a deployed DB will already
// have it recorded as applied.
//
// Migration #1 is the original schema shape. Do not replace it with the
// current schema from store.go: older databases may already have `messages`
// without newer columns, and SQLite will not reconcile that table through
// CREATE TABLE IF NOT EXISTS.
//
// Migration #2 captures the ad-hoc ALTER TABLE pattern from the older
// inline code in Open() — adding hash/pos columns and the positional index.
// It probes the actual table shape so partially upgraded databases recover
// without relying on "duplicate column" error strings.
var migrations = []migration{
	{
		version: 1,
		name:    "initial_schema",
		sql:     initialSchema,
	},
	{
		version: 2,
		name:    "messages_hash_pos",
		apply:   ensureMessagesHashPos,
	},
}

// runMigrations brings the connected database forward to the highest
// version in the migrations slice. Bookkeeping lives in schema_migrations,
// avoiding the previous strategy of executing every ALTER and swallowing
// "duplicate column" errors.
func runMigrations(db *sql.DB) error {
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := loadAppliedVersions(db)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if m.guard != nil {
			skip, err := m.guard(db)
			if err != nil {
				return fmt.Errorf("guard migration %d %s: %w", m.version, m.name, err)
			}
			if skip {
				if err := recordMigration(db, m); err != nil {
					return err
				}
				slog.Info("sessiondb: migration skipped (guard satisfied)",
					"version", m.version, "name", m.name)
				continue
			}
		}
		if err := applyMigration(db, m); err != nil {
			return fmt.Errorf("apply migration %d %s: %w", m.version, m.name, err)
		}
		slog.Info("sessiondb: migration applied", "version", m.version, "name", m.name)
	}
	return nil
}

func loadAppliedVersions(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return applied, nil
}

func applyMigration(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	for _, stmt := range splitStatements(m.sql) {
		if _, err := tx.Exec(stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("exec %q: %w", truncate(stmt, 80), err)
		}
	}
	if m.apply != nil {
		if err := m.apply(tx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}

	if _, err := tx.Exec(
		`INSERT INTO schema_migrations(version, name) VALUES (?, ?)`,
		m.version, m.name,
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record version %d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func recordMigration(db *sql.DB, m migration) error {
	_, err := db.Exec(
		`INSERT OR IGNORE INTO schema_migrations(version, name) VALUES (?, ?)`,
		m.version, m.name,
	)
	if err != nil {
		return fmt.Errorf("record migration %d: %w", m.version, err)
	}
	return nil
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	return columnExistsQuery(db, table, column)
}

func columnExistsTx(tx *sql.Tx, table, column string) (bool, error) {
	return columnExistsQuery(tx, table, column)
}

type sqlQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func columnExistsQuery(q sqlQueryer, table, column string) (bool, error) {
	rows, err := q.Query(`SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, table, column)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), nil
}

func ensureMessagesHashPos(tx *sql.Tx) error {
	hasHash, err := columnExistsTx(tx, "messages", "hash")
	if err != nil {
		return fmt.Errorf("check messages.hash: %w", err)
	}
	if !hasHash {
		if _, err := tx.Exec(`ALTER TABLE messages ADD COLUMN hash TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add messages.hash: %w", err)
		}
	}

	hasPos, err := columnExistsTx(tx, "messages", "pos")
	if err != nil {
		return fmt.Errorf("check messages.pos: %w", err)
	}
	if !hasPos {
		if _, err := tx.Exec(`ALTER TABLE messages ADD COLUMN pos INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add messages.pos: %w", err)
		}
	}

	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_session_pos ON messages(session_id, pos)`); err != nil {
		return fmt.Errorf("create idx_messages_session_pos: %w", err)
	}
	return nil
}

// splitStatements breaks a multi-statement SQL blob at top-level semicolons.
// SQLite does not support compound statements directly through database/sql.
// CREATE TRIGGER bodies in the initial schema use BEGIN/END blocks with
// internal semicolons; treat the whole BEGIN...END as one statement.
func splitStatements(sqlText string) []string {
	var (
		stmts         []string
		current       strings.Builder
		insideTrigger bool
	)
	for _, raw := range strings.Split(sqlText, "\n") {
		line := strings.TrimRight(raw, " \t")
		upper := strings.ToUpper(strings.TrimSpace(line))

		switch {
		case strings.HasPrefix(upper, "CREATE TRIGGER"):
			insideTrigger = true
		case insideTrigger && upper == "END;":
			current.WriteString(line)
			current.WriteString("\n")
			stmts = appendStmt(stmts, current.String())
			current.Reset()
			insideTrigger = false
			continue
		}

		if !insideTrigger && strings.HasSuffix(strings.TrimSpace(line), ";") {
			current.WriteString(line)
			current.WriteString("\n")
			stmts = appendStmt(stmts, current.String())
			current.Reset()
			continue
		}

		current.WriteString(line)
		current.WriteString("\n")
	}
	if remaining := strings.TrimSpace(current.String()); remaining != "" {
		stmts = appendStmt(stmts, current.String())
	}
	return stmts
}

func appendStmt(stmts []string, s string) []string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return stmts
	}
	return append(stmts, trimmed)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
