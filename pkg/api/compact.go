// Compaction config + defaults live here; the compactor implementation,
// session-memory shortcut, and media-stripping helpers are split into the
// sibling compact_*.go files.
package api

import (
	"errors"
	"strings"
	"time"
)

// CompactConfig controls automatic context compaction.
type CompactConfig struct {
	Enabled          bool    `json:"enabled"`
	Threshold        float64 `json:"threshold"`          // trigger ratio (default 0.8); ignored when BufferTokens > 0
	PreserveCount    int     `json:"preserve_count"`     // keep latest N messages (default 5)
	SummaryModel     string  `json:"summary_model"`      // model tier/name used for summary
	ContextLimit     int     `json:"context_limit"`      // fallback token limit when Options.TokenLimit is unset
	SummaryMaxTokens int     `json:"summary_max_tokens"` // token budget for generated summary

	PreserveInitial  bool `json:"preserve_initial"`   // keep initial messages when compacting
	InitialCount     int  `json:"initial_count"`      // keep first N messages from the compacted prefix
	PreserveUserText bool `json:"preserve_user_text"` // keep recent user messages from the compacted prefix
	UserTextTokens   int  `json:"user_text_tokens"`   // token budget for preserved user messages

	MaxRetries    int           `json:"max_retries"`
	RetryDelay    time.Duration `json:"retry_delay"`
	FallbackModel string        `json:"fallback_model"`

	// BufferTokens is a fixed token buffer subtracted from the effective context
	// window to determine the compaction trigger point. When > 0, this replaces
	// the percentage-based Threshold. Mirrors Claude Code's AUTOCOMPACT_BUFFER_TOKENS (13000).
	BufferTokens int `json:"buffer_tokens"`

	// MaxOutputTokens is the model's max output token count, used to calculate
	// the effective context window. Defaults to 8192 when unset.
	MaxOutputTokens int `json:"max_output_tokens"`

	// MaxConsecutiveFailures is the circuit breaker limit. After this many
	// consecutive compaction failures, automatic compaction is disabled until
	// a successful compaction resets the counter. Default 3.
	MaxConsecutiveFailures int `json:"max_consecutive_failures"`

	// RolloutDir enables compact event persistence when non-empty.
	// The directory is resolved relative to Options.ProjectRoot unless absolute.
	RolloutDir string `json:"rollout_dir"`

	// Collapse controls tool output folding before compaction.
	Collapse CollapseConfig `json:"collapse"`

	// PostCompact controls what gets restored after compaction.
	PostCompact PostCompactConfig `json:"post_compact"`

	// Microcompact controls time-based tool output clearing before model calls.
	Microcompact MicrocompactConfig `json:"microcompact"`

	// SessionMemoryCompact controls zero-cost compaction via session memory.
	SessionMemoryCompact SessionMemoryCompactConfig `json:"session_memory_compact"`
}

// SessionMemoryCompactConfig controls compaction that uses session memory
// as the summary, avoiding a model API call entirely.
type SessionMemoryCompactConfig struct {
	Enabled         bool `json:"enabled"`           // default true when memory store is available
	MinTokensToKeep int  `json:"min_tokens_keep"`   // min tokens to preserve after compaction (default 10000)
	MaxTokensToKeep int  `json:"max_tokens_keep"`   // max tokens to preserve (hard cap, default 40000)
	MinTextMessages int  `json:"min_text_messages"` // min text messages to keep (default 5)
}

const (
	defaultSMMinTokensToKeep = 10000
	defaultSMMaxTokensToKeep = 40000
	defaultSMMinTextMessages = 5
)

func (c SessionMemoryCompactConfig) withDefaults() SessionMemoryCompactConfig {
	cfg := c
	if cfg.MinTokensToKeep <= 0 {
		cfg.MinTokensToKeep = defaultSMMinTokensToKeep
	}
	if cfg.MaxTokensToKeep <= 0 {
		cfg.MaxTokensToKeep = defaultSMMaxTokensToKeep
	}
	if cfg.MinTextMessages <= 0 {
		cfg.MinTextMessages = defaultSMMinTextMessages
	}
	return cfg
}

const (
	defaultCompactThreshold       = 0.8
	defaultCompactPreserve        = 5
	defaultContextLimit           = 200000
	defaultSummaryMaxTokens       = 1024
	defaultBufferTokens           = 13000
	defaultMaxOutputTokens        = 8192
	defaultMaxConsecutiveFailures = 3
	defaultMaxOutputReserved      = 20000
)

var errNoCompaction = errors.New("api: nothing to compact")

func (c CompactConfig) withDefaults() CompactConfig {
	cfg := c
	if cfg.Threshold <= 0 || cfg.Threshold > 1 {
		cfg.Threshold = defaultCompactThreshold
	}
	if cfg.PreserveCount <= 0 {
		cfg.PreserveCount = defaultCompactPreserve
	}
	if cfg.PreserveCount < 1 {
		cfg.PreserveCount = 1
	}
	cfg.SummaryModel = strings.TrimSpace(cfg.SummaryModel)
	if cfg.ContextLimit <= 0 {
		cfg.ContextLimit = defaultContextLimit
	}
	if cfg.SummaryMaxTokens <= 0 {
		cfg.SummaryMaxTokens = defaultSummaryMaxTokens
	}
	if cfg.InitialCount < 0 {
		cfg.InitialCount = 0
	}
	if cfg.PreserveInitial && cfg.InitialCount == 0 {
		cfg.InitialCount = 1
	}
	if cfg.UserTextTokens < 0 {
		cfg.UserTextTokens = 0
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.RetryDelay < 0 {
		cfg.RetryDelay = 0
	}
	if cfg.MaxOutputTokens <= 0 {
		cfg.MaxOutputTokens = defaultMaxOutputTokens
	}
	if cfg.MaxConsecutiveFailures <= 0 {
		cfg.MaxConsecutiveFailures = defaultMaxConsecutiveFailures
	}
	cfg.FallbackModel = strings.TrimSpace(cfg.FallbackModel)
	cfg.RolloutDir = strings.TrimSpace(cfg.RolloutDir)
	cfg.Collapse = cfg.Collapse.withDefaults()
	cfg.PostCompact = cfg.PostCompact.withDefaults()
	cfg.Microcompact = cfg.Microcompact.withDefaults()
	cfg.SessionMemoryCompact = cfg.SessionMemoryCompact.withDefaults()
	return cfg
}
