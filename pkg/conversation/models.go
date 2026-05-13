package conversation

import (
	"encoding/json"
	"time"
)

// Thread is a conversation container. ID is a UUID assigned at create
// time so threads created on different nodes never collide. ProjectID is
// load-bearing — every query filters by it to enforce multi-tenant
// isolation at the SQL layer (defense in depth on top of the application
// authz checks).
type Thread struct {
	ID          string `gorm:"primaryKey;type:text"`
	ProjectID   string `gorm:"type:text;not null;index"`
	OwnerUserID string `gorm:"type:text;not null;index"`
	Title       string `gorm:"type:text;not null;default:''"`
	// Client tags the entry point that created the thread. Helps debugging
	// ("which UI created this?") and lets ListThreads filter to a single
	// surface. Examples: "web", "openai", "cli".
	Client    string     `gorm:"type:text;not null;default:''"`
	CreatedAt time.Time  `gorm:"not null"`
	UpdatedAt time.Time  `gorm:"not null"`
	DeletedAt *time.Time `gorm:"index"` // soft delete
	// Metadata is opaque caller data. json.RawMessage + serializer:json
	// passes raw JSON bytes through unchanged (as opposed to plain []byte,
	// which the json serializer would base64-encode). Column type is JSONB
	// on Postgres and BLOB-affinity on SQLite. Nil writes NULL.
	Metadata json.RawMessage `gorm:"type:jsonb;serializer:json"`
}

// TableName pins the table name so future package renames (or vendoring)
// don't silently change the schema. Callers should never hit GORM's
// default name-from-type pluralization for a critical table.
func (Thread) TableName() string { return "threads" }

// Event is the append-only log row. ID is a DB-side autoincrement so
// inserts don't need a network round-trip for ID assignment. Seq is a
// thread-scoped monotonic counter assigned inside the same transaction
// as the insert (see store_event.go appendEventTx). UNIQUE(thread_id,
// seq) is the canonical ordering key for SSE backfill in P4.
//
// content_text holds streaming text chunks (assistant_text events) so
// the messages projection in P1 can concatenate without re-deserializing
// JSON. content_json holds structured payload (tool calls, tool results,
// content blocks). Both can be empty if the event is purely metadata
// (system, error).
type Event struct {
	ID         int64  `gorm:"primaryKey;autoIncrement"`
	ThreadID   string `gorm:"type:text;not null;uniqueIndex:idx_events_thread_seq,priority:1;index:idx_events_thread,priority:1"`
	ProjectID  string `gorm:"type:text;not null;index"`
	TurnID     string `gorm:"type:text;not null;index"`
	Seq        int64  `gorm:"not null;uniqueIndex:idx_events_thread_seq,priority:2"`
	Kind       string `gorm:"type:text;not null"`
	Role       string `gorm:"type:text;not null;default:''"`
	ContentText string          `gorm:"type:text;not null;default:''"`
	ContentJSON json.RawMessage `gorm:"type:jsonb;serializer:json"`
	BlobRefs    json.RawMessage `gorm:"type:jsonb;serializer:json"`
	CreatedAt   time.Time       `gorm:"not null"`
}

// TableName pins the events table name (see Thread.TableName note).
func (Event) TableName() string { return "events" }

// SchemaMigration tracks applied migration versions. Owned by this
// package; uses its own namespace in the conversation DSN.
type SchemaMigration struct {
	Version   int       `gorm:"primaryKey"`
	Name      string    `gorm:"type:text;not null"`
	AppliedAt time.Time `gorm:"not null"`
}

// TableName pins the schema_migrations table name.
func (SchemaMigration) TableName() string { return "schema_migrations" }

// Message is the materialized projection of a single LLM
// user/assistant/tool message. Built by the P1 projection inside the
// AppendEvent transaction:
//
//   - user / system / tool_result events → 1:1 INSERT of a new row
//   - assistant_text / assistant_tool_call events → UPSERT into the
//     single per-(thread,turn,'assistant','') row
//
// Pos is a thread-scoped monotonic counter (analogous to Event.Seq but
// independent of it) so the SSE backfill in P4 can use a single cursor
// over the projected log. UNIQUE(thread_id, pos) is enforced.
//
// idx_messages_proj_lookup is a NON-unique composite index used by
// upsertAssistantMessage to find the existing assistant row for a turn
// in O(log n) without forcing a 1-row-per-(turn,role,tool_call_id)
// constraint that would conflict with the events log's "any kind, any
// number, any order" semantics. Single-assistant-row-per-turn is
// guaranteed by the per-thread mutex + in-tx SELECT-then-UPSERT pattern
// in store_message.go, not the schema.
//
// content holds plain text for FTS5 indexing; tool_calls holds the
// optional assistant-emitted call envelopes (array of {id, name,
// arguments}). tool_call_id is set on tool messages to point back at
// the call they answer; empty otherwise.
type Message struct {
	ID         int64           `gorm:"primaryKey;autoIncrement"`
	ThreadID   string          `gorm:"type:text;not null;uniqueIndex:idx_messages_thread_pos,priority:1;index:idx_messages_proj_lookup,priority:1"`
	ProjectID  string          `gorm:"type:text;not null;index"`
	TurnID     string          `gorm:"type:text;not null;index:idx_messages_proj_lookup,priority:2"`
	Pos        int64           `gorm:"not null;uniqueIndex:idx_messages_thread_pos,priority:2"`
	Role       string          `gorm:"type:text;not null;index:idx_messages_proj_lookup,priority:3"`
	ToolCallID string          `gorm:"type:text;not null;default:'';index:idx_messages_proj_lookup,priority:4"`
	Content    string          `gorm:"type:text;not null;default:''"`
	ToolCalls  json.RawMessage `gorm:"type:jsonb;serializer:json"`
	CreatedAt  time.Time       `gorm:"not null"`
	UpdatedAt  time.Time       `gorm:"not null"`
}

// TableName pins the messages table name.
func (Message) TableName() string { return "messages" }

// TurnContext is the per-(thread, turn) cache-breakpoint snapshot used by
// P2 to persist LLM-prompt cache state. Saving a snapshot per turn lets a
// resumed thread skip rebuilding the entire prompt from the event log
// when the provider's prompt cache is still warm — the snapshot ships
// straight back to the provider as the cache key/anchor.
//
// Snapshot is opaque bytes (the gateway owns its format — typically a
// gob/CBOR/JSON-encoded bundle of cache_control breakpoints, hashed
// system prompt fingerprints, and the bounded turn's pre-rendered
// message slice). Metadata is structured side-channel data the gateway
// can interrogate without deserializing the snapshot (e.g. provider name,
// breakpoint count, token estimate) — useful for debugging cache misses
// without paying the full unmarshal cost.
//
// (thread_id, turn_id) is UNIQUE: writing twice for the same turn is an
// UPSERT (not a new row), so cache state evolves in place across the
// turn's streaming lifetime. The compound index also serves the
// GetTurnContext-by-thread-latest read via a covering scan on the
// (thread_id, updated_at DESC) idx_turn_contexts_thread_updated index.
type TurnContext struct {
	ID        int64           `gorm:"primaryKey;autoIncrement"`
	ThreadID  string          `gorm:"type:text;not null;uniqueIndex:idx_turn_contexts_thread_turn,priority:1;index:idx_turn_contexts_thread_updated,priority:1"`
	TurnID    string          `gorm:"type:text;not null;uniqueIndex:idx_turn_contexts_thread_turn,priority:2"`
	Snapshot  []byte          `gorm:"type:blob;not null"`
	Metadata  json.RawMessage `gorm:"type:jsonb;serializer:json"`
	CreatedAt time.Time       `gorm:"not null"`
	UpdatedAt time.Time       `gorm:"not null;index:idx_turn_contexts_thread_updated,priority:2,sort:desc"`
}

// TableName pins the turn_contexts table name.
func (TurnContext) TableName() string { return "turn_contexts" }

// Blob is a content-addressed binary payload — typically image
// attachments, file uploads, or large tool inputs/outputs that don't
// belong inline on event rows. Identity is the lowercase-hex sha256
// digest of Content; storing the same bytes twice is a single row by
// construction (PutBlob is idempotent on the digest).
//
// RefCount is the number of events whose blob_refs JSON array contains
// this digest. AppendEvent atomically increments inside the same tx as
// the event insert; GCBlobs reclaims rows where RefCount = 0 and the
// blob has aged past a caller-supplied threshold (the threshold protects
// against a put-before-event race window where a newly stored blob is
// momentarily unreferenced).
//
// Indices:
//   - PRIMARY KEY (sha256): point lookup by digest
//   - idx_blobs_gc_scan(ref_count, created_at): supports the GC sweep
//     query "WHERE ref_count = 0 AND created_at < ?" without a table
//     scan, even on tables with millions of live blobs.
type Blob struct {
	SHA256    string    `gorm:"primaryKey;type:text"`
	SizeBytes int64     `gorm:"not null"`
	Content   []byte    `gorm:"type:blob;not null"`
	RefCount  int       `gorm:"not null;default:0;index:idx_blobs_gc_scan,priority:1"`
	CreatedAt time.Time `gorm:"not null;index:idx_blobs_gc_scan,priority:2"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName pins the blobs table name.
func (Blob) TableName() string { return "blobs" }

// Turn tracks the lifecycle of a single LLM round-trip within a thread.
// Each turn starts with a user prompt and ends with the assistant's final
// response (or an error/cancellation). Callers create a turn via OpenTurn,
// attach events to its ID, and close it via CloseTurn.
type Turn struct {
	ID         string     `gorm:"primaryKey;type:text"`
	ThreadID   string     `gorm:"type:text;not null;index:idx_turns_thread"`
	UserID     string     `gorm:"type:text;not null;default:''"`
	Status     string     `gorm:"type:text;not null;default:'open'"`
	StartedAt  time.Time  `gorm:"not null"`
	FinishedAt *time.Time
}

// TableName pins the turns table name.
func (Turn) TableName() string { return "turns" }

// AllModels returns the GORM models managed by AutoMigrate. New models
// should be appended, never reordered — AutoMigrate is position-stable
// but tests assert presence by index in places.
func AllModels() []any {
	return []any{
		&Thread{},
		&Event{},
		&SchemaMigration{},
		&Message{},
		&TurnContext{},
		&Blob{},
		&Turn{},
	}
}
