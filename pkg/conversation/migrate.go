package conversation

import (
	"fmt"
	"log/slog"

	"gorm.io/gorm"
)

// migration is one forward-only schema step. P0 ships a single migration
// (version 1) that wraps GORM AutoMigrate over AllModels(). The framework
// is intentionally generous so P1+ can drop in raw SQL steps for FTS5
// triggers, view definitions, and cross-table backfills without a rewrite.
//
// `apply` runs inside a fresh GORM transaction. Returning an error rolls
// back without recording the version.
//
// `guard` is a pre-flight probe that lets a migration short-circuit when
// the destination state was already reached out-of-band (e.g. AutoMigrate
// landed an idempotent table during an earlier run that crashed before
// recording the version). When guard returns skip=true the body is not
// executed but the version is still recorded as applied — exactly mirrors
// a standard migration guard pattern.
type migration struct {
	version int
	name    string
	apply   func(*gorm.DB) error
	guard   func(*gorm.DB) (skip bool, err error)
}

// migrations is the canonical, ordered list of schema steps. Append new
// migrations at the end with strictly increasing version numbers — never
// rewrite or reorder a published version, since a deployed DB will
// already have it recorded as applied.
//
// Migration #1 calls AutoMigrate over AllModels(). Because AllModels
// grew in P1 to include Message, a fresh DB lands all four tables in
// one shot. An existing P0 DB skips v1 (already recorded) and picks up
// the messages table from migration #2's AutoMigrate fallback below.
//
// Migration #2 attaches the FTS5 read projection (virtual table +
// AI/AD/AU triggers) over messages. The DDL is SQLite-only — Postgres
// FTS lands later as part of the multi-driver work; on Postgres this
// migration runs the AutoMigrate(&Message{}) fallback and then no-ops
// on the FTS pieces.
var migrations = []migration{
	{
		version: 1,
		name:    "initial_schema",
		apply: func(tx *gorm.DB) error {
			return tx.AutoMigrate(AllModels()...)
		},
		guard: func(db *gorm.DB) (bool, error) {
			// If the threads table already exists from a previous
			// crashed-after-AutoMigrate run, treat version 1 as already
			// applied. Idempotent re-AutoMigrate is harmless on an
			// existing table but the explicit guard makes the log line
			// readable ("skipped" vs "applied").
			return db.Migrator().HasTable(&Thread{}), nil
		},
	},
	{
		version: 2,
		name:    "messages_fts5",
		apply:   applyMessagesFTS5,
	},
	{
		version: 3,
		name:    "turn_contexts",
		apply: func(tx *gorm.DB) error {
			// AutoMigrate is idempotent: a fresh DB at v1+v2 picks up the
			// turn_contexts table here for the first time; an upgrade DB
			// where someone added the model after deploying P2 also lands
			// it cleanly. The compound unique index on (thread_id, turn_id)
			// is what enforces UPSERT semantics in PutTurnContext.
			return tx.AutoMigrate(&TurnContext{})
		},
	},
	{
		version: 4,
		name:    "blobs",
		apply: func(tx *gorm.DB) error {
			// blobs is a leaf table: AutoMigrate creates it with the
			// (ref_count, created_at) composite index that GCBlobs scans.
			// No FK from events.blob_refs (it's a JSON array, not a
			// scalar column) — referential integrity is enforced
			// imperatively in AppendEvent's tx.
			return tx.AutoMigrate(&Blob{})
		},
	},
}

// applyMessagesFTS5 lands the messages table (in case the DB was
// initialized at P0 and never had messages auto-migrated) and the FTS5
// virtual table + AI/AD/AU sync triggers. The trigger pattern mirrors
// a standard FTS5 sync-trigger pattern — self-contained, no external
// dependencies.
func applyMessagesFTS5(tx *gorm.DB) error {
	// First, make sure the messages table exists. AutoMigrate is
	// idempotent: a fresh DB already created it via v1; an upgrade DB
	// (P0 → P1) didn't and gets it now.
	if err := tx.AutoMigrate(&Message{}); err != nil {
		return fmt.Errorf("automigrate messages: %w", err)
	}

	// FTS5 is SQLite-specific. Skip silently on other drivers; the
	// Search method will surface a clear "FTS not available on driver X"
	// error at call time. The driver name lives on the dialector's
	// Name() — for GORM that's exposed via tx.Dialector.Name().
	driver := tx.Dialector.Name()
	if driver != "sqlite" {
		return nil
	}

	stmts := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
            content,
            content=messages,
            content_rowid=id
        )`,
		`CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
            INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
        END`,
		`CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
            INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.id, old.content);
        END`,
		`CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
            INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.id, old.content);
            INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
        END`,
	}
	for _, stmt := range stmts {
		if err := tx.Exec(stmt).Error; err != nil {
			return fmt.Errorf("apply fts5 ddl: %w", err)
		}
	}
	return nil
}

// runMigrations brings the connected database forward to the highest
// version in the migrations slice. Bookkeeping lives in a dedicated
// schema_migrations table so re-running on an up-to-date DB is a no-op.
//
// schema_migrations itself is created via raw DDL (not GORM AutoMigrate)
// because the migration framework needs to query it before any models
// are migrated — chicken-and-egg avoidance.
func runMigrations(db *gorm.DB) error {
	if err := db.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
`).Error; err != nil {
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
				slog.Info("conversation: migration skipped (guard satisfied)",
					"version", m.version, "name", m.name)
				continue
			}
		}
		if err := applyMigration(db, m); err != nil {
			return fmt.Errorf("apply migration %d %s: %w", m.version, m.name, err)
		}
		slog.Info("conversation: migration applied", "version", m.version, "name", m.name)
	}
	return nil
}

func loadAppliedVersions(db *gorm.DB) (map[int]bool, error) {
	var versions []int
	if err := db.Raw("SELECT version FROM schema_migrations").Scan(&versions).Error; err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	applied := map[int]bool{}
	for _, v := range versions {
		applied[v] = true
	}
	return applied, nil
}

func applyMigration(db *gorm.DB, m migration) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if m.apply != nil {
			if err := m.apply(tx); err != nil {
				return err
			}
		}
		if err := tx.Exec(
			`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`,
			m.version, m.name, nowUTC(),
		).Error; err != nil {
			return fmt.Errorf("record version %d: %w", m.version, err)
		}
		return nil
	})
}

func recordMigration(db *gorm.DB, m migration) error {
	if err := db.Exec(
		`INSERT OR IGNORE INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, nowUTC(),
	).Error; err != nil {
		return fmt.Errorf("record migration %d: %w", m.version, err)
	}
	return nil
}
