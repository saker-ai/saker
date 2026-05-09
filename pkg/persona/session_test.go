package persona

import "testing"

func TestScopedSessionID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		personaID string
		sessionID string
		want      string
	}{
		{"aria", "sess-1", "aria:sess-1"},
		{"", "sess-1", "sess-1"},
		{"default", "sess-1", "sess-1"},
		{"bot", "", "bot:"},
	}
	for _, tt := range tests {
		got := ScopedSessionID(tt.personaID, tt.sessionID)
		if got != tt.want {
			t.Errorf("ScopedSessionID(%q, %q) = %q, want %q", tt.personaID, tt.sessionID, got, tt.want)
		}
	}
}
