package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/cinience/saker/pkg/project/dialect"
)

// Config controls how Store is opened. DSN follows the rules in
// dialect.ParseDSN; a bare path is treated as a SQLite file.
type Config struct {
	// DSN is the database location. When empty, FallbackPath is used.
	DSN string
	// FallbackPath is the on-disk SQLite path used when DSN is empty.
	// Typically `<serverDataDir>/app.db`.
	FallbackPath string
	// Logger is forwarded to GORM. nil disables logging.
	Logger gormlogger.Interface
}

// Store wraps a *gorm.DB plus the dialect that built it. Callers should use
// the high-level service methods (CreateProject, InviteByUsername, etc.)
// instead of touching DB() directly outside of tests.
type Store struct {
	db     *gorm.DB
	driver string
	dsn    string

	// provisioningMu serializes Select-or-Create flows per logical key so
	// concurrent first-request bursts (typical for localhost or LDAP login
	// from multiple tabs) cannot race past the existence check and produce
	// duplicate rows. Keyed by caller-supplied namespace strings.
	provisioningMu sync.Map // map[string]*sync.Mutex
}

// Open creates a Store using cfg, runs AutoMigrate for AllModels(), and
// returns it ready for use.
func Open(cfg Config) (*Store, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		if cfg.FallbackPath == "" {
			return nil, errors.New("project.Open: DSN and FallbackPath both empty")
		}
		dsn = cfg.FallbackPath
	}

	scheme, body, err := dialect.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("project.Open: %w", err)
	}

	// Ensure parent directory exists for sqlite file paths so first-launch
	// users don't have to mkdir the data dir manually.
	if scheme == "sqlite" && body != ":memory:" {
		if dir := filepath.Dir(body); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("project.Open: create db dir: %w", err)
			}
		}
	}

	d, err := dialect.Resolve(scheme)
	if err != nil {
		return nil, fmt.Errorf("project.Open: %w", err)
	}
	dialector, err := d.Open(body)
	if err != nil {
		return nil, fmt.Errorf("project.Open: dialect open: %w", err)
	}

	gormCfg := &gorm.Config{}
	if cfg.Logger != nil {
		gormCfg.Logger = cfg.Logger
	} else {
		gormCfg.Logger = gormlogger.Discard
	}

	db, err := gorm.Open(dialector, gormCfg)
	if err != nil {
		return nil, fmt.Errorf("project.Open: gorm.Open: %w", err)
	}

	if err := db.AutoMigrate(AllModels()...); err != nil {
		return nil, fmt.Errorf("project.Open: auto-migrate: %w", err)
	}

	return &Store{db: db, driver: scheme, dsn: dsn}, nil
}

// DB exposes the underlying *gorm.DB. Prefer service methods; this is for
// tests and the rare cross-table query that doesn't fit a service method.
func (s *Store) DB() *gorm.DB { return s.db }

// Driver returns the resolved dialect name ("sqlite", "postgres", ...).
func (s *Store) Driver() string { return s.driver }

// Close releases the underlying DB connection (for tests / graceful shutdown).
func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// provisioningLock acquires a per-key mutex used by Select-or-Create flows.
// Callers MUST call the returned release func (typically `defer release()`).
// Different keys do not contend; the same key serializes.
func (s *Store) provisioningLock(key string) func() {
	v, _ := s.provisioningMu.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
