package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	provisioningMu sync.Map // map[string]*provisioningMuEntry

	// stopCh stops the provisioningMu cleanup goroutine.
	stopCh   chan struct{}
	closeOnce sync.Once
}

// provisioningMuEntry wraps a per-key mutex with a last-access timestamp so
// stale entries can be swept by the background cleanup goroutine.
type provisioningMuEntry struct {
	mu         sync.Mutex
	lastAccess time.Time
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

	s := &Store{db: db, driver: scheme, dsn: dsn, stopCh: make(chan struct{})}
	go s.provisioningCleanupLoop()
	return s, nil
}

// DB exposes the underlying *gorm.DB. Prefer service methods; this is for
// tests and the rare cross-table query that doesn't fit a service method.
func (s *Store) DB() *gorm.DB { return s.db }

// Driver returns the resolved dialect name ("sqlite", "postgres", ...).
func (s *Store) Driver() string { return s.driver }

// Close releases the underlying DB connection and stops the background
// provisioningMu cleanup goroutine (for tests / graceful shutdown).
// Safe to call multiple times; subsequent calls are no-ops.
func (s *Store) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.stopCh != nil {
			close(s.stopCh)
		}
		sqlDB, dbErr := s.db.DB()
		if dbErr != nil {
			err = dbErr
			return
		}
		err = sqlDB.Close()
	})
	return err
}

// provisioningLock acquires a per-key mutex used by Select-or-Create flows.
// Callers MUST call the returned release func (typically `defer release()`).
// Different keys do not contend; the same key serializes.
func (s *Store) provisioningLock(key string) func() {
	now := time.Now()
	v, _ := s.provisioningMu.LoadOrStore(key, &provisioningMuEntry{lastAccess: now})
	entry := v.(*provisioningMuEntry)
	entry.mu.Lock()
	entry.lastAccess = now
	return entry.mu.Unlock
}

// provisioningCleanupLoop periodically removes stale entries from the
// provisioningMu sync.Map (entries older than 1 hour since last access).
func (s *Store) provisioningCleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			now := time.Now()
			cutoff := now.Add(-1 * time.Hour)
			s.provisioningMu.Range(func(key, value any) bool {
				entry := value.(*provisioningMuEntry)
				entry.mu.Lock()
				if entry.lastAccess.Before(cutoff) {
					s.provisioningMu.Delete(key)
				}
				entry.mu.Unlock()
				return true
			})
		}
	}
}
