package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/saker-ai/saker/pkg/project/dialect"
)

// Config controls how Store is opened.
type Config struct {
	// DSN follows dialect.ParseDSN. Empty is rejected — caller must guard
	// before calling Open (PersistentHub only opens a Store when the
	// gateway operator configures Options.RunHubDSN).
	DSN string
	// Logger is forwarded to GORM. nil → discard, matching pkg/project.
	Logger gormlogger.Interface
	// OnListenerReconnect is fired by the shared LISTEN pool every time
	// it attempts to reconnect after a dropped pgx connection. Lets the
	// caller observe reconnect health without coupling the store to a
	// concrete metrics package. Optional.
	OnListenerReconnect func(success bool)
	// PGCopyThreshold gates the postgres-only COPY-based bulk insert
	// path. When the runtime driver is postgres AND a single
	// InsertEventsBatch carries at least this many rows, the call is
	// served via pgx.CopyFrom into a TEMP staging table followed by an
	// INSERT ... SELECT ... ON CONFLICT DO NOTHING (preserves the
	// existing dedup semantics that vanilla CopyFrom can't express).
	// Smaller batches stay on the multi-row INSERT path because the
	// temp-table tx overhead dominates below ~50 rows. Zero disables
	// the COPY fast-path entirely (every batch goes through the
	// prepared multi-row INSERT). Ignored on non-postgres drivers.
	PGCopyThreshold int
}

// Store wraps a *gorm.DB plus the resolved driver name.
type Store struct {
	db     *gorm.DB
	driver string
	dsn    string

	// onReconnect is the callback supplied by the operator (Config field)
	// and consumed by the postgres listen pool. Stored on the Store so
	// the pool can be lazily constructed without re-plumbing the Config.
	onReconnect func(success bool)

	// pgCopyThreshold mirrors Config.PGCopyThreshold so the always-built
	// repo.go routing decision can read it without touching Config (which
	// is consumed-and-discarded by Open). Zero disables the COPY path.
	pgCopyThreshold int

	// listenState carries any build-tagged listener state (currently the
	// shared postgres LISTEN pool). Declared as `any` so this file stays
	// compileable in both `-tags postgres` and the default build; the
	// concrete shape is owned by the build-tagged listen_pool_*.go files.
	listenMu    sync.Mutex
	listenState any

	// pgxState carries the lazily-constructed *pgxpool.Pool used by the
	// COPY-based batch-insert path. Declared as `any` so this file
	// compiles in both `-tags postgres` and the default build; the
	// concrete shape is owned by repo_postgres.go. Constructed on first
	// large batch, torn down by Store.Close.
	pgxMu    sync.Mutex
	pgxState any

	closeOnce sync.Once
	closeErr  error
}

// sqlitePragmas is appended to a sqlite DSN that doesn't already carry
// PRAGMAs. WAL gives concurrent readers + a writer (matches the
// producer/many-subscribers split runhub generates); busy_timeout absorbs
// short contention bursts; foreign_keys is enabled defensively even though
// the schema declares no FKs today.
const sqlitePragmas = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

// Open creates a Store, runs AutoMigrate for AllModels, and returns it
// ready for use. SQLite DSNs get the runhub PRAGMA defaults appended
// automatically when the operator hasn't supplied any.
func Open(cfg Config) (*Store, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, errors.New("runhub/store: DSN is required")
	}

	scheme, body, err := dialect.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("runhub/store: %w", err)
	}

	if scheme == "sqlite" {
		if body != ":memory:" {
			if dir := filepath.Dir(body); dir != "" && dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return nil, fmt.Errorf("runhub/store: create db dir: %w", err)
				}
			}
		}
		body = withSQLitePragmas(body)
	}

	d, err := dialect.Resolve(scheme)
	if err != nil {
		return nil, fmt.Errorf("runhub/store: %w", err)
	}
	dialector, err := d.Open(body)
	if err != nil {
		return nil, fmt.Errorf("runhub/store: dialect open: %w", err)
	}

	// PrepareStmt enables GORM's per-DB prepared-statement cache so the
	// hot-path InsertEventsBatch / LoadEventsSince queries (re-issued
	// thousands of times per minute under burst load) skip the
	// parse + plan steps after the first call. The handwritten
	// insertEventsBatchPrepared below also benefits — pgx / sqlite see
	// the SQL once, plan once, then reuse the plan keyed on the
	// statement text. Bench gain on SQLite: ~2× throughput on
	// InsertEventsBatch.
	gormCfg := &gorm.Config{PrepareStmt: true}
	if cfg.Logger != nil {
		gormCfg.Logger = cfg.Logger
	} else {
		gormCfg.Logger = gormlogger.Discard
	}

	db, err := gorm.Open(dialector, gormCfg)
	if err != nil {
		return nil, fmt.Errorf("runhub/store: gorm.Open: %w", err)
	}
	if err := db.AutoMigrate(AllModels()...); err != nil {
		return nil, fmt.Errorf("runhub/store: auto-migrate: %w", err)
	}

	return &Store{
		db:              db,
		driver:          scheme,
		dsn:             dsn,
		onReconnect:     cfg.OnListenerReconnect,
		pgCopyThreshold: cfg.PGCopyThreshold,
	}, nil
}

// withSQLitePragmas appends the runhub PRAGMA defaults to a sqlite DSN
// only when none are present, so an operator-supplied PRAGMA wins.
func withSQLitePragmas(body string) string {
	if strings.Contains(body, "_pragma=") {
		return body
	}
	if strings.Contains(body, "?") {
		return body + "&" + sqlitePragmas
	}
	return body + "?" + sqlitePragmas
}

// DB returns the underlying *gorm.DB. Use repo methods first; this is for
// tests and the rare cross-table query that doesn't fit a repo method.
func (s *Store) DB() *gorm.DB { return s.db }

// Driver returns the resolved dialect name ("sqlite", "postgres").
func (s *Store) Driver() string { return s.driver }

// DSN returns the raw DSN this store was opened with.
func (s *Store) DSN() string { return s.dsn }

// Close releases the underlying connection. If a postgres LISTEN pool
// or pgxpool COPY pool was constructed, both are shut down before the
// gorm pool so in-flight reader/writer goroutines exit before the
// connections they depend on go away. Idempotent; subsequent calls
// return the cached error from the first close.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.poolShutdown()
		s.pgxPoolShutdown()
		sqlDB, err := s.db.DB()
		if err != nil {
			s.closeErr = err
			return
		}
		s.closeErr = sqlDB.Close()
	})
	return s.closeErr
}
