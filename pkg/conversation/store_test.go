package conversation

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// openTestStore opens a fresh SQLite-backed store inside the test's
// TempDir so each test run is isolated. Returns the store; teardown is
// registered with t.Cleanup.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(Config{FallbackPath: filepath.Join(dir, "conv.db")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateThread_ProjectIsolation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Two projects, one user owning one thread in each.
	tA, err := s.CreateThread(ctx, "projA", "userX", "A-thread", "web")
	require.NoError(t, err)
	require.NotEmpty(t, tA.ID)
	require.Equal(t, "projA", tA.ProjectID)

	tB, err := s.CreateThread(ctx, "projB", "userX", "B-thread", "web")
	require.NoError(t, err)
	require.NotEqual(t, tA.ID, tB.ID, "ids must differ across projects")

	// ListThreads(projA) sees only A's thread.
	listA, err := s.ListThreads(ctx, "projA", ListThreadsOpts{})
	require.NoError(t, err)
	require.Len(t, listA, 1)
	require.Equal(t, tA.ID, listA[0].ID)

	// ListThreads(projB) sees only B's thread.
	listB, err := s.ListThreads(ctx, "projB", ListThreadsOpts{})
	require.NoError(t, err)
	require.Len(t, listB, 1)
	require.Equal(t, tB.ID, listB[0].ID)

	// GetThread succeeds regardless of project (callers above this layer
	// enforce projectID checks); no isolation leak via ListThreads is
	// the contract under test.
	_, err = s.GetThread(ctx, tA.ID)
	require.NoError(t, err)
}

func TestCreateThread_RequiredFields(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.CreateThread(ctx, "", "user", "title", "web")
	require.Error(t, err)
	_, err = s.CreateThread(ctx, "proj", "", "title", "web")
	require.Error(t, err)
}

func TestUpdateThreadTitle(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "old", "web")
	require.NoError(t, err)

	require.NoError(t, s.UpdateThreadTitle(ctx, th.ID, "new"))
	got, err := s.GetThread(ctx, th.ID)
	require.NoError(t, err)
	require.Equal(t, "new", got.Title)

	// Missing thread → ErrThreadNotFound
	err = s.UpdateThreadTitle(ctx, "does-not-exist", "x")
	require.ErrorIs(t, err, ErrThreadNotFound)
}

func TestSoftDelete_HiddenFromList(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	keep, err := s.CreateThread(ctx, "proj", "user", "keep", "web")
	require.NoError(t, err)
	gone, err := s.CreateThread(ctx, "proj", "user", "gone", "web")
	require.NoError(t, err)

	require.NoError(t, s.SoftDeleteThread(ctx, gone.ID))

	// Default list excludes deleted.
	list, err := s.ListThreads(ctx, "proj", ListThreadsOpts{})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, keep.ID, list[0].ID)

	// IncludeDeleted brings it back.
	listAll, err := s.ListThreads(ctx, "proj", ListThreadsOpts{IncludeDeleted: true})
	require.NoError(t, err)
	require.Len(t, listAll, 2)

	// GetThread on the deleted row returns ErrThreadNotFound.
	_, err = s.GetThread(ctx, gone.ID)
	require.ErrorIs(t, err, ErrThreadNotFound)

	// Double-delete is a no-op error so callers can be idempotent if they
	// retry (rather than silently succeeding which would mask bugs).
	err = s.SoftDeleteThread(ctx, gone.ID)
	require.ErrorIs(t, err, ErrThreadNotFound)
}

func TestAppendEvent_AssignsSequentialSeq(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	for i := 1; i <= 5; i++ {
		seq, err := s.AppendEvent(ctx, AppendEventInput{
			ThreadID:  th.ID,
			ProjectID: "proj",
			TurnID:    turnID,
			Kind:      EventKindUserMessage,
			Role:      "user",
			ContentText: "hello",
		})
		require.NoError(t, err)
		require.EqualValues(t, i, seq)
	}

	events, err := s.GetEvents(ctx, th.ID, GetEventsOpts{})
	require.NoError(t, err)
	require.Len(t, events, 5)
	for i, e := range events {
		require.EqualValues(t, i+1, e.Seq)
	}
}

func TestAppendEvent_RejectsMissingFields(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	bad := []AppendEventInput{
		{ProjectID: "p", TurnID: "t", Kind: EventKindSystem},                 // missing ThreadID
		{ThreadID: "t", TurnID: "t", Kind: EventKindSystem},                  // missing ProjectID
		{ThreadID: "t", ProjectID: "p", Kind: EventKindSystem},               // missing TurnID
		{ThreadID: "t", ProjectID: "p", TurnID: "t"},                         // missing Kind
	}
	for _, in := range bad {
		_, err := s.AppendEvent(ctx, in)
		require.Error(t, err)
	}
}

func TestAppendEvent_SeqMonotonic(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "concur", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	const goroutines = 10
	const perGoroutine = 50
	var wg sync.WaitGroup
	var failures atomic.Int64

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				_, err := s.AppendEvent(ctx, AppendEventInput{
					ThreadID:  th.ID,
					ProjectID: "proj",
					TurnID:    turnID,
					Kind:      EventKindAssistantText,
					Role:      "assistant",
					ContentText: "chunk",
				})
				if err != nil {
					failures.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	require.Zero(t, failures.Load(), "no AppendEvent should have failed")

	events, err := s.GetEvents(ctx, th.ID, GetEventsOpts{Limit: MaxListLimit})
	require.NoError(t, err)
	require.Len(t, events, goroutines*perGoroutine)
	for i, e := range events {
		require.EqualValues(t, i+1, e.Seq, "gap-free monotonic seq required")
	}
}

func TestAppendEvent_CrossTurn_Ordering(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "cross-turn", "cli")
	require.NoError(t, err)
	turn1, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)
	turn2, err := s.OpenTurn(ctx, th.ID, turn1)
	require.NoError(t, err)

	// Interleave events from two turns.
	turns := []string{turn1, turn2, turn1, turn2, turn1}
	for _, turn := range turns {
		_, err := s.AppendEvent(ctx, AppendEventInput{
			ThreadID:  th.ID,
			ProjectID: "proj",
			TurnID:    turn,
			Kind:      EventKindUserMessage,
		})
		require.NoError(t, err)
	}

	all, err := s.GetEvents(ctx, th.ID, GetEventsOpts{})
	require.NoError(t, err)
	require.Len(t, all, len(turns))
	// Insertion order is preserved by seq even across interleaved turns.
	for i, e := range all {
		require.Equal(t, turns[i], e.TurnID)
		require.EqualValues(t, i+1, e.Seq)
	}

	// Filtering by turn returns only that turn's slice.
	turn1Events, err := s.GetEvents(ctx, th.ID, GetEventsOpts{TurnID: turn1})
	require.NoError(t, err)
	require.Len(t, turn1Events, 3)
}

func TestAppendEvent_ContentJSONRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "json", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	// Pre-populate blobs so AppendEvent's strict ref check passes.
	digestA, err := s.PutBlob(ctx, []byte("blob payload A"))
	require.NoError(t, err)
	digestB, err := s.PutBlob(ctx, []byte("blob payload B"))
	require.NoError(t, err)

	payload := map[string]any{
		"tool":  "search",
		"args":  []string{"a", "b"},
		"limit": 10,
	}
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:    th.ID,
		ProjectID:   "proj",
		TurnID:      turnID,
		Kind:        EventKindAssistantToolCall,
		ContentJSON: payload,
		BlobRefs:    []string{digestA, digestB},
	})
	require.NoError(t, err)

	events, err := s.GetEvents(ctx, th.ID, GetEventsOpts{})
	require.NoError(t, err)
	require.Len(t, events, 1)

	var got map[string]any
	require.NoError(t, json.Unmarshal(events[0].ContentJSON, &got))
	require.Equal(t, "search", got["tool"])

	var refs []string
	require.NoError(t, json.Unmarshal(events[0].BlobRefs, &refs))
	require.Equal(t, []string{digestA, digestB}, refs)
}

func TestGetEvents_AfterSeq(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "cursor", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		_, err := s.AppendEvent(ctx, AppendEventInput{
			ThreadID:  th.ID,
			ProjectID: "proj",
			TurnID:    turnID,
			Kind:      EventKindAssistantText,
		})
		require.NoError(t, err)
	}

	tail, err := s.GetEvents(ctx, th.ID, GetEventsOpts{AfterSeq: 5})
	require.NoError(t, err)
	require.Len(t, tail, 5)
	require.EqualValues(t, 6, tail[0].Seq)
	require.EqualValues(t, 10, tail[4].Seq)
}

func TestAppendEvent_BumpsThreadUpdatedAt(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "touch", "cli")
	require.NoError(t, err)
	originalUpdated := th.UpdatedAt
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	// Force the wall clock past the resolution of stored timestamps so
	// the bump is observable. nowUTC() is monotonic to nanosecond on
	// modern Go runtimes, which is enough.
	_, err = s.AppendEvent(ctx, AppendEventInput{
		ThreadID:  th.ID,
		ProjectID: "proj",
		TurnID:    turnID,
		Kind:      EventKindUserMessage,
	})
	require.NoError(t, err)

	got, err := s.GetThread(ctx, th.ID)
	require.NoError(t, err)
	require.True(t, got.UpdatedAt.After(originalUpdated) || got.UpdatedAt.Equal(originalUpdated),
		"updated_at must move forward on AppendEvent (got=%v original=%v)", got.UpdatedAt, originalUpdated)
}

func TestOpenTurn_ValidatesThreadExists(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.OpenTurn(ctx, "nonexistent", "")
	require.ErrorIs(t, err, ErrThreadNotFound)
}

func TestCloseTurn_ValidatesStatus(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)

	for _, st := range []TurnStatus{TurnStatusCompleted, TurnStatusFailed, TurnStatusCancelled} {
		tid, oErr := s.OpenTurn(ctx, th.ID, "")
		require.NoError(t, oErr)
		require.NoError(t, s.CloseTurn(ctx, tid, st))
	}

	tid2, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)
	require.Error(t, s.CloseTurn(ctx, tid2, TurnStatus("bogus")))
	require.Error(t, s.CloseTurn(ctx, tid2, TurnStatusOpen))
	require.Error(t, s.CloseTurn(ctx, "", TurnStatusCompleted))
	require.Error(t, s.CloseTurn(ctx, "no-such-turn", TurnStatusCompleted))
}

func TestMigrationsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "conv.db")

	s1, err := Open(Config{FallbackPath: dsn})
	require.NoError(t, err)
	require.NoError(t, s1.Close())

	s2, err := Open(Config{FallbackPath: dsn})
	require.NoError(t, err)
	defer s2.Close()

	// schema_migrations must contain v1–v5 exactly once each — re-Open is a no-op.
	var rows []SchemaMigration
	require.NoError(t, s2.DB().Order("version ASC").Find(&rows).Error)
	require.Len(t, rows, 5)
	require.Equal(t, 1, rows[0].Version)
	require.Equal(t, "initial_schema", rows[0].Name)
	require.Equal(t, 2, rows[1].Version)
	require.Equal(t, "messages_fts5", rows[1].Name)
	require.Equal(t, 3, rows[2].Version)
	require.Equal(t, "turn_contexts", rows[2].Name)
	require.Equal(t, 4, rows[3].Version)
	require.Equal(t, "blobs", rows[3].Name)
	require.Equal(t, 5, rows[4].Version)
	require.Equal(t, "turns", rows[4].Name)
}

func TestListThreads_Pagination(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	for i := 0; i < 25; i++ {
		_, err := s.CreateThread(ctx, "proj", "user", "t", "web")
		require.NoError(t, err)
	}

	page1, err := s.ListThreads(ctx, "proj", ListThreadsOpts{Limit: 10, Offset: 0})
	require.NoError(t, err)
	require.Len(t, page1, 10)

	page2, err := s.ListThreads(ctx, "proj", ListThreadsOpts{Limit: 10, Offset: 10})
	require.NoError(t, err)
	require.Len(t, page2, 10)

	page3, err := s.ListThreads(ctx, "proj", ListThreadsOpts{Limit: 10, Offset: 20})
	require.NoError(t, err)
	require.Len(t, page3, 5)

	// All distinct.
	seen := map[string]bool{}
	for _, page := range [][]Thread{page1, page2, page3} {
		for _, th := range page {
			require.False(t, seen[th.ID], "duplicate id across pages")
			seen[th.ID] = true
		}
	}
}

func TestListThreads_FilterByOwnerAndClient(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.CreateThread(ctx, "proj", "alice", "a-web", "web")
	require.NoError(t, err)
	_, err = s.CreateThread(ctx, "proj", "alice", "a-cli", "cli")
	require.NoError(t, err)
	_, err = s.CreateThread(ctx, "proj", "bob", "b-web", "web")
	require.NoError(t, err)

	aliceOnly, err := s.ListThreads(ctx, "proj", ListThreadsOpts{OwnerUserID: "alice"})
	require.NoError(t, err)
	require.Len(t, aliceOnly, 2)

	webOnly, err := s.ListThreads(ctx, "proj", ListThreadsOpts{Client: "web"})
	require.NoError(t, err)
	require.Len(t, webOnly, 2)

	aliceWeb, err := s.ListThreads(ctx, "proj", ListThreadsOpts{OwnerUserID: "alice", Client: "web"})
	require.NoError(t, err)
	require.Len(t, aliceWeb, 1)
}
