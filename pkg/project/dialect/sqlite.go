package dialect

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// sqliteDialect wraps the CGO-free glebarez sqlite driver. It is registered
// unconditionally so sqlite is always available, even in builds without the
// `postgres` build tag.
type sqliteDialect struct{}

func (sqliteDialect) Name() string { return "sqlite" }

func (sqliteDialect) Open(dsn string) (gorm.Dialector, error) {
	return sqlite.Open(dsn), nil
}

func init() { Register(sqliteDialect{}) }
