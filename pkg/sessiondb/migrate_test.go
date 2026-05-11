package sessiondb

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

func TestRunMigrations_FreshDB(t *testing.T) {
	db := openTestDB(t)
	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	got := appliedVersions(t, db)
	want := []int{1, 2}
	if !equalIntSlice(got, want) {
		t.Fatalf("applied versions = %v, want %v", got, want)
	}
}

func TestRunMigrations_Idempotent(t *testing.T) {
	db := openTestDB(t)
	for i := 0; i < 3; i++ {
		if err := runMigrations(db); err != nil {
			t.Fatalf("runMigrations iter %d: %v", i, err)
		}
	}

	rowCount := countMigrations(t, db)
	if rowCount != len(migrations) {
		t.Fatalf("schema_migrations rows = %d, want %d (no duplicates expected)",
			rowCount, len(migrations))
	}
}

func TestRunMigrations_PreExistingNoBookkeeping(t *testing.T) {
	db := openTestDB(t)

	// Simulate a database opened by an older binary: schema fully present,
	// but no schema_migrations table.
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("seed schema: %v", err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	got := appliedVersions(t, db)
	want := []int{1, 2}
	if !equalIntSlice(got, want) {
		t.Fatalf("applied versions = %v, want %v (baseline auto-detect)",
			got, want)
	}
}

func TestSplitStatements_HandlesTriggerBegins(t *testing.T) {
	out := splitStatements(schema)
	for _, stmt := range out {
		if strings.HasPrefix(strings.ToUpper(stmt), "CREATE TRIGGER") {
			if !strings.Contains(stmt, "END;") {
				t.Fatalf("trigger statement missing END;: %q", stmt)
			}
		}
	}
	if len(out) < 5 {
		t.Fatalf("expected several statements from schema, got %d", len(out))
	}
}

func TestColumnExists(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE t (a INTEGER, b TEXT)`); err != nil {
		t.Fatalf("create test table: %v", err)
	}

	for _, tc := range []struct {
		col  string
		want bool
	}{
		{"a", true},
		{"b", true},
		{"missing", false},
	} {
		got, err := columnExists(db, "t", tc.col)
		if err != nil {
			t.Fatalf("columnExists(%s): %v", tc.col, err)
		}
		if got != tc.want {
			t.Fatalf("columnExists(t, %s) = %v, want %v", tc.col, got, tc.want)
		}
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "test.db") + "?_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

func appliedVersions(t *testing.T, db *sql.DB) []int {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	return out
}

func countMigrations(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
