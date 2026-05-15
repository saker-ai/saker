package api

import (
	"sync"
	"time"

	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/model"
)

const (
	TimelineInputArtifact     = "input_artifact"
	TimelineGeneratedArtifact = "generated_artifact"
	TimelineToolCall          = "tool_call"
	TimelineToolResult        = "tool_result"
	TimelineCacheHit          = "cache_hit"
	TimelineCacheMiss         = "cache_miss"
	TimelineCheckpointCreate  = "checkpoint_create"
	TimelineCheckpointResume  = "checkpoint_resume"
	TimelineTokenSnapshot     = "token_snapshot"
	TimelineLatencySnapshot   = "latency_snapshot"
)

// TimelineEntry captures one significant runtime event for multimodal tracing.
type TimelineEntry struct {
	Kind         string                `json:"kind"`
	Name         string                `json:"name,omitempty"`
	Artifact     *artifact.ArtifactRef `json:"artifact,omitempty"`
	CheckpointID string                `json:"checkpoint_id,omitempty"`
	CacheKey     string                `json:"cache_key,omitempty"`
	Output       string                `json:"output,omitempty"`
	Duration     time.Duration         `json:"duration,omitempty"`
	Usage        *model.Usage          `json:"usage,omitempty"`
	Timestamp    time.Time             `json:"timestamp"`
}

type timelineCollector struct {
	mu      sync.RWMutex
	entries []TimelineEntry
}

func (c *timelineCollector) add(entry TimelineEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	c.entries = append(c.entries, entry)
}

func (c *timelineCollector) snapshot() []TimelineEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.entries) == 0 {
		return nil
	}
	out := make([]TimelineEntry, len(c.entries))
	copy(out, c.entries)
	return out
}
