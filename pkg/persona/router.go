package persona

import (
	"path/filepath"
	"sort"
	"sync"
)

// RouteContext encapsulates request attributes for persona routing.
type RouteContext struct {
	Channels []string          // e.g. "discord:guild-123:channel-456"
	User     string            // sender ID
	Tags     map[string]string // request metadata
	Path     string            // HTTP path if applicable
}

// Router resolves which persona should handle a request based on channel bindings.
type Router struct {
	mu       sync.RWMutex
	bindings []ChannelBinding // sorted by priority descending
	fallback string
}

// NewRouter creates a router with the given bindings and fallback persona.
func NewRouter(bindings []ChannelBinding, fallback string) *Router {
	sorted := make([]ChannelBinding, len(bindings))
	copy(sorted, bindings)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority > sorted[j].Priority
	})
	return &Router{bindings: sorted, fallback: fallback}
}

// Resolve finds the best matching persona ID for the given context.
func (r *Router) Resolve(ctx RouteContext) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, b := range r.bindings {
		if b.matches(ctx) {
			return b.PersonaID
		}
	}
	return r.fallback
}

// Fallback returns the default persona ID.
func (r *Router) Fallback() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.fallback
}

// Update replaces bindings and fallback.
func (r *Router) Update(bindings []ChannelBinding, fallback string) {
	sorted := make([]ChannelBinding, len(bindings))
	copy(sorted, bindings)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority > sorted[j].Priority
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bindings = sorted
	r.fallback = fallback
}

func (b ChannelBinding) matches(ctx RouteContext) bool {
	for _, ch := range ctx.Channels {
		if matchGlob(b.Channel, ch) {
			if b.Peer == "" || b.Peer == ctx.User {
				return true
			}
		}
	}
	if ctx.Path != "" && matchGlob(b.Channel, ctx.Path) {
		if b.Peer == "" || b.Peer == ctx.User {
			return true
		}
	}
	return false
}

// matchGlob performs simple glob matching using filepath.Match semantics,
// with an extra convention: "foo:*" matches "foo:bar:baz" (star spans separators).
func matchGlob(pattern, value string) bool {
	if pattern == "" || value == "" {
		return false
	}
	// filepath.Match does not let * cross path separators, so we use : as separator.
	// For simple cases, try direct match first.
	if ok, _ := filepath.Match(pattern, value); ok {
		return true
	}
	// Fallback: treat trailing :* as "match any suffix".
	if len(pattern) >= 2 && pattern[len(pattern)-1] == '*' && pattern[len(pattern)-2] == ':' {
		prefix := pattern[:len(pattern)-1]
		if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
