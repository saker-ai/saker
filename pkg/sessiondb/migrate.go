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
// `guard` is an optional pre-flight check. If it returns skip=true the
// migration body is not executed but the version is still recorded as
// applied — used when the destination state was already reached by an
// earlier `IF NOT EXISTS` schema apply on a freshly-opened database.
type migration struct {
	version int
	name    string
	sql     string
	guard   func(*sql.DB) (skip bool, err error)
}

// migrations is the canonical, ordered list of schema steps. Append new
// migrations at the end with strictly increasing version numbers — never
// rewrite or reorder a published version, since a deployed DB will already
// have it recorded as applied.
//
// Migration #1 is intentionally the full schema string from store.go: it
// uses CREATE ... IF NOT EXISTS, so re-applying on a database that was
// already opened by an older binary is a no-op and the migration becomes
// the recorded baseline.
//
// Migration #2 captures the ad-hoc ALTER TABLE pattern from the older
// inline code in Open() — adding hash/pos columns. The check guard is the
// schema_migrations row, not the "duplicate column" error string.
var migrations = []migration{
	{
		version: 1,
		name:    "initial_schema",
		sql:     schema,
	},
	{
		version: 2,
		name:    "messages_hash_pos",
		sql: `
ALTER TABLE messages ADD COLUMN hash TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN pos INTEGER NOT NULL DEFAULT 0;
`,
		// Skip when migration #1 already created the columns — the current
		// `schema` constant in store.go includes hash/pos, so a fresh open
		// satisfies #2 implicitly. Older databases (created before hash/pos
		// were folded into the baseline) trigger the real ALTER path.
		guard: func(db *sql.DB) (bool, error) {
			has, err := columnExists(db, "messages", "hash")
			if err != nil {
				return false, err
			}
			return has, nil
		},
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

	// Special case for pre-existing DBs created before this migration
	// system existed: detect "messages.hash" column and silently mark
	// migrations 1+2 as applied so we don't try to ALTER an existing
	// column.
	if len(applied) == 0 {
		if has, err := columnExists(db, "messages", "hash"); err == nil && has {
			for _, m := range migrations {
				if m.version > 2 {
					break
				}
				if err := recordMigration(db, m); err != nil {
					return err
				}
				applied[m.version] = true
			}
			slog.Info("sessiondb: detected pre-existing schema, baseline marked",
				"highest_baseline_version", 2)
		}
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
	rows, err := db.Query(`SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, table, column)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), nil
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
