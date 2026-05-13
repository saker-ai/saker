// Package conversation provides the unified conversation store for saker.
//
// P0 establishes the data layer and Store interface skeleton. Three tables
// land in this phase — threads, events, schema_migrations. The remaining
// reads-side projections (messages / FTS), turn_contexts, and blob CAS
// arrive in P1, P2, P3 respectively without requiring schema-breaking
// changes (see .docs/conversation-store-v2.md §11 evolution chapter).
//
// Stub interface methods that belong to later phases (Search, GetMessages,
// GetTurnContext, PutBlob) panic with a clear "not implemented in P0"
// message. Callers must check the package phase before invoking them.
package conversation

import "time"

// EventKind enumerates the recognized event types stored in the events
// table. The string is persisted verbatim, so renaming a kind is a
// breaking schema change — add new kinds, never rewrite existing ones.
type EventKind string

const (
	// EventKindUserMessage is a user-authored prompt (text content lands in
	// content_text; structured parts in content_json).
	EventKindUserMessage EventKind = "user_message"
	// EventKindAssistantText is a streamed assistant text chunk. Multiple
	// chunks may share a turn_id and are concatenated by the messages
	// projection in P1.
	EventKindAssistantText EventKind = "assistant_text"
	// EventKindAssistantToolCall is a single assistant-emitted tool call
	// envelope (id, name, arguments json).
	EventKindAssistantToolCall EventKind = "assistant_tool_call"
	// EventKindToolResult is a single tool execution result tied to a
	// previously-emitted tool call by tool_call_id (in content_json).
	EventKindToolResult EventKind = "tool_result"
	// EventKindSystem captures a system-issued message (system prompt,
	// instruction injection, gateway-side rewrite).
	EventKindSystem EventKind = "system"
	// EventKindUsage records token usage for the bounded turn.
	EventKindUsage EventKind = "usage"
	// EventKindError records a turn-level error (saker runtime, provider,
	// validation). Failed turns still flush their accumulated content
	// chunks before this event.
	EventKindError EventKind = "error"
)

// TurnStatus describes the terminal state of a turn. P0 only persists
// "open" / "completed" via OpenTurn / CloseTurn; P1+ adds the turns table
// where these values land in a real column.
type TurnStatus string

const (
	TurnStatusOpen      TurnStatus = "open"
	TurnStatusCompleted TurnStatus = "completed"
	TurnStatusCancelled TurnStatus = "cancelled"
	TurnStatusFailed    TurnStatus = "failed"
)

// ListThreadsOpts filters and paginates ListThreads. All fields are
// optional; the zero value returns the most recently updated threads up
// to a sane built-in cap.
type ListThreadsOpts struct {
	// OwnerUserID restricts results to a single owner. Empty = any owner
	// inside the project.
	OwnerUserID string
	// Client filters by the client-of-origin tag ("web", "openai", "cli").
	// Empty = any client.
	Client string
	// Limit caps the row count. 0 → DefaultListLimit. Negative is rejected.
	Limit int
	// Offset skips this many leading rows for cursor pagination. P0 uses
	// offset; P1+ may switch to keyset on (updated_at, id) once row counts
	// justify it.
	Offset int
	// IncludeDeleted controls whether soft-deleted rows are returned.
	// Default false.
	IncludeDeleted bool
}

// GetEventsOpts filters and paginates GetEvents. Events are always
// returned in seq-ascending order so callers can resume from a known
// cursor without re-sorting.
type GetEventsOpts struct {
	// AfterSeq returns only events with seq strictly greater than this
	// value. Use 0 to fetch from the beginning.
	AfterSeq int64
	// TurnID restricts to events of a single turn. Empty = all turns.
	TurnID string
	// Limit caps the row count. 0 → DefaultListLimit.
	Limit int
}

// AppendEventInput is the canonical input shape for AppendEvent. The
// store assigns id, seq, and created_at — the caller supplies semantic
// fields only.
type AppendEventInput struct {
	ThreadID    string
	ProjectID   string
	TurnID      string
	Kind        EventKind
	Role        string
	ContentText string
	// ContentJSON is opaque structured payload (tool args, tool result,
	// usage breakdown, content blocks). The store marshals via GORM's
	// json serializer so SQLite stores BLOB and Postgres stores JSONB.
	ContentJSON any
	// BlobRefs is a list of sha256 digests this event depends on. Foreign
	// keys land in P3 with the blob CAS table.
	BlobRefs []string
	// ToolCallID links a tool_result event back to the assistant tool
	// call it answers. Required when Kind = EventKindToolResult so the
	// P1 projection can attach the result to the right message. Ignored
	// for other kinds.
	ToolCallID string
	// ToolCallName is the assistant tool name being invoked. Used by the
	// P1 projection when materializing assistant messages with tool_calls.
	// Optional; ContentJSON typically carries the full call envelope too.
	ToolCallName string
}

// GetMessagesOpts filters and paginates GetMessages.
type GetMessagesOpts struct {
	// AfterPos returns only messages with pos strictly greater than this
	// value. 0 = from the start.
	AfterPos int64
	// Limit caps the row count. 0 → DefaultListLimit.
	Limit int
}

// SearchOpts narrows a Search query.
type SearchOpts struct {
	// ThreadID restricts hits to a single thread. Empty = all threads in
	// the project.
	ThreadID string
	// Limit caps the hit count. 0 → DefaultListLimit.
	Limit int
}

// SearchHit is a single FTS match. Snippet is a UI-ready highlighted
// excerpt; callers can re-render it themselves by re-fetching the
// message and applying their own highlighting if richer markup is
// needed.
type SearchHit struct {
	ThreadID  string
	MessageID int64
	Pos       int64
	Role      string
	Snippet   string
	Score     float64
}

// DefaultListLimit is the cap applied when ListThreadsOpts.Limit /
// GetEventsOpts.Limit is zero. Matches a TUI screenful comfortably and
// keeps a bad caller from accidentally fetching millions of rows.
const DefaultListLimit = 100

// MaxListLimit is the hard upper bound enforced regardless of caller
// input. Prevents accidental table-scans masquerading as a "high limit".
const MaxListLimit = 1000

// clampLimit normalizes a caller-provided limit against the defaults.
func clampLimit(in int) int {
	if in <= 0 {
		return DefaultListLimit
	}
	if in > MaxListLimit {
		return MaxListLimit
	}
	return in
}

// nowUTC is the time source for created_at / updated_at. Centralized so
// tests can substitute a fake clock if needed (none in P0).
var nowUTC = func() time.Time { return time.Now().UTC() }
