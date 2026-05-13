package openai

import (
	"sync"
	"time"
)

// askAnswer carries the client's response to a paused ask_user_question tool call.
type askAnswer struct {
	Answers map[string]string
	Action  string // "accept" (default), "decline", "cancel"
}

// pendingAsk represents a single paused ask_user_question awaiting client response.
type pendingAsk struct {
	RunID      string
	SessionID  string
	TenantID   string
	ToolCallID string
	AnswerCh   chan askAnswer
	PauseCh    chan struct{}
	CreatedAt  time.Time
}

// pendingAskRegistry is a concurrency-safe registry of paused tool calls.
// Keyed by RunID (at most one pending ask per run at a time).
type pendingAskRegistry struct {
	mu    sync.Mutex
	byRun map[string]*pendingAsk
}

func newPendingAskRegistry() *pendingAskRegistry {
	return &pendingAskRegistry{byRun: make(map[string]*pendingAsk)}
}

// Register adds a pending ask to the registry.
func (r *pendingAskRegistry) Register(pa *pendingAsk) {
	r.mu.Lock()
	r.byRun[pa.RunID] = pa
	r.mu.Unlock()
}

// Lookup returns the pending ask for the given run ID, or nil.
func (r *pendingAskRegistry) Lookup(runID string) *pendingAsk {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byRun[runID]
}

// LookupBySession finds a pending ask matching the given session+tenant pair.
func (r *pendingAskRegistry) LookupBySession(sessionID, tenantID string) *pendingAsk {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, pa := range r.byRun {
		if pa.SessionID == sessionID && pa.TenantID == tenantID {
			return pa
		}
	}
	return nil
}

// Remove deletes the pending ask for the given run ID.
func (r *pendingAskRegistry) Remove(runID string) {
	r.mu.Lock()
	delete(r.byRun, runID)
	r.mu.Unlock()
}
