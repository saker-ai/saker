// Package store provides the GORM-backed persistence layer for runhub.
//
// One Store wraps one *gorm.DB. The PersistentHub composes it with a
// MemoryHub and an event sink so producer Publish writes go through the
// in-memory ring AND the database, while reconnect / restart-replay
// queries fall back to the database when the ring has aged out.
//
// Two engines are supported via pkg/project/dialect:
//   - sqlite (default, single-process, embedded, no LISTEN/NOTIFY)
//   - postgres (multi-process, requires `-tags postgres` build, exposes Listen)
package store

import "time"

// RunRow is the persistent shape of a runhub.Run. Mirrors the in-memory
// fields needed for restart-replay; ring buffer contents are not persisted
// here — the source of truth for events lives in EventRow.
//
// Status is stored as the string form of runhub.RunStatus so the schema
// stays decoupled from the Go type's zero value (empty string would shadow
// "queued").
type RunRow struct {
	ID        string    `gorm:"primaryKey;size:64"`
	SessionID string    `gorm:"size:64;index"`
	TenantID  string    `gorm:"size:128;index"`
	Status    string    `gorm:"size:32;index"`
	CreatedAt time.Time `gorm:"index"`
	ExpiresAt time.Time
	UpdatedAt time.Time
}

// TableName namespaces the runhub tables so a Postgres install shared with
// pkg/project's store can never collide on a generic "runs" table.
func (RunRow) TableName() string { return "runhub_runs" }

// EventRow is one ring-buffer entry as it lands on disk. The composite
// primary key (RunID, Seq) lets range queries by run + seq hit the PK
// index, which is the hot path for SubscribeSince fall-back reads.
type EventRow struct {
	RunID  string    `gorm:"primaryKey;size:64"`
	Seq    int       `gorm:"primaryKey"`
	Type   string    `gorm:"size:32"`
	Data   []byte    `gorm:"type:bytes"`
	Stored time.Time `gorm:"index"`
}

func (EventRow) TableName() string { return "runhub_events" }

// AllModels enumerates the GORM models managed by this package. Open uses
// it for AutoMigrate; tests reuse it for cleanup.
func AllModels() []any {
	return []any{&RunRow{}, &EventRow{}}
}
