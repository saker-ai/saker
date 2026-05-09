package persona

import (
	"testing"
)

func TestRouter_resolve(t *testing.T) {
	t.Parallel()
	r := NewRouter([]ChannelBinding{
		{Channel: "discord:guild-123:poetry-*", PersonaID: "aria", Priority: 20},
		{Channel: "discord:guild-123:*", PersonaID: "coder", Priority: 10},
		{Channel: "telegram:*", PersonaID: "aria", Priority: 10},
	}, "coder")

	tests := []struct {
		name     string
		ctx      RouteContext
		expected string
	}{
		{
			name:     "poetry channel matches aria",
			ctx:      RouteContext{Channels: []string{"discord:guild-123:poetry-haiku"}},
			expected: "aria",
		},
		{
			name:     "general discord matches coder",
			ctx:      RouteContext{Channels: []string{"discord:guild-123:general"}},
			expected: "coder",
		},
		{
			name:     "telegram matches aria",
			ctx:      RouteContext{Channels: []string{"telegram:group-456"}},
			expected: "aria",
		},
		{
			name:     "no match returns fallback",
			ctx:      RouteContext{Channels: []string{"slack:channel-1"}},
			expected: "coder",
		},
		{
			name:     "empty channels returns fallback",
			ctx:      RouteContext{},
			expected: "coder",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Resolve(tt.ctx)
			if got != tt.expected {
				t.Errorf("Resolve = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestRouter_peerFilter(t *testing.T) {
	t.Parallel()
	r := NewRouter([]ChannelBinding{
		{Channel: "discord:*", Peer: "user-vip", PersonaID: "vip-bot", Priority: 30},
		{Channel: "discord:*", PersonaID: "default-bot", Priority: 10},
	}, "fallback")

	got := r.Resolve(RouteContext{Channels: []string{"discord:ch1"}, User: "user-vip"})
	if got != "vip-bot" {
		t.Errorf("VIP user should match vip-bot: %q", got)
	}

	got = r.Resolve(RouteContext{Channels: []string{"discord:ch1"}, User: "user-regular"})
	if got != "default-bot" {
		t.Errorf("Regular user should match default-bot: %q", got)
	}
}

func TestRouter_update(t *testing.T) {
	t.Parallel()
	r := NewRouter(nil, "old")
	if r.Fallback() != "old" {
		t.Errorf("Fallback = %q", r.Fallback())
	}

	r.Update([]ChannelBinding{
		{Channel: "http:*", PersonaID: "web"},
	}, "new")

	if r.Fallback() != "new" {
		t.Errorf("Fallback after update = %q", r.Fallback())
	}
	got := r.Resolve(RouteContext{Channels: []string{"http:api"}})
	if got != "web" {
		t.Errorf("Resolve after update = %q", got)
	}
}

func TestMatchGlob(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern, value string
		expected       bool
	}{
		{"discord:*", "discord:channel", true},
		{"discord:*", "discord:guild:channel", true},
		{"telegram:group-456", "telegram:group-456", true},
		{"telegram:group-456", "telegram:group-789", false},
		{"http:/api/*", "http:/api/chat", true},
		{"", "anything", false},
		{"pattern", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.value, func(t *testing.T) {
			got := matchGlob(tt.pattern, tt.value)
			if got != tt.expected {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.expected)
			}
		})
	}
}
