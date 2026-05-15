package server

import (
	"sync"
	"time"

	"github.com/saker-ai/saker/pkg/api"
)

// ActiveTurn represents a currently running agent turn.
type ActiveTurn struct {
	TurnID      string    `json:"turn_id"`
	ThreadID    string    `json:"thread_id"`
	ThreadTitle string    `json:"thread_title"`
	Prompt      string    `json:"prompt"`
	Status      string    `json:"status"` // "running" | "waiting"
	StartedAt   time.Time `json:"started_at"`
	Source      string    `json:"source"` // "user" | "cron"
	CronJobID   string    `json:"cron_job_id,omitempty"`
	StreamText  string    `json:"stream_text,omitempty"`
	ToolName    string    `json:"tool_name,omitempty"` // currently executing tool
}

// ActiveTurnTracker tracks all currently running agent turns.
type ActiveTurnTracker struct {
	mu    sync.RWMutex
	turns map[string]*activeTurnEntry
}

type activeTurnEntry struct {
	turn      ActiveTurn
	streamBuf []byte // accumulated stream text (capped)
	lastTool  string
}

const maxStreamBufLen = 500 // keep last N chars of stream text

// NewActiveTurnTracker creates a new tracker.
func NewActiveTurnTracker() *ActiveTurnTracker {
	return &ActiveTurnTracker{
		turns: make(map[string]*activeTurnEntry),
	}
}

// Register starts tracking a new turn.
func (t *ActiveTurnTracker) Register(turnID, threadID, threadTitle, prompt, source string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.turns[turnID] = &activeTurnEntry{
		turn: ActiveTurn{
			TurnID:      turnID,
			ThreadID:    threadID,
			ThreadTitle: threadTitle,
			Prompt:      truncateStr(prompt, 200),
			Status:      "running",
			StartedAt:   time.Now(),
			Source:      source,
		},
	}
}

// RegisterCron starts tracking a cron-triggered turn.
func (t *ActiveTurnTracker) RegisterCron(turnID, threadID, threadTitle, prompt, cronJobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.turns[turnID] = &activeTurnEntry{
		turn: ActiveTurn{
			TurnID:      turnID,
			ThreadID:    threadID,
			ThreadTitle: threadTitle,
			Prompt:      truncateStr(prompt, 200),
			Status:      "running",
			StartedAt:   time.Now(),
			Source:      "cron",
			CronJobID:   cronJobID,
		},
	}
}

// Unregister stops tracking a turn.
func (t *ActiveTurnTracker) Unregister(turnID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.turns, turnID)
}

// AppendStreamText adds streaming text to a turn's buffer.
func (t *ActiveTurnTracker) AppendStreamText(turnID, text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.turns[turnID]
	if !ok {
		return
	}
	e.streamBuf = append(e.streamBuf, []byte(text)...)
	if len(e.streamBuf) > maxStreamBufLen {
		e.streamBuf = e.streamBuf[len(e.streamBuf)-maxStreamBufLen:]
	}
}

// SetStatus updates the status of a turn.
func (t *ActiveTurnTracker) SetStatus(turnID, status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.turns[turnID]; ok {
		e.turn.Status = status
	}
}

// SetToolName updates the currently executing tool name.
func (t *ActiveTurnTracker) SetToolName(turnID, toolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.turns[turnID]; ok {
		e.lastTool = toolName
	}
}

// UpdateFromEvent processes a stream event to update turn state.
func (t *ActiveTurnTracker) UpdateFromEvent(turnID string, evt api.StreamEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.turns[turnID]
	if !ok {
		return
	}
	if evt.Delta != nil && evt.Delta.Text != "" {
		text := []byte(evt.Delta.Text)
		e.streamBuf = append(e.streamBuf, text...)
		if len(e.streamBuf) > maxStreamBufLen {
			e.streamBuf = e.streamBuf[len(e.streamBuf)-maxStreamBufLen:]
		}
	}
	if evt.Type == "tool_execution_start" {
		e.lastTool = evt.Name
	} else if evt.Type == "tool_execution_result" {
		e.lastTool = ""
	}
}

// List returns a snapshot of all active turns.
func (t *ActiveTurnTracker) List() []ActiveTurn {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]ActiveTurn, 0, len(t.turns))
	for _, e := range t.turns {
		turn := e.turn
		turn.StreamText = string(e.streamBuf)
		turn.ToolName = e.lastTool
		result = append(result, turn)
	}
	return result
}

// Count returns the number of active turns.
func (t *ActiveTurnTracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.turns)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
