package conversation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Tests for the P2 turn_contexts persistence path. The schema's
// (thread_id, turn_id) UNIQUE index is what makes UPSERT semantics
// work — a regression to a non-unique index would silently grow the
// table by one row per chunk and TestPutTurnContext_UpsertOverwrites
// would catch it.

func TestPutTurnContext_RequiresFields(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	require.Error(t, s.PutTurnContext(ctx, "", "turn", []byte("snap"), nil))
	require.Error(t, s.PutTurnContext(ctx, "thread", "", []byte("snap"), nil))
	require.Error(t, s.PutTurnContext(ctx, "thread", "turn", nil, nil))
	require.Error(t, s.PutTurnContext(ctx, "thread", "turn", []byte{}, nil))
}

func TestPutTurnContext_InsertNewRow(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	snap := []byte("cache-state-v1")
	meta := json.RawMessage(`{"breakpoints":3}`)
	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-1", snap, meta))

	got, err := s.GetTurnContextByTurn(ctx, "thread-A", "turn-1")
	require.NoError(t, err)
	require.Equal(t, "thread-A", got.ThreadID)
	require.Equal(t, "turn-1", got.TurnID)
	require.Equal(t, snap, got.Snapshot)
	require.JSONEq(t, `{"breakpoints":3}`, string(got.Metadata))
	require.False(t, got.CreatedAt.IsZero())
	require.False(t, got.UpdatedAt.IsZero())
}

func TestPutTurnContext_UpsertOverwritesSnapshot(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-1",
		[]byte("first-snapshot"), json.RawMessage(`{"v":1}`)))
	first, err := s.GetTurnContextByTurn(ctx, "thread-A", "turn-1")
	require.NoError(t, err)
	originalCreated := first.CreatedAt
	originalID := first.ID

	// Force a measurable wall-clock gap so updated_at can advance even
	// on coarse-resolution clocks.
	time.Sleep(2 * time.Millisecond)

	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-1",
		[]byte("second-snapshot"), json.RawMessage(`{"v":2}`)))

	second, err := s.GetTurnContextByTurn(ctx, "thread-A", "turn-1")
	require.NoError(t, err)

	// Same row (UPSERT, not INSERT).
	require.Equal(t, originalID, second.ID, "UPSERT must reuse the row id")
	// Snapshot/metadata refreshed.
	require.Equal(t, []byte("second-snapshot"), second.Snapshot)
	require.JSONEq(t, `{"v":2}`, string(second.Metadata))
	// created_at preserved; updated_at advanced.
	require.True(t, second.CreatedAt.Equal(originalCreated),
		"created_at must be preserved on UPSERT (got=%v original=%v)", second.CreatedAt, originalCreated)
	require.True(t, second.UpdatedAt.After(first.UpdatedAt),
		"updated_at must advance on UPSERT")

	// Only one row exists for the (thread, turn) pair.
	var count int64
	require.NoError(t, s.DB().Model(&TurnContext{}).
		Where("thread_id = ? AND turn_id = ?", "thread-A", "turn-1").
		Count(&count).Error)
	require.EqualValues(t, 1, count, "UPSERT must not create duplicates")
}

func TestGetTurnContext_RequiresThreadID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.GetTurnContext(ctx, "")
	require.Error(t, err)
}

func TestGetTurnContext_NotFoundReturnsSentinel(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.GetTurnContext(ctx, "no-such-thread")
	require.ErrorIs(t, err, ErrTurnContextNotFound)
}

func TestGetTurnContext_ReturnsLatestAcrossTurns(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-1",
		[]byte("snap-1"), nil))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-2",
		[]byte("snap-2"), nil))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-3",
		[]byte("snap-3"), nil))

	got, err := s.GetTurnContext(ctx, "thread-A")
	require.NoError(t, err)
	require.Equal(t, "turn-3", got.TurnID, "latest by updated_at must win")
	require.Equal(t, []byte("snap-3"), got.Snapshot)
}

func TestGetTurnContext_LatestRespectsRewrites(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Write turn-1 first, then turn-2, then UPSERT turn-1 again — turn-1
	// should now be the "latest" because its updated_at moved forward.
	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-1",
		[]byte("snap-1"), nil))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-2",
		[]byte("snap-2"), nil))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-1",
		[]byte("snap-1-rewritten"), nil))

	got, err := s.GetTurnContext(ctx, "thread-A")
	require.NoError(t, err)
	require.Equal(t, "turn-1", got.TurnID)
	require.Equal(t, []byte("snap-1-rewritten"), got.Snapshot)
}

func TestGetTurnContextByTurn_PinpointsExactTurn(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-1",
		[]byte("snap-1"), nil))
	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-2",
		[]byte("snap-2"), nil))

	got, err := s.GetTurnContextByTurn(ctx, "thread-A", "turn-1")
	require.NoError(t, err)
	require.Equal(t, "turn-1", got.TurnID)
	require.Equal(t, []byte("snap-1"), got.Snapshot)
}

func TestGetTurnContextByTurn_NotFound(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.GetTurnContextByTurn(ctx, "thread-A", "missing")
	require.ErrorIs(t, err, ErrTurnContextNotFound)
}

func TestGetTurnContextByTurn_RequiresFields(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.GetTurnContextByTurn(ctx, "", "turn")
	require.Error(t, err)
	_, err = s.GetTurnContextByTurn(ctx, "thread", "")
	require.Error(t, err)
}

func TestPutTurnContext_NilMetadataWritesNull(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-1",
		[]byte("snap"), nil))

	got, err := s.GetTurnContextByTurn(ctx, "thread-A", "turn-1")
	require.NoError(t, err)
	require.Nil(t, got.Metadata, "nil metadata must round-trip as nil (not [] or {})")
}

func TestPutTurnContext_ThreadIsolation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Same turn ID across threads must NOT collide — the unique key is
	// (thread_id, turn_id), not turn_id alone. A regression that made
	// turn_id globally unique would surface here.
	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "shared-turn-id",
		[]byte("snap-A"), nil))
	require.NoError(t, s.PutTurnContext(ctx, "thread-B", "shared-turn-id",
		[]byte("snap-B"), nil))

	gotA, err := s.GetTurnContextByTurn(ctx, "thread-A", "shared-turn-id")
	require.NoError(t, err)
	require.Equal(t, []byte("snap-A"), gotA.Snapshot)

	gotB, err := s.GetTurnContextByTurn(ctx, "thread-B", "shared-turn-id")
	require.NoError(t, err)
	require.Equal(t, []byte("snap-B"), gotB.Snapshot)

	// GetTurnContext (latest by thread) also stays per-thread.
	latestA, err := s.GetTurnContext(ctx, "thread-A")
	require.NoError(t, err)
	require.Equal(t, []byte("snap-A"), latestA.Snapshot)
}

func TestPutTurnContext_LargeSnapshot(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// 256 KB blob — well within SQLite's BLOB capacity but large enough
	// to surface any silent truncation in the GORM serializer chain.
	big := make([]byte, 256*1024)
	for i := range big {
		big[i] = byte(i % 256)
	}
	require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-1", big, nil))

	got, err := s.GetTurnContextByTurn(ctx, "thread-A", "turn-1")
	require.NoError(t, err)
	require.Equal(t, big, got.Snapshot, "large snapshots must round-trip byte-for-byte")
}

func TestPutTurnContext_StreamingCheckpoint(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Simulate a streaming turn that checkpoints its cache state every
	// few chunks. After 100 checkpoints, exactly one row should exist.
	for i := 0; i < 100; i++ {
		snap := []byte{byte(i)}
		require.NoError(t, s.PutTurnContext(ctx, "thread-A", "turn-1", snap, nil))
	}

	var count int64
	require.NoError(t, s.DB().Model(&TurnContext{}).
		Where("thread_id = ? AND turn_id = ?", "thread-A", "turn-1").
		Count(&count).Error)
	require.EqualValues(t, 1, count, "100 checkpoints must collapse into one row")

	got, err := s.GetTurnContextByTurn(ctx, "thread-A", "turn-1")
	require.NoError(t, err)
	require.Equal(t, []byte{99}, got.Snapshot, "last write wins")
}
