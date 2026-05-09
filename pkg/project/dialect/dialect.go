// Package dialect provides a thin abstraction over GORM dialectors so the
// project store can switch between SQLite (default, embedded) and PostgreSQL
// (production) without leaking driver imports into business code.
//
// New dialects register themselves in init() so a build with `-tags postgres`
// transparently makes "postgres" available as a scheme.
package dialect

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"gorm.io/gorm"
)

// Dialect builds a GORM dialector from a DSN. Each implementation lives in a
// per-driver file (sqlite.go, postgres.go) and registers itself in init().
type Dialect interface {
	Name() string
	Open(dsn string) (gorm.Dialector, error)
}

var (
	mu       sync.RWMutex
	registry = map[string]Dialect{}
)

// Register adds a Dialect under its Name(). Called from driver init() blocks.
// Returns an error on duplicate registration so misconfigurations surface at startup.
func Register(d Dialect) error {
	mu.Lock()
	defer mu.Unlock()
	name := strings.ToLower(d.Name())
	if _, dup := registry[name]; dup {
		return fmt.Errorf("dialect: duplicate registration: %s", name)
	}
	registry[name] = d
	return nil
}

// ErrUnknownDialect is returned when a DSN scheme has no registered driver.
var ErrUnknownDialect = errors.New("dialect: unknown driver")

// Resolve picks a Dialect by name. The lookup is case-insensitive.
func Resolve(name string) (Dialect, error) {
	mu.RLock()
	defer mu.RUnlock()
	if d, ok := registry[strings.ToLower(name)]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("%w: %q (registered: %v)", ErrUnknownDialect, name, names())
}

func names() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// ParseDSN splits an saker DSN into (scheme, body). Accepted forms:
//
//	sqlite:///abs/path/app.db
//	sqlite://./relative/app.db
//	sqlite::memory:
//	file:/abs/path/app.db        (treated as sqlite)
//	postgres://user:pw@host/db   (postgres scheme passed through unchanged)
//
// Bare paths (no scheme, no "://") are treated as sqlite file paths so the
// common case "SAKER_DB_DSN=/var/saker/app.db" works without ceremony.
func ParseDSN(dsn string) (scheme, body string, err error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return "", "", errors.New("dialect: empty DSN")
	}
	// Bare path → sqlite.
	if !strings.Contains(dsn, "://") && !strings.HasPrefix(dsn, "sqlite:") && !strings.HasPrefix(dsn, "file:") {
		return "sqlite", dsn, nil
	}
	if strings.HasPrefix(dsn, "sqlite::memory:") {
		return "sqlite", ":memory:", nil
	}
	if strings.HasPrefix(dsn, "file:") {
		return "sqlite", strings.TrimPrefix(dsn, "file:"), nil
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "", "", fmt.Errorf("dialect: parse DSN: %w", err)
	}
	switch u.Scheme {
	case "sqlite", "sqlite3":
		// sqlite:///abs/path  → /abs/path
		// sqlite://./rel/path → ./rel/path
		body = u.Host + u.Path
		if u.RawQuery != "" {
			body += "?" + u.RawQuery
		}
		if body == "" {
			return "", "", errors.New("dialect: sqlite DSN missing path")
		}
		return "sqlite", body, nil
	case "postgres", "postgresql":
		// Pass the original DSN to lib/pq parser (don't strip scheme).
		return "postgres", dsn, nil
	}
	return "", "", fmt.Errorf("%w: scheme %q", ErrUnknownDialect, u.Scheme)
}
