package skills

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

const maxHistorySize = 500

// SkillActivationRecord captures a single skill activation event.
type SkillActivationRecord struct {
	Skill      string    `json:"skill"`
	Scope      string    `json:"scope"`
	Source     string    `json:"source"`
	Score      float64   `json:"score"`
	SessionID  string    `json:"session_id"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
	ToolCalls  int       `json:"tool_calls"`
	DurationMs int64     `json:"duration_ms"`
	TokenUsage int       `json:"token_usage"`
	Timestamp  time.Time `json:"timestamp"`
}

// SkillStats holds aggregated statistics for a single skill.
type SkillStats struct {
	Name            string         `json:"name"`
	Scope           string         `json:"scope"`
	ActivationCount int            `json:"activation_count"`
	SuccessCount    int            `json:"success_count"`
	FailCount       int            `json:"fail_count"`
	LastUsed        string         `json:"last_used"`
	AvgDurationMs   float64        `json:"avg_duration_ms"`
	TotalTokens     int            `json:"total_tokens"`
	BySource        map[string]int `json:"by_source"`
}

// SkillAnalytics is the persistent file structure.
type SkillAnalytics struct {
	Skills  map[string]*SkillStats  `json:"skills"`
	History []SkillActivationRecord `json:"history"`
}

// SkillTracker records skill activation metrics and persists them to disk.
type SkillTracker struct {
	mu    sync.Mutex
	data  *SkillAnalytics
	path  string
	dirty bool
}

// NewSkillTracker creates a tracker, loading existing data from path if present.
func NewSkillTracker(path string) *SkillTracker {
	t := &SkillTracker{
		path: path,
		data: &SkillAnalytics{
			Skills:  make(map[string]*SkillStats),
			History: nil,
		},
	}
	if raw, err := os.ReadFile(path); err == nil {
		var loaded SkillAnalytics
		if json.Unmarshal(raw, &loaded) == nil {
			if loaded.Skills != nil {
				t.data = &loaded
			}
		}
	}
	return t
}

// Record adds an activation record and updates aggregated stats.
func (t *SkillTracker) Record(rec SkillActivationRecord) {
	t.mu.Lock()
	defer t.mu.Unlock()

	stats, ok := t.data.Skills[rec.Skill]
	if !ok {
		stats = &SkillStats{
			Name:     rec.Skill,
			Scope:    rec.Scope,
			BySource: make(map[string]int),
		}
		t.data.Skills[rec.Skill] = stats
	}

	stats.ActivationCount++
	if rec.Success {
		stats.SuccessCount++
	} else {
		stats.FailCount++
	}
	stats.LastUsed = rec.Timestamp.Format(time.RFC3339)
	stats.TotalTokens += rec.TokenUsage
	stats.BySource[rec.Source]++
	if rec.Scope != "" {
		stats.Scope = rec.Scope
	}

	// rolling average duration
	n := float64(stats.ActivationCount)
	stats.AvgDurationMs = stats.AvgDurationMs*(n-1)/n + float64(rec.DurationMs)/n

	// append to history ring buffer
	t.data.History = append(t.data.History, rec)
	if len(t.data.History) > maxHistorySize {
		excess := len(t.data.History) - maxHistorySize
		t.data.History = t.data.History[excess:]
	}

	t.dirty = true
}

// Flush writes data to disk if there are pending changes.
func (t *SkillTracker) Flush() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.dirty {
		return nil
	}

	raw, err := json.MarshalIndent(t.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(t.path, append(raw, '\n'), 0600); err != nil {
		return err
	}
	t.dirty = false
	return nil
}

// GetStats returns a copy of all skill statistics.
func (t *SkillTracker) GetStats() map[string]*SkillStats {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make(map[string]*SkillStats, len(t.data.Skills))
	for k, v := range t.data.Skills {
		cp := *v
		cp.BySource = make(map[string]int, len(v.BySource))
		for sk, sv := range v.BySource {
			cp.BySource[sk] = sv
		}
		out[k] = &cp
	}
	return out
}

// GetHistory returns activation records for a specific skill, most recent first.
// If name is empty, returns records for all skills. Limit 0 means no limit.
func (t *SkillTracker) GetHistory(name string, limit int) []SkillActivationRecord {
	t.mu.Lock()
	defer t.mu.Unlock()

	var result []SkillActivationRecord
	// iterate in reverse for most-recent-first
	for i := len(t.data.History) - 1; i >= 0; i-- {
		rec := t.data.History[i]
		if name != "" && rec.Skill != name {
			continue
		}
		result = append(result, rec)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

// ParseSource extracts a canonical source label from a match reason string.
func ParseSource(matchReason string) string {
	if matchReason == "" {
		return "unknown"
	}
	if matchReason == "forced" {
		return "forced"
	}
	if matchReason == "mentioned" {
		return "mentioned"
	}
	if matchReason == "path" {
		return "path"
	}
	if matchReason == "always" {
		return "always"
	}
	// "keywords|hit=foo" → "keywords"
	// "tags|require=N" → "tags"
	// "traits:M/N" → "traits"
	if idx := strings.IndexAny(matchReason, "|:"); idx > 0 {
		return matchReason[:idx]
	}
	return matchReason
}
