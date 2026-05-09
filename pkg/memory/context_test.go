package memory

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildContext_empty(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	ctx, err := store.BuildContext(10000)
	if err != nil {
		t.Fatal(err)
	}
	if ctx != "" {
		t.Errorf("expected empty context, got: %q", ctx)
	}
}

func TestBuildContext_withEntries(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	store.Save(Entry{Name: "test", Description: "A test memory", Type: MemoryTypeUser, Content: "test content"})

	ctx, err := store.BuildContext(10000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx, "Session Memory") {
		t.Error("context should contain header")
	}
	if !strings.Contains(ctx, "test") {
		t.Error("context should contain entry name")
	}
}

func TestBuildContext_truncation(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	for i := 0; i < 50; i++ {
		store.Save(Entry{
			Name:        fmt.Sprintf("entry_%d", i),
			Description: strings.Repeat("desc ", 20),
			Type:        MemoryTypeUser,
			Content:     "content",
		})
	}

	ctx, err := store.BuildContext(100) // very small budget: ~400 chars
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx) > 500 { // 100 tokens * 4 chars + header
		t.Errorf("context too large: %d chars", len(ctx))
	}
}

func TestBuildContext_noLimit(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	store.Save(Entry{Name: "a", Description: "desc", Type: MemoryTypeUser, Content: "c"})

	ctx, err := store.BuildContext(0) // no token limit
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx, "Session Memory") {
		t.Error("context should contain header")
	}
}
