package conversation_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/saker-ai/saker/pkg/conversation"
	"github.com/saker-ai/saker/pkg/server"
)

// External benchmark package (`conversation_test`) so the import cycle
// with pkg/server can never happen even if conversation later grows
// internal helpers that pull saker types — pkg/server already imports
// conversation, so this benchmark file deliberately stays at the
// outer test scope.
//
// Each benchmark prepares a fresh data dir and reports allocs/op so the
// hot-fixed SessionStore baseline is comparable apples-to-apples with
// the new conversation.Store.
//
// Targets (from the plan): the new store should be ≤ 5× the in-memory
// SessionStore on ListThreads and provide a stable AppendEvent baseline
// for P1 to regress against.

func benchOpenConversationStore(b *testing.B) *conversation.Store {
	b.Helper()
	dir := b.TempDir()
	s, err := conversation.Open(conversation.Config{FallbackPath: filepath.Join(dir, "conv.db")})
	if err != nil {
		b.Fatalf("open conversation store: %v", err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s
}

func benchOpenSessionStore(b *testing.B) *server.SessionStore {
	b.Helper()
	s, err := server.NewSessionStore()
	if err != nil {
		b.Fatalf("open session store: %v", err)
	}
	return s
}

func seedConversationThreads(b *testing.B, s *conversation.Store, n int) {
	b.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if _, err := s.CreateThread(ctx, "proj", "user", "t", "bench"); err != nil {
			b.Fatalf("seed thread %d: %v", i, err)
		}
	}
}

func seedSessionThreads(b *testing.B, s *server.SessionStore, n int) {
	b.Helper()
	for i := 0; i < n; i++ {
		s.CreateThread("t")
	}
}

func BenchmarkListThreads_N100_ConversationStore(b *testing.B) {
	s := benchOpenConversationStore(b)
	seedConversationThreads(b, s, 100)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.ListThreads(ctx, "proj", conversation.ListThreadsOpts{Limit: 100})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListThreads_N1000_ConversationStore(b *testing.B) {
	s := benchOpenConversationStore(b)
	seedConversationThreads(b, s, 1000)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.ListThreads(ctx, "proj", conversation.ListThreadsOpts{Limit: 1000})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListThreads_N100_SessionStore(b *testing.B) {
	s := benchOpenSessionStore(b)
	seedSessionThreads(b, s, 100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.ListThreads()
	}
}

func BenchmarkListThreads_N1000_SessionStore(b *testing.B) {
	s := benchOpenSessionStore(b)
	seedSessionThreads(b, s, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.ListThreads()
	}
}

// BenchmarkAppendEvent_ConversationStore is the P0 self-baseline for
// AppendEvent. P1 will introduce the messages projection worker; this
// number is the reference to regress against.
func BenchmarkAppendEvent_ConversationStore(b *testing.B) {
	s := benchOpenConversationStore(b)
	ctx := context.Background()
	th, err := s.CreateThread(ctx, "proj", "user", "bench-append", "bench")
	if err != nil {
		b.Fatal(err)
	}
	turnID, err := s.OpenTurn(ctx, th.ID, "")
	if err != nil {
		b.Fatal(err)
	}
	in := conversation.AppendEventInput{
		ThreadID:    th.ID,
		ProjectID:   "proj",
		TurnID:      turnID,
		Kind:        conversation.EventKindAssistantText,
		Role:        "assistant",
		ContentText: "the quick brown fox jumps over the lazy dog",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.AppendEvent(ctx, in); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAppendItem_SessionStore is the comparison baseline against
// the hot-fixed SessionStore.AppendItem (single-thread persist write,
// not the old O(N) version).
func BenchmarkAppendItem_SessionStore(b *testing.B) {
	s := benchOpenSessionStore(b)
	th := s.CreateThread("bench-append")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.AppendItem(th.ID, "assistant", "the quick brown fox jumps over the lazy dog", "turn-bench")
	}
}
