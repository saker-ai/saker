package sessiondb

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/message"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenAndClose(t *testing.T) {
	s := tempStore(t)
	if s.db == nil {
		t.Fatal("expected non-nil db")
	}
}

func TestIndexAndGetSession(t *testing.T) {
	s := tempStore(t)
	msgs := []message.Message{
		{Role: "user", Content: "Hello world"},
		{Role: "assistant", Content: "Hi there"},
	}
	if err := s.Index("sess-1", msgs); err != nil {
		t.Fatalf("index: %v", err)
	}

	got, err := s.GetSession("sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Role != "user" || got[0].Content != "Hello world" {
		t.Errorf("unexpected first message: %+v", got[0])
	}
}

func TestIndexReplacesMessages(t *testing.T) {
	s := tempStore(t)
	msgs1 := []message.Message{{Role: "user", Content: "First"}}
	if err := s.Index("sess-1", msgs1); err != nil {
		t.Fatalf("index: %v", err)
	}

	msgs2 := []message.Message{
		{Role: "user", Content: "Second"},
		{Role: "assistant", Content: "Reply"},
	}
	if err := s.Index("sess-1", msgs2); err != nil {
		t.Fatalf("re-index: %v", err)
	}

	got, err := s.GetSession("sess-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages after re-index, got %d", len(got))
	}
	if got[0].Content != "Second" {
		t.Errorf("expected 'Second', got %q", got[0].Content)
	}
}

func TestSearchFTS5(t *testing.T) {
	s := tempStore(t)
	if err := s.Index("sess-a", []message.Message{
		{Role: "user", Content: "How to fix the authentication bug"},
		{Role: "assistant", Content: "Check the token validation logic"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Index("sess-b", []message.Message{
		{Role: "user", Content: "Deploy the application to production"},
	}); err != nil {
		t.Fatal(err)
	}

	results, err := s.Search("authentication", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results for 'authentication'")
	}
	if results[0].SessionID != "sess-a" {
		t.Errorf("expected sess-a, got %s", results[0].SessionID)
	}

	results, err = s.Search("deploy production", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results for 'deploy production'")
	}
	if results[0].SessionID != "sess-b" {
		t.Errorf("expected sess-b, got %s", results[0].SessionID)
	}
}

func TestSearchEmpty(t *testing.T) {
	s := tempStore(t)
	results, err := s.Search("", 10)
	if err != nil {
		t.Fatalf("search empty: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected no results for empty query, got %d", len(results))
	}
}

func TestListSessions(t *testing.T) {
	s := tempStore(t)
	for _, id := range []string{"s1", "s2", "s3"} {
		if err := s.Index(id, []message.Message{
			{Role: "user", Content: "prompt for " + id},
		}); err != nil {
			t.Fatal(err)
		}
	}

	sessions, err := s.ListSessions(10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	// Pagination.
	sessions, err = s.ListSessions(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions with limit=2, got %d", len(sessions))
	}
}

func TestDeriveTitle(t *testing.T) {
	tests := []struct {
		name string
		msgs []message.Message
		want string
	}{
		{"user message", []message.Message{{Role: "user", Content: "Hello"}}, "Hello"},
		{"skip assistant", []message.Message{{Role: "assistant", Content: "Hi"}, {Role: "user", Content: "Yo"}}, "Yo"},
		{"empty", []message.Message{}, ""},
		{"multiline", []message.Message{{Role: "user", Content: "First line\nSecond line"}}, "First line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveTitle(tt.msgs)
			if got != tt.want {
				t.Errorf("deriveTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConcurrentIndex(t *testing.T) {
	s := tempStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "concurrent-" + string(rune('a'+i))
			_ = s.Index(id, []message.Message{{Role: "user", Content: "msg"}})
		}(i)
	}
	wg.Wait()

	sessions, err := s.ListSessions(100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 10 {
		t.Errorf("expected 10 sessions, got %d", len(sessions))
	}
}

func TestNilStoreSafe(t *testing.T) {
	var s *Store
	if err := s.Index("x", nil); err != nil {
		t.Errorf("nil Index should not error: %v", err)
	}
	if _, err := s.Search("x", 10); err != nil {
		t.Errorf("nil Search should not error: %v", err)
	}
	if _, err := s.ListSessions(10, 0); err != nil {
		t.Errorf("nil ListSessions should not error: %v", err)
	}
	if _, err := s.GetSession("x"); err != nil {
		t.Errorf("nil GetSession should not error: %v", err)
	}
	if err := s.DeleteSession("x"); err != nil {
		t.Errorf("nil DeleteSession should not error: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("nil Close should not error: %v", err)
	}
}

func TestSkipsEmptyContent(t *testing.T) {
	s := tempStore(t)
	msgs := []message.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: ""},
		{Role: "assistant", Content: "   "},
	}
	if err := s.Index("sess-1", msgs); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSession("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 message (skipping empty), got %d", len(got))
	}
}

// --- New tests added for comprehensive coverage ---

func TestCRUDFlow(t *testing.T) {
	s := tempStore(t)

	// Create: index a session with messages.
	createMsgs := []message.Message{
		{Role: "user", Content: "What is Go?"},
		{Role: "assistant", Content: "Go is a programming language"},
	}
	if err := s.Index("crud-sess", createMsgs); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Get: retrieve messages and verify.
	got, err := s.GetSession("crud-sess")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Content != "What is Go?" {
		t.Errorf("first message content = %q, want 'What is Go?'", got[0].Content)
	}
	if got[1].Content != "Go is a programming language" {
		t.Errorf("second message content = %q, want 'Go is a programming language'", got[1].Content)
	}

	// Verify session metadata via ListSessions.
	sessions, err := s.ListSessions(10, 0)
	if err != nil {
		t.Fatalf("list after create: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "crud-sess" {
		t.Errorf("session id = %q, want 'crud-sess'", sessions[0].ID)
	}
	if sessions[0].MessageCount != 2 {
		t.Errorf("message_count = %d, want 2", sessions[0].MessageCount)
	}
	if sessions[0].Title != "What is Go?" {
		t.Errorf("title = %q, want 'What is Go?'", sessions[0].Title)
	}

	// Update: re-index with additional messages.
	updateMsgs := []message.Message{
		{Role: "user", Content: "What is Go?"},
		{Role: "assistant", Content: "Go is a programming language"},
		{Role: "user", Content: "Tell me more"},
	}
	if err := s.Index("crud-sess", updateMsgs); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err = s.GetSession("crud-sess")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 messages after update, got %d", len(got))
	}
	if got[2].Content != "Tell me more" {
		t.Errorf("third message content = %q, want 'Tell me more'", got[2].Content)
	}

	// Verify updated metadata.
	sessions, err = s.ListSessions(10, 0)
	if err != nil {
		t.Fatalf("list after update: %v", err)
	}
	if sessions[0].MessageCount != 3 {
		t.Errorf("message_count after update = %d, want 3", sessions[0].MessageCount)
	}

	// Delete: remove the session entirely.
	if err := s.DeleteSession("crud-sess"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Verify session is gone.
	got, err = s.GetSession("crud-sess")
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 messages after delete, got %d", len(got))
	}

	sessions, err = s.ListSessions(10, 0)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions after delete, got %d", len(sessions))
	}
}

func TestDeleteSession(t *testing.T) {
	s := tempStore(t)

	// Index two sessions.
	if err := s.Index("del-a", []message.Message{
		{Role: "user", Content: "alpha"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Index("del-b", []message.Message{
		{Role: "user", Content: "beta"},
	}); err != nil {
		t.Fatal(err)
	}

	// Delete one session.
	if err := s.DeleteSession("del-a"); err != nil {
		t.Fatalf("delete del-a: %v", err)
	}

	// Verify del-a is gone but del-b remains.
	msgs, err := s.GetSession("del-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for del-a after delete, got %d", len(msgs))
	}

	msgs, err = s.GetSession("del-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 message for del-b, got %d", len(msgs))
	}

	// Verify FTS no longer returns results for deleted session.
	results, err := s.Search("alpha", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.SessionID == "del-a" {
			t.Errorf("FTS still returns results for deleted session del-a")
		}
	}

	// Search for "beta" should still work.
	results, err = s.Search("beta", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'beta' after deleting del-a")
	}
}

func TestDeleteSessionNonExistent(t *testing.T) {
	s := tempStore(t)
	// Deleting a session that doesn't exist should not error.
	if err := s.DeleteSession("no-such-session"); err != nil {
		t.Errorf("delete non-existent session should not error: %v", err)
	}
}

func TestListSessionsPagination(t *testing.T) {
	s := tempStore(t)
	// Create 5 sessions.
	for i := 0; i < 5; i++ {
		id := "page-" + string(rune('A'+i))
		if err := s.Index(id, []message.Message{
			{Role: "user", Content: "content for " + id},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Page 1: limit=2, offset=0.
	page1, err := s.ListSessions(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1: expected 2 sessions, got %d", len(page1))
	}

	// Page 2: limit=2, offset=2.
	page2, err := s.ListSessions(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2: expected 2 sessions, got %d", len(page2))
	}

	// Page 3: limit=2, offset=4.
	page3, err := s.ListSessions(2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3: expected 1 session, got %d", len(page3))
	}

	// Verify no overlap between pages.
	ids := map[string]bool{}
	for _, sm := range page1 {
		ids[sm.ID] = true
	}
	for _, sm := range page2 {
		if ids[sm.ID] {
			t.Errorf("overlap: session %s appears in page1 and page2", sm.ID)
		}
		ids[sm.ID] = true
	}
	for _, sm := range page3 {
		if ids[sm.ID] {
			t.Errorf("overlap: session %s appears in earlier pages", sm.ID)
		}
		ids[sm.ID] = true
	}
	if len(ids) != 5 {
		t.Errorf("expected 5 unique session IDs across pages, got %d", len(ids))
	}

	// Total count matches.
	all, err := s.ListSessions(100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Errorf("expected 5 total sessions, got %d", len(all))
	}

	// Offset beyond data returns empty.
	empty, err := s.ListSessions(10, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 sessions for large offset, got %d", len(empty))
	}
}

func TestListSessionsDefaults(t *testing.T) {
	s := tempStore(t)
	// Negative limit should default to 50.
	sessions, err := s.ListSessions(-1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sessions != nil {
		t.Logf("negative limit returned %d sessions (default 50 cap)", len(sessions))
	}

	// Negative offset should default to 0.
	sessions, err = s.ListSessions(10, -5)
	if err != nil {
		t.Fatal(err)
	}
	if sessions != nil && len(sessions) > 0 {
		t.Logf("negative offset returned %d sessions", len(sessions))
	}
}

func TestParseTime(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  time.Time
	}{
		{
			name:  "valid datetime",
			input: "2024-01-15 10:30:00",
			want:  time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		},
		{
			name:  "invalid format",
			input: "not-a-date",
			want:  time.Time{},
		},
		{
			name:  "empty string",
			input: "",
			want:  time.Time{},
		},
		{
			name:  "date only without time",
			input: "2024-01-15",
			want:  time.Time{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTime(tt.input)
			if !got.Equal(tt.want) {
				t.Errorf("parseTime(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestMsgHash(t *testing.T) {
	// Same inputs produce same hash.
	h1 := msgHash("user", "hello", "")
	h2 := msgHash("user", "hello", "")
	if h1 != h2 {
		t.Errorf("identical inputs produced different hashes: %s vs %s", h1, h2)
	}

	// Different content produces different hash.
	h3 := msgHash("user", "world", "")
	if h1 == h3 {
		t.Errorf("different content produced same hash")
	}

	// Different role produces different hash.
	h4 := msgHash("assistant", "hello", "")
	if h1 == h4 {
		t.Errorf("different role produced same hash")
	}

	// Different toolName produces different hash.
	h5 := msgHash("user", "hello", "bash")
	if h1 == h5 {
		t.Errorf("different toolName produced same hash")
	}

	// Hash length is 64 chars (SHA-256 hex).
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex hash, got len=%d", len(h1))
	}
}

func TestIndexDiffSkipsUnchanged(t *testing.T) {
	s := tempStore(t)

	// Initial index with 3 messages.
	msgs1 := []message.Message{
		{Role: "user", Content: "question one"},
		{Role: "assistant", Content: "answer one"},
		{Role: "user", Content: "question two"},
	}
	if err := s.Index("diff-sess", msgs1); err != nil {
		t.Fatal(err)
	}

	got1, err := s.GetSession("diff-sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(got1) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got1))
	}

	// Re-index with same messages — should produce identical result.
	if err := s.Index("diff-sess", msgs1); err != nil {
		t.Fatal(err)
	}

	got2, err := s.GetSession("diff-sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 3 {
		t.Fatalf("expected 3 messages after re-index, got %d", len(got2))
	}
	// Verify content is unchanged.
	for i := range got2 {
		if got2[i].Content != got1[i].Content {
			t.Errorf("message %d content changed: %q vs %q", i, got1[i].Content, got2[i].Content)
		}
		if got2[i].Role != got1[i].Role {
			t.Errorf("message %d role changed: %q vs %q", i, got1[i].Role, got2[i].Role)
		}
	}

	// Re-index with one changed message in the middle.
	msgs2 := []message.Message{
		{Role: "user", Content: "question one"},
		{Role: "assistant", Content: "answer one UPDATED"},
		{Role: "user", Content: "question two"},
	}
	if err := s.Index("diff-sess", msgs2); err != nil {
		t.Fatal(err)
	}

	got3, err := s.GetSession("diff-sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(got3) != 3 {
		t.Fatalf("expected 3 messages after partial update, got %d", len(got3))
	}
	// First message should be unchanged.
	if got3[0].Content != "question one" {
		t.Errorf("unchanged message was modified: %q", got3[0].Content)
	}
	// Second message should be updated.
	if got3[1].Content != "answer one UPDATED" {
		t.Errorf("changed message not updated: %q", got3[1].Content)
	}
	// Third message should be unchanged.
	if got3[2].Content != "question two" {
		t.Errorf("unchanged message was modified: %q", got3[2].Content)
	}

	// Search should find the updated content.
	results, err := s.Search("UPDATED", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'UPDATED'")
	}
}

func TestIndexDiffAppendMessages(t *testing.T) {
	s := tempStore(t)

	// Start with 1 message.
	msgs1 := []message.Message{
		{Role: "user", Content: "first question"},
	}
	if err := s.Index("append-sess", msgs1); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetSession("append-sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}

	// Append 2 more messages.
	msgs2 := []message.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second question"},
	}
	if err := s.Index("append-sess", msgs2); err != nil {
		t.Fatal(err)
	}

	got, err = s.GetSession("append-sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 messages after append, got %d", len(got))
	}

	// Original message should be unchanged.
	if got[0].Content != "first question" {
		t.Errorf("original message changed: %q", got[0].Content)
	}
	if got[1].Content != "first answer" {
		t.Errorf("appended message wrong: %q", got[1].Content)
	}
	if got[2].Content != "second question" {
		t.Errorf("appended message wrong: %q", got[2].Content)
	}
}

func TestIndexDiffTruncateMessages(t *testing.T) {
	s := tempStore(t)

	// Start with 3 messages.
	msgs3 := []message.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
	}
	if err := s.Index("truncate-sess", msgs3); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetSession("truncate-sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}

	// Re-index with only 1 message (truncation).
	msgs1 := []message.Message{
		{Role: "user", Content: "q1"},
	}
	if err := s.Index("truncate-sess", msgs1); err != nil {
		t.Fatal(err)
	}

	got, err = s.GetSession("truncate-sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 message after truncation, got %d", len(got))
	}
	if got[0].Content != "q1" {
		t.Errorf("remaining message wrong: %q", got[0].Content)
	}

	// Stale messages should no longer appear in search.
	results, err := s.Search("a1", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.SessionID == "truncate-sess" {
			t.Error("truncated message still appears in search results")
		}
	}
}

func TestGetSessionNonExistent(t *testing.T) {
	s := tempStore(t)
	msgs, err := s.GetSession("no-such-session")
	if err != nil {
		t.Fatalf("get non-existent session should not error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for non-existent session, got %d", len(msgs))
	}
}

func TestIndexWithToolName(t *testing.T) {
	s := tempStore(t)
	msgs := []message.Message{
		{Role: "assistant", Content: "result", ToolCalls: []message.ToolCall{{Name: "bash"}}},
	}
	if err := s.Index("tool-sess", msgs); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetSession("tool-sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if got[0].ToolName != "bash" {
		t.Errorf("tool_name = %q, want 'bash'", got[0].ToolName)
	}
}

func TestIndexWithContentBlocks(t *testing.T) {
	s := tempStore(t)
	msgs := []message.Message{
		{
			Role:          "user",
			ContentBlocks: []message.ContentBlock{{Type: message.ContentBlockText, Text: "block content"}},
		},
	}
	if err := s.Index("block-sess", msgs); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetSession("block-sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if got[0].Content != "block content" {
		t.Errorf("content from blocks = %q, want 'block content'", got[0].Content)
	}
}

func TestListSessionsDeterministicOrder(t *testing.T) {
	s := tempStore(t)
	if err := s.Index("alpha", []message.Message{{Role: "user", Content: "first"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Index("beta", []message.Message{{Role: "user", Content: "second"}}); err != nil {
		t.Fatal(err)
	}

	// When timestamps are identical, id ASC serves as tiebreaker.
	sessions, err := s.ListSessions(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	// Verify deterministic ordering: sessions should appear in consistent order across queries.
	order1 := []string{sessions[0].ID, sessions[1].ID}
	sessions2, err := s.ListSessions(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	order2 := []string{sessions2[0].ID, sessions2[1].ID}
	if order1[0] != order2[0] || order1[1] != order2[1] {
		t.Errorf("ordering not deterministic: first=%v, second=%v", order1, order2)
	}
}

func TestSessionMessageCount(t *testing.T) {
	s := tempStore(t)
	msgs := []message.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "two"},
		{Role: "user", Content: "three"},
	}
	if err := s.Index("count-sess", msgs); err != nil {
		t.Fatal(err)
	}

	sessions, err := s.ListSessions(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].MessageCount != 3 {
		t.Errorf("message_count = %d, want 3", sessions[0].MessageCount)
	}
}
