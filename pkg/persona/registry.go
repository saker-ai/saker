package persona

import (
	"fmt"
	"sort"
	"sync"
)

const maxInheritDepth = 5

// Registry manages persona profiles with thread-safe access and inheritance resolution.
type Registry struct {
	mu            sync.RWMutex
	profiles      map[string]*Profile
	resolved      map[string]*Profile
	resolvedSouls map[string]cachedSoul // key: id + "\x00" + projectRoot
}

type cachedSoul struct {
	soul         string
	instructions string
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		profiles:      make(map[string]*Profile),
		resolved:      make(map[string]*Profile),
		resolvedSouls: make(map[string]cachedSoul),
	}
}

// Register adds or updates a persona profile. Clears the resolved cache.
func (r *Registry) Register(p Profile) error {
	if p.ID == "" {
		return fmt.Errorf("persona: profile ID is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profiles[p.ID] = &p
	r.resolved = make(map[string]*Profile)        // invalidate cache
	r.resolvedSouls = make(map[string]cachedSoul) // invalidate soul cache
	return nil
}

// Get returns a fully resolved profile (with inheritance applied).
func (r *Registry) Get(id string) (*Profile, bool) {
	r.mu.RLock()
	if cached, ok := r.resolved[id]; ok {
		r.mu.RUnlock()
		return cached, true
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock.
	if cached, ok := r.resolved[id]; ok {
		return cached, true
	}

	resolved, ok := r.resolve(id)
	if !ok {
		return nil, false
	}
	r.resolved[id] = resolved
	return resolved, true
}

// List returns all registered profile IDs sorted alphabetically.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.profiles))
	for id := range r.profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Profiles returns all registered profiles (unresolved).
func (r *Registry) Profiles() map[string]*Profile {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]*Profile, len(r.profiles))
	for k, v := range r.profiles {
		out[k] = v
	}
	return out
}

// Delete removes a persona and invalidates cache.
func (r *Registry) Delete(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.profiles, id)
	r.resolved = make(map[string]*Profile)
	r.resolvedSouls = make(map[string]cachedSoul)
}

// Reload replaces all profiles and clears cache.
func (r *Registry) Reload(profiles []Profile) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profiles = make(map[string]*Profile, len(profiles))
	for i := range profiles {
		r.profiles[profiles[i].ID] = &profiles[i]
	}
	r.resolved = make(map[string]*Profile)
	r.resolvedSouls = make(map[string]cachedSoul)
}

// ResolvedSoulCached returns the cached soul text for a persona, reading from
// disk only on the first call (or after cache invalidation).
func (r *Registry) ResolvedSoulCached(id, projectRoot string) (soul, instructions string) {
	key := id + "\x00" + projectRoot
	r.mu.RLock()
	if c, ok := r.resolvedSouls[key]; ok {
		r.mu.RUnlock()
		return c.soul, c.instructions
	}
	r.mu.RUnlock()

	// Resolve from profile.
	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after write lock.
	if c, ok := r.resolvedSouls[key]; ok {
		return c.soul, c.instructions
	}
	p, ok := r.resolved[id]
	if !ok {
		return "", ""
	}
	c := cachedSoul{
		soul:         p.ResolvedSoul(projectRoot),
		instructions: p.ResolvedInstructions(projectRoot),
	}
	r.resolvedSouls[key] = c
	return c.soul, c.instructions
}

// Len returns the number of registered profiles.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.profiles)
}

// resolve builds the inheritance chain and merges from root ancestor to leaf.
func (r *Registry) resolve(id string) (*Profile, bool) {
	chain := r.buildChain(id, maxInheritDepth)
	if len(chain) == 0 {
		return nil, false
	}
	result := &Profile{}
	// Merge from root ancestor to leaf (child overrides parent).
	for i := len(chain) - 1; i >= 0; i-- {
		mergeProfile(result, chain[i])
	}
	result.ID = id
	return result, true
}

// buildChain walks the Inherit chain from id up to the root, stopping at maxDepth.
func (r *Registry) buildChain(id string, maxDepth int) []*Profile {
	var chain []*Profile
	visited := make(map[string]bool)
	current := id
	for depth := 0; depth < maxDepth && current != ""; depth++ {
		if visited[current] {
			break // cycle detected
		}
		visited[current] = true
		p, ok := r.profiles[current]
		if !ok {
			break
		}
		chain = append(chain, p)
		current = p.Inherit
	}
	return chain
}
