package persona

import (
	"testing"
)

func TestBuildPromptSections_Nil(t *testing.T) {
	t.Parallel()
	sections := BuildPromptSections(nil, "")
	if sections != nil {
		t.Errorf("expected nil for nil profile, got %v", sections)
	}
}

func TestBuildPromptSections_FullProfile(t *testing.T) {
	t.Parallel()
	p := &Profile{
		Name:         "Aria",
		Description:  "A creative assistant",
		Emoji:        "🌟",
		Vibe:         "warm and playful",
		Soul:         "You are a warm AI.",
		Instructions: "Always be helpful.",
	}
	sections := BuildPromptSections(p, "")
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}
	// Identity
	if sections[0].Name != "persona.identity" {
		t.Errorf("section[0].Name = %q, want persona.identity", sections[0].Name)
	}
	if !sections[0].Cacheable {
		t.Error("identity section should be cacheable")
	}
	// Soul
	if sections[1].Name != "persona.soul" {
		t.Errorf("section[1].Name = %q, want persona.soul", sections[1].Name)
	}
	// Instructions
	if sections[2].Name != "persona.instructions" {
		t.Errorf("section[2].Name = %q, want persona.instructions", sections[2].Name)
	}
}

func TestBuildPromptSections_NoIdentity(t *testing.T) {
	t.Parallel()
	p := &Profile{Soul: "Just soul."}
	sections := BuildPromptSections(p, "")
	if len(sections) != 1 {
		t.Fatalf("expected 1 section (soul only), got %d", len(sections))
	}
	if sections[0].Name != "persona.soul" {
		t.Errorf("section[0].Name = %q, want persona.soul", sections[0].Name)
	}
}

func TestBuildPromptSections_Empty(t *testing.T) {
	t.Parallel()
	p := &Profile{ID: "empty"}
	sections := BuildPromptSections(p, "")
	if len(sections) != 0 {
		t.Errorf("expected 0 sections for empty profile, got %d", len(sections))
	}
}

func TestBuildIdentityBlock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		profile Profile
		want    string
	}{
		{
			name:    "empty",
			profile: Profile{},
			want:    "",
		},
		{
			name:    "name only",
			profile: Profile{Name: "Bot"},
			want:    "# Identity\n\nYou are Bot.",
		},
		{
			name:    "name and emoji",
			profile: Profile{Name: "Bot", Emoji: "🤖"},
			want:    "# Identity\n\nYou are 🤖 Bot.",
		},
		{
			name:    "name and description",
			profile: Profile{Name: "Bot", Description: "A helper"},
			want:    "# Identity\n\nYou are Bot — A helper.",
		},
		{
			name:    "name, emoji, vibe",
			profile: Profile{Name: "Bot", Emoji: "🤖", Vibe: "chill"},
			want:    "# Identity\n\nYou are 🤖 Bot.\nVibe: chill",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildIdentityBlock(&tt.profile)
			if got != tt.want {
				t.Errorf("buildIdentityBlock() = %q, want %q", got, tt.want)
			}
		})
	}
}
