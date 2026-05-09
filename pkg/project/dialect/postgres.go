//go:build postgres

// Build with `-tags postgres` to enable the PostgreSQL driver. Without the
// tag, gorm.io/driver/postgres is not imported and the binary stays small.
package dialect

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type postgresDialect struct{}

func (postgresDialect) Name() string { return "postgres" }

func (postgresDialect) Open(dsn string) (gorm.Dialector, error) {
	return postgres.Open(dsn), nil
}

func init() { Register(postgresDialect{}) }
