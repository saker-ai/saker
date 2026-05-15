package middleware

import (
	"context"
	"sync"
	"testing"

	"github.com/saker-ai/saker/pkg/memory"
)

// fakeMemoryStore captures Save calls without touching the filesystem.
type fakeMemoryStore struct {
	mu      sync.Mutex
	entries []memory.Entry
}

func (f *fakeMemoryStore) saved() []memory.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]memory.Entry(nil), f.entries...)
}

// testMemoryNudge is a thin wrapper that drives the middleware OnAfterAgent
// callback directly, bypassing the full middleware chain.
type testMemoryNudge struct {
	afterAgent func(ctx context.Context, st *State) error
}

func setupNudge(store *memory.Store, every int) *testMemoryNudge {
	mw := NewMemoryNudge(MemoryNudgeConfig{
		Store:       store,
		EveryNTurns: every,
	})
	if mw == nil {
		return nil
	}
	f := mw.(Funcs)
	return &testMemoryNudge{afterAgent: f.OnAfterAgent}
}

func TestNewMemoryNudge_NilStore(t *testing.T) {
	mw := NewMemoryNudge(MemoryNudgeConfig{Store: nil})
	if mw != nil {
		t.Fatal("expected nil middleware for nil store")
	}
}

func TestContainsMemoryKeyword(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"I learned that Go maps are unordered", true},
		{"The fix was to add a mutex", true},
		{"Key insight: use channels", true},
		{"nothing special here", false},
		{"发现了一个bug", true},
		{"解决方案是重启", true},
		{"普通消息", false},
		{"IMPORTANT: do not skip tests", true},
		{"Root cause was nil pointer", true},
	}
	for _, tc := range cases {
		t.Run(tc.text[:min(len(tc.text), 30)], func(t *testing.T) {
			if got := containsMemoryKeyword(tc.text); got != tc.want {
				t.Errorf("containsMemoryKeyword(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestExtractMemoryEntries(t *testing.T) {
	text := `Some preamble text.
Key insight: always validate inputs before processing
Note: the API rate limit is 100 req/s
The fix was to add proper error handling in the retry loop
Random line with no pattern.
重要: 数据库连接需要设置超时`

	entries := extractMemoryEntries(text)
	if len(entries) < 3 {
		t.Fatalf("expected at least 3 entries, got %d: %+v", len(entries), entries)
	}

	// Verify types are assigned.
	for _, e := range entries {
		if e.Name == "" {
			t.Error("entry has empty name")
		}
		if e.Content == "" {
			t.Error("entry has empty content")
		}
		if e.Type == "" {
			t.Error("entry has empty type")
		}
	}
}

func TestExtractMemoryEntries_Dedup(t *testing.T) {
	text := "Key insight: use caching\nKey insight: use caching"
	entries := extractMemoryEntries(text)
	if len(entries) != 1 {
		t.Fatalf("expected 1 deduplicated entry, got %d", len(entries))
	}
}

func TestExtractMemoryEntries_EmptyLine(t *testing.T) {
	entries := extractMemoryEntries("")
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries for empty text, got %d", len(entries))
	}
}

func TestClassifyMemory(t *testing.T) {
	cases := []struct {
		content string
		want    memory.MemoryType
	}{
		{"fixed a bug in the parser", memory.MemoryTypeProject},
		{"deploy to production", memory.MemoryTypeProject},
		{"prefer tabs over spaces", memory.MemoryTypeFeedback},
		{"don't use global state", memory.MemoryTypeFeedback},
		{"always run tests first", memory.MemoryTypeFeedback},
		{"check the API docs at link", memory.MemoryTypeReference},
		{"some general knowledge", memory.MemoryTypeProject},
	}
	for _, tc := range cases {
		t.Run(tc.content[:min(len(tc.content), 25)], func(t *testing.T) {
			if got := classifyMemory(tc.content); got != tc.want {
				t.Errorf("classifyMemory(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"Hello World", 20, "hello_world"},
		{"  spaces  ", 20, "spaces"},
		{"CJK 测试文字", 20, "cjk_测试文字"},
		{"", 10, "memory"},
		{"a-b_c d", 5, "a_b_c"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := slugify(tc.input, tc.maxLen)
			if got != tc.want {
				t.Errorf("slugify(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("a long string here", 10); got != "a long ..." {
		t.Errorf("truncate long = %q", got)
	}
}

func TestExtractAgentOutput(t *testing.T) {
	cases := []struct {
		name string
		st   *State
		want string
	}{
		{"nil state", nil, ""},
		{"string model output", &State{ModelOutput: "hello"}, "hello"},
		{"map model output", &State{ModelOutput: map[string]any{"content": "world"}}, "world"},
		{"string agent", &State{Agent: "fallback"}, "fallback"},
		{"empty state", &State{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractAgentOutput(tc.st); got != tc.want {
				t.Errorf("extractAgentOutput = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMemoryNudge_PeriodicTrigger(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nudge := setupNudge(store, 2) // fire every 2 turns
	if nudge == nil {
		t.Fatal("expected non-nil nudge")
	}

	ctx := context.Background()

	// Turn 1: has keyword but not periodic — should still trigger via keyword
	st1 := &State{ModelOutput: "Key insight: testing is important"}
	if err := nudge.afterAgent(ctx, st1); err != nil {
		t.Fatal(err)
	}

	// Turn 2: periodic trigger, no keyword — needs content with a pattern
	st2 := &State{ModelOutput: "Note: always check errors"}
	if err := nudge.afterAgent(ctx, st2); err != nil {
		t.Fatal(err)
	}

	entries, _ := store.List()
	if len(entries) < 1 {
		t.Fatalf("expected at least 1 saved entry, got %d", len(entries))
	}
}

func TestMemoryNudge_NoTrigger(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nudge := setupNudge(store, 5)
	ctx := context.Background()

	// Turn 1: no keyword, not periodic
	st := &State{ModelOutput: "just a normal response with no patterns"}
	if err := nudge.afterAgent(ctx, st); err != nil {
		t.Fatal(err)
	}

	entries, _ := store.List()
	if len(entries) != 0 {
		t.Fatalf("expected 0 saved entries, got %d", len(entries))
	}
}

func TestMemoryNudge_EmptyOutput(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nudge := setupNudge(store, 1) // fire every turn
	ctx := context.Background()

	// Empty output should be skipped even on periodic trigger
	if err := nudge.afterAgent(ctx, &State{}); err != nil {
		t.Fatal(err)
	}

	entries, _ := store.List()
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries for empty output, got %d", len(entries))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
