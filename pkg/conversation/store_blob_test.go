package conversation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Tests for the P3 blob CAS path. The integration test
// TestAppendEvent_ContentJSONRoundTrip in store_test.go also exercises
// the AppendEvent → ref_count increment chain; the tests below focus on
// blob-only invariants and the GC sweep.

func TestPutBlob_ReturnsExpectedDigest(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	data := []byte("hello world")
	digest, err := s.PutBlob(ctx, data)
	require.NoError(t, err)

	// Recompute on the test side so a regression in sha256Hex (e.g.
	// uppercase hex, base64) gets caught here, not silently in
	// production.
	expected := sha256.Sum256(data)
	require.Equal(t, hex.EncodeToString(expected[:]), digest)
	require.Len(t, digest, 64, "sha256 hex must be 64 chars")
}

func TestPutBlob_RequiresData(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.PutBlob(ctx, nil)
	require.Error(t, err)
	_, err = s.PutBlob(ctx, []byte{})
	require.Error(t, err)
}

func TestPutBlob_DedupOnSameContent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	data := []byte("dedup me")
	d1, err := s.PutBlob(ctx, data)
	require.NoError(t, err)
	d2, err := s.PutBlob(ctx, data)
	require.NoError(t, err)
	require.Equal(t, d1, d2, "same content must yield same digest")

	// Exactly one row in the table for this digest.
	var count int64
	require.NoError(t, s.DB().Model(&Blob{}).Where("sha256 = ?", d1).Count(&count).Error)
	require.EqualValues(t, 1, count, "double PutBlob must dedup, not insert twice")
}

func TestPutBlob_DifferentContentDifferentDigest(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	d1, err := s.PutBlob(ctx, []byte("aaa"))
	require.NoError(t, err)
	d2, err := s.PutBlob(ctx, []byte("bbb"))
	require.NoError(t, err)
	require.NotEqual(t, d1, d2)
}

func TestGetBlob_RoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	original := []byte("get me back")
	digest, err := s.PutBlob(ctx, original)
	require.NoError(t, err)

	got, err := s.GetBlob(ctx, digest)
	require.NoError(t, err)
	require.Equal(t, original, got)
}

func TestGetBlob_NotFoundReturnsSentinel(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.GetBlob(ctx, "nonexistent-digest")
	require.ErrorIs(t, err, ErrBlobNotFound)
}

func TestGetBlob_RequiresDigest(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.GetBlob(ctx, "")
	require.Error(t, err)
}

func TestGetBlobMeta_ReturnsRefCountWithoutContent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	digest, err := s.PutBlob(ctx, []byte("payload"))
	require.NoError(t, err)

	meta, err := s.GetBlobMeta(ctx, digest)
	require.NoError(t, err)
	require.Equal(t, digest, meta.SHA256)
	require.EqualValues(t, len("payload"), meta.SizeBytes)
	require.EqualValues(t, 0, meta.RefCount)
	require.Empty(t, meta.Content, "Meta variant must not load Content")
}

func TestGetBlobMeta_NotFound(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.GetBlobMeta(ctx, "missing")
	require.ErrorIs(t, err, ErrBlobNotFound)
}

func TestAppendEvent_BlobRefIncrementsCount(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	digest, err := s.PutBlob(ctx, []byte("attachment"))
	require.NoError(t, err)

	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:  th.ID,
		ProjectID: "proj",
		TurnID:    turnID,
		Kind:      EventKindUserMessage,
		BlobRefs:  []string{digest},
	})
	require.NoError(t, err)

	meta, err := s.GetBlobMeta(ctx, digest)
	require.NoError(t, err)
	require.EqualValues(t, 1, meta.RefCount, "ref_count must bump to 1 after one ref")
}

func TestAppendEvent_MultipleEventsAccumulateRefCount(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	digest, err := s.PutBlob(ctx, []byte("popular blob"))
	require.NoError(t, err)

	const N = 5
	for i := 0; i < N; i++ {
		_, err := s.AppendEvent(ctx, AppendEventInput{
			ThreadID:  th.ID,
			ProjectID: "proj",
			TurnID:    turnID,
			Kind:      EventKindUserMessage,
			BlobRefs:  []string{digest},
		})
		require.NoError(t, err)
	}

	meta, err := s.GetBlobMeta(ctx, digest)
	require.NoError(t, err)
	require.EqualValues(t, N, meta.RefCount)
}

func TestAppendEvent_DuplicateRefsCountSeparately(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	digest, err := s.PutBlob(ctx, []byte("twice"))
	require.NoError(t, err)

	// Same digest twice in one event → ref_count += 2. Documents the
	// "no implicit dedup" contract; if the contract changes, this test
	// flips and signals a behavior shift.
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:  th.ID,
		ProjectID: "proj",
		TurnID:    turnID,
		Kind:      EventKindUserMessage,
		BlobRefs:  []string{digest, digest},
	})
	require.NoError(t, err)

	meta, err := s.GetBlobMeta(ctx, digest)
	require.NoError(t, err)
	require.EqualValues(t, 2, meta.RefCount)
}

func TestAppendEvent_MissingBlobRefRollsBackTx(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:  th.ID,
		ProjectID: "proj",
		TurnID:    turnID,
		Kind:      EventKindUserMessage,
		BlobRefs:  []string{"deadbeef-not-a-real-digest"},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrBlobMissingForRef)

	// No event row should have been persisted (whole tx rolled back).
	events, err := s.GetEvents(ctx, th.ID, GetEventsOpts{})
	require.NoError(t, err)
	require.Empty(t, events, "missing blob ref must roll back the entire tx")
}

func TestAppendEvent_PartiallyMissingRefsRollsBackEverything(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	good, err := s.PutBlob(ctx, []byte("real blob"))
	require.NoError(t, err)

	// One real ref + one bogus ref. Whole tx must roll back, including
	// the good ref's increment — otherwise future GCs would over-keep
	// blobs based on phantom counts.
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:  th.ID,
		ProjectID: "proj",
		TurnID:    turnID,
		Kind:      EventKindUserMessage,
		BlobRefs:  []string{good, "phantom"},
	})
	require.ErrorIs(t, err, ErrBlobMissingForRef)

	meta, err := s.GetBlobMeta(ctx, good)
	require.NoError(t, err)
	require.EqualValues(t, 0, meta.RefCount,
		"partial-failure tx must NOT leak ref_count increments on the surviving blob")
}

func TestGCBlobs_DeletesUnreferencedAndOld(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	digest, err := s.PutBlob(ctx, []byte("orphan"))
	require.NoError(t, err)

	// Pass olderThan=0 so the freshly-inserted blob counts as "old enough".
	deleted, err := s.GCBlobs(ctx, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)

	_, err = s.GetBlob(ctx, digest)
	require.ErrorIs(t, err, ErrBlobNotFound)
}

func TestGCBlobs_KeepsReferencedBlobs(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	live, err := s.PutBlob(ctx, []byte("alive"))
	require.NoError(t, err)
	dead, err := s.PutBlob(ctx, []byte("dead"))
	require.NoError(t, err)

	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:  th.ID,
		ProjectID: "proj",
		TurnID:    turnID,
		Kind:      EventKindUserMessage,
		BlobRefs:  []string{live},
	})
	require.NoError(t, err)

	deleted, err := s.GCBlobs(ctx, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted, "only the unreferenced blob must be reclaimed")

	// Live still readable.
	_, err = s.GetBlob(ctx, live)
	require.NoError(t, err)
	// Dead gone.
	_, err = s.GetBlob(ctx, dead)
	require.ErrorIs(t, err, ErrBlobNotFound)
}

func TestGCBlobs_KeepsTooNewOrphans(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	digest, err := s.PutBlob(ctx, []byte("freshly orphaned"))
	require.NoError(t, err)

	// One-hour age guard — the just-PutBlob row is way younger than that
	// and must be kept for the put-before-event race window.
	deleted, err := s.GCBlobs(ctx, time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 0, deleted, "orphans inside the age guard must be kept")

	_, err = s.GetBlob(ctx, digest)
	require.NoError(t, err, "guarded orphan must still be readable")
}

func TestGCBlobs_RejectsNegativeWindow(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.GCBlobs(ctx, -time.Second)
	require.Error(t, err)
}

func TestGCBlobs_NoOpWhenEmpty(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	deleted, err := s.GCBlobs(ctx, 0)
	require.NoError(t, err)
	require.EqualValues(t, 0, deleted)
}

func TestGCBlobs_BulkSweep(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Mix of orphans and a referenced blob.
	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	const orphanCount = 20
	for i := 0; i < orphanCount; i++ {
		_, err := s.PutBlob(ctx, []byte{byte(i)})
		require.NoError(t, err)
	}
	live, err := s.PutBlob(ctx, []byte("kept"))
	require.NoError(t, err)
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:  th.ID,
		ProjectID: "proj",
		TurnID:    turnID,
		Kind:      EventKindUserMessage,
		BlobRefs:  []string{live},
	})
	require.NoError(t, err)

	deleted, err := s.GCBlobs(ctx, 0)
	require.NoError(t, err)
	require.EqualValues(t, orphanCount, deleted)

	var remaining int64
	require.NoError(t, s.DB().Model(&Blob{}).Count(&remaining).Error)
	require.EqualValues(t, 1, remaining, "only the live blob must survive")
}
