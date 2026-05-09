package api

// TokenWarningState describes how close the conversation context is to its limit.
type TokenWarningState struct {
	PercentUsed             float64 `json:"percent_used"`               // 0.0 – 1.0
	IsAboveWarningThreshold bool    `json:"is_above_warning_threshold"` // context > ~80%
	IsAboveErrorThreshold   bool    `json:"is_above_error_threshold"`   // context > ~90%
	IsAtBlockingLimit       bool    `json:"is_at_blocking_limit"`       // context > ~95%
}

const (
	// warningThresholdBuffer is the token headroom below the autocompact trigger
	// at which a warning is shown. Mirrors Claude Code's WARNING_THRESHOLD_BUFFER_TOKENS.
	warningThresholdBuffer = 20000

	// errorThresholdBuffer is the token headroom above the autocompact trigger
	// at which an error-level warning is shown.
	errorThresholdBuffer = 20000

	// blockingLimitBuffer is tokens from absolute context limit that triggers blocking.
	blockingLimitBuffer = 3000
)

// CalculateTokenWarning computes the current warning state based on how many
// tokens are in use relative to the context window and compaction thresholds.
func (c *compactor) CalculateTokenWarning(tokenCount int) TokenWarningState {
	if c == nil || c.limit <= 0 {
		return TokenWarningState{}
	}

	effectiveLimit := c.getEffectiveLimit()
	absoluteLimit := c.limit

	percentUsed := float64(tokenCount) / float64(absoluteLimit)
	if percentUsed > 1 {
		percentUsed = 1
	}

	warningThreshold := effectiveLimit - warningThresholdBuffer
	errorThreshold := effectiveLimit + errorThresholdBuffer
	blockingThreshold := absoluteLimit - blockingLimitBuffer

	return TokenWarningState{
		PercentUsed:             percentUsed,
		IsAboveWarningThreshold: tokenCount >= warningThreshold,
		IsAboveErrorThreshold:   tokenCount >= errorThreshold,
		IsAtBlockingLimit:       tokenCount >= blockingThreshold,
	}
}
