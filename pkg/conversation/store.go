package conversation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/cinience/saker/pkg/project/dialect"
)

// seqMuSweepInterval controls how often the background goroutine sweeps
// idle per-thread mutexes. 10 minutes keeps long-running multi-tenant
// servers tidy without spending measurable CPU.
const seqMuSweepInterval = 10 * time.Minute

// seqMuMaxIdle is the idle threshold beyond which a per-thread mutex is
// eligible for eviction. Threads active within this window keep their lock.
const seqMuMaxIdle = 30 * time.Minute

// Config controls how Store is opened. DSN follows the rules in
// dialect.ParseDSN; a bare path is treated as a SQLite file. The fallback
// path mirrors pkg/project.Config so callers can stand up both stores
// from the same on-disk layout (typically `<dataDir>/conversation.db`
// alongside `<dataDir>/app.db`).
type Config struct {
	DSN          string
	FallbackPath string
	Logger       gormlogger.Interface
}

// Store wraps a *gorm.DB and the resolved dialect. Callers should drive
// it through the typed methods on this struct (CreateThread, AppendEvent,
// etc.) instead of touching DB() directly outside of tests / migrations.
//
// The seqMu sync.Map serializes per-thread AppendEvent calls so a burst
// of concurrent inserts on the same thread can't race past the
// MAX(seq)+1 read inside the transaction. SQLite serializes writes anyway
// (single-writer + WAL), but the per-thread mutex keeps the ordering
// deterministic across both backends and dramatically reduces transient
// UNIQUE constraint retries on Postgres.
type Store struct {
	db     *gorm.DB
	driver string
	dsn    string

	// seqMu is a per-thread mutex map keyed by threadID. Lazily populated
	// by threadLock; idle entries are swept by a background goroutine to
	// prevent unbounded growth on long-running multi-tenant servers.
	seqMu    sync.Map // map[string]*threadMu
	stopSweep chan struct{}

	closeOnce sync.Once
	closeErr  error
}

// threadMu pairs a per-thread serialization mutex with a last-used
// timestamp so the sweep goroutine can evict idle entries.
type threadMu struct {
	mu       sync.Mutex
	lastUsed atomic.Int64 // UnixNano
}

// Open creates a Store using cfg, runs migrations, and returns it ready
// to use. Mirrors pkg/project.Open intentionally so the two stores look
// the same to integrators.
func Open(cfg Config) (*Store, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		if cfg.FallbackPath == "" {
			return nil, errors.New("conversation.Open: DSN and FallbackPath both empty")
		}
		dsn = cfg.FallbackPath
	}

	scheme, body, err := dialect.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("conversation.Open: %w", err)
	}

	// Ensure parent directory exists for sqlite file paths so first-run
	// users don't have to mkdir manually. :memory: and DSNs with embedded
	// query strings (`file.db?_pragma=...`) skip the mkdir for the query
	// suffix portion.
	if scheme == "sqlite" {
		path := body
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path = path[:i]
		}
		if path != ":memory:" && path != "" {
			if dir := filepath.Dir(path); dir != "" && dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return nil, fmt.Errorf("conversation.Open: create db dir: %w", err)
				}
			}
		}
	}

	d, err := dialect.Resolve(scheme)
	if err != nil {
		return nil, fmt.Errorf("conversation.Open: %w", err)
	}
	dialector, err := d.Open(body)
	if err != nil {
		return nil, fmt.Errorf("conversation.Open: dialect open: %w", err)
	}

	gormCfg := &gorm.Config{}
	if cfg.Logger != nil {
		gormCfg.Logger = cfg.Logger
	} else {
		gormCfg.Logger = gormlogger.Discard
	}

	db, err := gorm.Open(dialector, gormCfg)
	if err != nil {
		return nil, fmt.Errorf("conversation.Open: gorm.Open: %w", err)
	}

	// glebarez/sqlite ships with journal_mode=delete by default; flip to
	// WAL so concurrent readers (TUI list-thread refresh while a write is
	// in flight) don't block. Skipped on :memory: where WAL is a no-op
	// (and rejected by some sqlite builds). Errors are logged but not
	// fatal — degraded performance is preferable to hard-failing startup.
	if scheme == "sqlite" {
		applySQLitePragmas(db, body)
	}

	if err := runMigrations(db); err != nil {
		_ = closeUnderlying(db)
		return nil, fmt.Errorf("conversation.Open: migrate: %w", err)
	}

	st := &Store{db: db, driver: scheme, dsn: dsn, stopSweep: make(chan struct{})}
	go st.sweepLoop()
	return st, nil
}

// applySQLitePragmas sets WAL + busy_timeout on the open SQLite handle.
// Kept on a best-effort basis: if the user already pinned a journal_mode
// in their DSN, glebarez forwards it and our Exec gets ignored. If a
// pragma fails, the store still works — just with degraded concurrency
// characteristics.
func applySQLitePragmas(db *gorm.DB, body string) {
	if strings.HasPrefix(body, ":memory:") {
		return
	}
	_ = db.Exec("PRAGMA journal_mode = WAL").Error
	_ = db.Exec("PRAGMA busy_timeout = 5000").Error
	// Synchronous=NORMAL is safe under WAL (durable on commit, may lose
	// the last few txns on power-loss; fsync per-txn — full-mode — costs
	// 10×). Conversation history is not financial data; NORMAL is the
	// industry-standard tradeoff for app DBs.
	_ = db.Exec("PRAGMA synchronous = NORMAL").Error
	_ = db.Exec("PRAGMA foreign_keys = ON").Error
}

// DB exposes the underlying *gorm.DB for tests and the rare cross-table
// query that doesn't fit a typed method.
func (s *Store) DB() *gorm.DB { return s.db }

// Driver returns the resolved dialect name ("sqlite", "postgres", ...).
func (s *Store) Driver() string { return s.driver }

// Close releases the underlying DB connection. Safe to call multiple
// times; subsequent calls are no-ops.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		close(s.stopSweep)
		s.closeErr = closeUnderlying(s.db)
	})
	return s.closeErr
}

func closeUnderlying(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// threadLock acquires the per-thread mutex used to serialize seq
// assignment. Callers MUST call the returned release func.
func (s *Store) threadLock(threadID string) func() {
	v, _ := s.seqMu.LoadOrStore(threadID, &threadMu{})
	tm := v.(*threadMu)
	tm.lastUsed.Store(time.Now().UnixNano())
	tm.mu.Lock()
	return tm.mu.Unlock
}

// sweepLoop periodically evicts idle per-thread mutexes to prevent
// unbounded growth on long-running servers. Stops when stopSweep is closed.
func (s *Store) sweepLoop() {
	ticker := time.NewTicker(seqMuSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopSweep:
			return
		case <-ticker.C:
			s.sweepIdleLocks(seqMuMaxIdle)
		}
	}
}

// sweepIdleLocks removes per-thread mutexes that haven't been used within
// maxIdle. TryLock ensures we never evict a mutex that's currently held.
func (s *Store) sweepIdleLocks(maxIdle time.Duration) int {
	cutoff := time.Now().Add(-maxIdle).UnixNano()
	swept := 0
	s.seqMu.Range(func(key, val any) bool {
		tm := val.(*threadMu)
		if tm.lastUsed.Load() < cutoff {
			if tm.mu.TryLock() {
				s.seqMu.Delete(key)
				tm.mu.Unlock()
				swept++
			}
		}
		return true
	})
	return swept
}

// withCtx is a tiny helper that returns a *gorm.DB scoped to the caller
// context. Done as a helper so future tracing / logging hooks land in
// one place.
func (s *Store) withCtx(ctx context.Context) *gorm.DB {
	return s.db.WithContext(ctx)
}
