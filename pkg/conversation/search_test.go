package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Tests for the FTS5 Search path (P1). Every test here relies on the
// AI/AD/AU triggers wired up in migration v2 to keep messages_fts in
// sync with messages — there's no manual rebuild step. If the triggers
// were broken, these tests would fail because the FTS table would be
// empty.

func seedThreadWithMessages(t *testing.T, s *Store, projectID, ownerUserID string, msgs []string) string {
	t.Helper()
	ctx := context.Background()

	th, err := s.CreateThread(ctx, projectID, ownerUserID, "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)
	for _, m := range msgs {
		_, err := s.AppendEvent(ctx, AppendEventInput{
			ThreadID:    th.ID,
			ProjectID:   projectID,
			TurnID:      turnID,
			Kind:        EventKindUserMessage,
			ContentText: m,
		})
		require.NoError(t, err)
	}
	return th.ID
}

func TestSearch_FindsMatchingMessage(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	threadID := seedThreadWithMessages(t, s, "proj", "user", []string{
		"the quick brown fox",
		"hello world",
		"jumps over the lazy dog",
	})

	hits, err := s.Search(ctx, "proj", "fox", SearchOpts{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, threadID, hits[0].ThreadID)
	require.Contains(t, strings.ToLower(hits[0].Snippet), "fox")
	require.Greater(t, hits[0].Score, 0.0, "negated bm25 should be positive")
}

func TestSearch_PrefixQuery(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	seedThreadWithMessages(t, s, "proj", "user", []string{
		"the quick brown fox",
		"jumping high",
		"jumpsuit",
	})

	// FTS5 prefix syntax (jump*) should match jumping and jumpsuit.
	hits, err := s.Search(ctx, "proj", "jump*", SearchOpts{})
	require.NoError(t, err)
	require.Len(t, hits, 2)
}

func TestSearch_ProjectIsolation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Same content in two projects. Use a single token (no hyphens or
	// operator characters) so FTS5 doesn't reinterpret the query.
	seedThreadWithMessages(t, s, "projA", "user", []string{"uniquecontentmarker"})
	seedThreadWithMessages(t, s, "projB", "user", []string{"uniquecontentmarker"})

	hitsA, err := s.Search(ctx, "projA", "uniquecontentmarker", SearchOpts{})
	require.NoError(t, err)
	require.Len(t, hitsA, 1, "projA query must not see projB's matching message")

	hitsB, err := s.Search(ctx, "projB", "uniquecontentmarker", SearchOpts{})
	require.NoError(t, err)
	require.Len(t, hitsB, 1, "projB query must not see projA's matching message")

	require.NotEqual(t, hitsA[0].ThreadID, hitsB[0].ThreadID)
}

func TestSearch_ThreadFilter(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Two threads in same project, same matching content.
	t1 := seedThreadWithMessages(t, s, "proj", "user", []string{"sharedmarker thread one"})
	t2 := seedThreadWithMessages(t, s, "proj", "user", []string{"sharedmarker thread two"})

	all, err := s.Search(ctx, "proj", "sharedmarker", SearchOpts{})
	require.NoError(t, err)
	require.Len(t, all, 2)

	onlyT1, err := s.Search(ctx, "proj", "sharedmarker", SearchOpts{ThreadID: t1})
	require.NoError(t, err)
	require.Len(t, onlyT1, 1)
	require.Equal(t, t1, onlyT1[0].ThreadID)
	_ = t2
}

func TestSearch_AssistantStreamingIndexed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	th, err := s.CreateThread(ctx, "proj", "user", "t", "cli")
	require.NoError(t, err)
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	require.NoError(t, err)

	// Stream three chunks into one assistant message. The AU trigger
	// must keep messages_fts in sync after each UPDATE.
	for _, c := range []string{"the answer ", "is forty ", "two"} {
		_, err := s.AppendEvent(ctx, AppendEventInput{
			ThreadID:    th.ID,
			ProjectID:   "proj",
			TurnID:      turnID,
			Kind:        EventKindAssistantText,
			ContentText: c,
		})
		require.NoError(t, err)
	}

	hits, err := s.Search(ctx, "proj", "forty", SearchOpts{})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "assistant", hits[0].Role)
	require.Contains(t, hits[0].Snippet, "forty")
}

func TestSearch_NoMatches(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	seedThreadWithMessages(t, s, "proj", "user", []string{"hello world"})

	hits, err := s.Search(ctx, "proj", "xyznotpresent", SearchOpts{})
	require.NoError(t, err)
	require.Empty(t, hits)
}

func TestSearch_RequiresFields(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.Search(ctx, "", "q", SearchOpts{})
	require.Error(t, err)
	_, err = s.Search(ctx, "proj", "", SearchOpts{})
	require.Error(t, err)
}

func TestSearch_RankingOrdersBetterMatchFirst(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Two messages, one with the query term twice (should rank higher).
	seedThreadWithMessages(t, s, "proj", "user", []string{
		"banana split with toppings",
		"banana banana banana",
	})

	hits, err := s.Search(ctx, "proj", "banana", SearchOpts{})
	require.NoError(t, err)
	require.Len(t, hits, 2)
	// Higher score = better match (we negate bm25). The triple-banana row
	// must come first.
	require.Greater(t, hits[0].Score, hits[1].Score)
	require.Contains(t, hits[0].Snippet, "banana banana")
}
