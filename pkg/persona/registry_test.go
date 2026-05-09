package persona

import (
	"testing"
)

func TestRegistry_basic(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	if err := r.Register(Profile{ID: "aria", Name: "Aria", Emoji: "🌸"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Profile{ID: "coder", Name: "Coder"}); err != nil {
		t.Fatal(err)
	}

	if r.Len() != 2 {
		t.Errorf("Len = %d", r.Len())
	}

	ids := r.List()
	if len(ids) != 2 || ids[0] != "aria" || ids[1] != "coder" {
		t.Errorf("List = %v", ids)
	}

	p, ok := r.Get("aria")
	if !ok {
		t.Fatal("Get(aria) not found")
	}
	if p.Name != "Aria" || p.Emoji != "🌸" {
		t.Errorf("Get(aria) = %+v", p)
	}

	_, ok = r.Get("nonexistent")
	if ok {
		t.Error("Get(nonexistent) should return false")
	}
}

func TestRegistry_emptyID(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	err := r.Register(Profile{Name: "No ID"})
	if err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestRegistry_inheritance(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	r.Register(Profile{
		ID:           "_default",
		Model:        "claude-sonnet",
		Language:     "Chinese",
		EnabledTools: []string{"bash", "file_read"},
	})
	r.Register(Profile{
		ID:      "aria",
		Name:    "Aria",
		Emoji:   "🌸",
		Soul:    "Be creative.",
		Inherit: "_default",
	})

	p, ok := r.Get("aria")
	if !ok {
		t.Fatal("Get(aria) not found")
	}
	if p.Name != "Aria" {
		t.Errorf("Name = %q", p.Name)
	}
	if p.Model != "claude-sonnet" {
		t.Errorf("Model should be inherited: %q", p.Model)
	}
	if p.Language != "Chinese" {
		t.Errorf("Language should be inherited: %q", p.Language)
	}
	if p.Soul != "Be creative." {
		t.Errorf("Soul = %q", p.Soul)
	}
	if len(p.EnabledTools) != 2 {
		t.Errorf("EnabledTools should be inherited: %v", p.EnabledTools)
	}
}

func TestRegistry_deepInheritance(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	r.Register(Profile{ID: "base", Language: "English"})
	r.Register(Profile{ID: "mid", Model: "claude-sonnet", Inherit: "base"})
	r.Register(Profile{ID: "leaf", Name: "Leaf", Inherit: "mid"})

	p, ok := r.Get("leaf")
	if !ok {
		t.Fatal("Get(leaf) not found")
	}
	if p.Language != "English" {
		t.Errorf("Language = %q", p.Language)
	}
	if p.Model != "claude-sonnet" {
		t.Errorf("Model = %q", p.Model)
	}
	if p.Name != "Leaf" {
		t.Errorf("Name = %q", p.Name)
	}
}

func TestRegistry_cycleDetection(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	r.Register(Profile{ID: "a", Inherit: "b", Name: "A"})
	r.Register(Profile{ID: "b", Inherit: "a", Name: "B"})

	// Should not hang; returns whatever it can resolve.
	p, ok := r.Get("a")
	if !ok {
		t.Fatal("Get(a) should still return a profile")
	}
	if p.Name != "A" {
		t.Errorf("Name = %q", p.Name)
	}
}

func TestRegistry_delete(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(Profile{ID: "x", Name: "X"})
	r.Delete("x")
	if r.Len() != 0 {
		t.Errorf("Len after delete = %d", r.Len())
	}
	_, ok := r.Get("x")
	if ok {
		t.Error("Get after delete should fail")
	}
}

func TestRegistry_reload(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(Profile{ID: "old", Name: "Old"})

	r.Reload([]Profile{
		{ID: "new1", Name: "New1"},
		{ID: "new2", Name: "New2"},
	})
	if r.Len() != 2 {
		t.Errorf("Len = %d", r.Len())
	}
	_, ok := r.Get("old")
	if ok {
		t.Error("old profile should be gone after reload")
	}
}
