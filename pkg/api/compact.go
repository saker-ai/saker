package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	coreevents "github.com/cinience/saker/pkg/core/events"
	corehooks "github.com/cinience/saker/pkg/core/hooks"
	"github.com/cinience/saker/pkg/memory"
	"github.com/cinience/saker/pkg/message"
	"github.com/cinience/saker/pkg/model"
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

type compactor struct {
	cfg                 CompactConfig
	model               model.Model
	limit               int
	hooks               *corehooks.Executor
	rollout             *RolloutWriter
	memStore            *memory.Store
	mu                  sync.Mutex
	consecutiveFailures int
}

func newCompactor(projectRoot string, cfg CompactConfig, mdl model.Model, tokenLimit int, hooks *corehooks.Executor) *compactor {
	cfg = cfg.withDefaults()
	if !cfg.Enabled {
		return nil
	}
	limit := tokenLimit
	if limit <= 0 {
		limit = cfg.ContextLimit
	}
	rollout := newRolloutWriter(projectRoot, cfg.RolloutDir)
	return &compactor{
		cfg:     cfg,
		model:   mdl,
		limit:   limit,
		hooks:   hooks,
		rollout: rollout,
	}
}

// SetMemoryStore attaches a memory store for session memory compaction.
func (c *compactor) SetMemoryStore(store *memory.Store) {
	if c == nil {
		return
	}
	c.memStore = store
}

func (c *compactor) shouldCompact(msgCount, tokenCount int) bool {
	if c == nil || !c.cfg.Enabled {
		return false
	}
	// Circuit breaker: stop auto-compacting after too many consecutive failures.
	if c.consecutiveFailures >= c.cfg.MaxConsecutiveFailures {
		return false
	}
	if msgCount <= c.cfg.PreserveCount {
		return false
	}
	if tokenCount <= 0 || c.limit <= 0 {
		return false
	}
	// When BufferTokens is configured, use effective context window calculation
	// (mirrors Claude Code's approach: contextWindow - max(maxOutput, 20K) - bufferTokens).
	if c.cfg.BufferTokens > 0 {
		return tokenCount >= c.getEffectiveLimit()
	}
	// Fallback: percentage-based threshold.
	ratio := float64(tokenCount) / float64(c.limit)
	return ratio >= c.cfg.Threshold
}

// getEffectiveLimit calculates the token threshold that triggers compaction.
// Formula: contextWindow - max(maxOutput, 20000) - bufferTokens
func (c *compactor) getEffectiveLimit() int {
	reserved := c.cfg.MaxOutputTokens
	if reserved < defaultMaxOutputReserved {
		reserved = defaultMaxOutputReserved
	}
	effective := c.limit - reserved - c.cfg.BufferTokens
	if effective < 1 {
		effective = 1
	}
	return effective
}

type compactResult struct {
	summary       string
	originalMsgs  int
	preservedMsgs int
	tokensBefore  int
	tokensAfter   int
}

func (c *compactor) maybeCompact(ctx context.Context, hist *message.History, sessionID string, recorder *hookRecorder) (compactResult, bool, error) {
	if c == nil || hist == nil || !c.cfg.Enabled {
		return compactResult{}, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	msgCount := hist.Len()
	tokenCount := hist.TokenCount()
	if !c.shouldCompact(msgCount, tokenCount) {
		return compactResult{}, false, nil
	}
	snapshot := hist.All()
	if len(snapshot) <= c.cfg.PreserveCount {
		return compactResult{}, false, nil
	}

	payload := coreevents.PreCompactPayload{
		EstimatedTokens: tokenCount,
		TokenLimit:      c.limit,
		Threshold:       c.cfg.Threshold,
		PreserveCount:   c.cfg.PreserveCount,
	}
	allow, err := c.preCompact(ctx, sessionID, payload, recorder)
	if err != nil {
		return compactResult{}, false, err
	}
	if !allow {
		return compactResult{}, false, nil
	}

	// Apply tool output collapse before attempting full compaction.
	if c.cfg.Collapse.Enabled {
		collapsed := c.collapseToolOutputs(snapshot)
		if len(collapsed) != len(snapshot) || !messagesEqual(collapsed, snapshot) {
			hist.Replace(collapsed)
			snapshot = collapsed
			tokenCount = hist.TokenCount()
			// Re-check if compaction is still needed after collapse.
			if !c.shouldCompact(len(snapshot), tokenCount) {
				return compactResult{}, false, nil
			}
		}
	}

	// Try session memory compaction first (zero API cost).
	if c.cfg.SessionMemoryCompact.Enabled && c.memStore != nil {
		res, err := c.sessionMemoryCompact(hist, snapshot, tokenCount)
		if err == nil {
			c.consecutiveFailures = 0
			c.postCompact(sessionID, res, recorder)
			return res, true, nil
		}
		slog.Warn("api: session memory compact failed, falling back to full compact", "error", err)
	}

	res, err := c.compact(ctx, hist, snapshot, tokenCount)
	if err != nil {
		if errors.Is(err, errNoCompaction) {
			return compactResult{}, false, nil
		}
		c.consecutiveFailures++
		return compactResult{}, false, err
	}
	c.consecutiveFailures = 0
	c.postCompact(sessionID, res, recorder)
	return res, true, nil
}

func (c *compactor) preCompact(ctx context.Context, sessionID string, payload coreevents.PreCompactPayload, recorder *hookRecorder) (bool, error) {
	evt := coreevents.Event{
		Type:      coreevents.PreCompact,
		SessionID: sessionID,
		Payload:   payload,
	}
	if c.hooks == nil {
		c.record(recorder, evt)
		return true, nil
	}
	results, err := c.hooks.Execute(ctx, evt)
	c.record(recorder, evt)
	if err != nil {
		return false, err
	}
	for _, res := range results {
		if res.Decision == corehooks.DecisionBlockingError {
			return false, nil
		}
		if res.Output != nil && res.Output.Continue != nil && !*res.Output.Continue {
			return false, nil
		}
	}
	return true, nil
}

func (c *compactor) postCompact(sessionID string, res compactResult, recorder *hookRecorder) {
	payload := coreevents.ContextCompactedPayload{
		Summary:               res.summary,
		OriginalMessages:      res.originalMsgs,
		PreservedMessages:     res.preservedMsgs,
		EstimatedTokensBefore: res.tokensBefore,
		EstimatedTokensAfter:  res.tokensAfter,
	}
	evt := coreevents.Event{
		Type:      coreevents.ContextCompacted,
		SessionID: sessionID,
		Payload:   payload,
	}
	if c.hooks != nil {
		//nolint:errcheck // context compacted events are non-critical notifications
		c.hooks.Publish(evt)
	}
	c.record(recorder, evt)
	if c.rollout != nil {
		if err := c.rollout.WriteCompactEvent(sessionID, res); err != nil {
			slog.Error("api: write compaction rollout", "error", err)
		}
	}
}

func (c *compactor) record(recorder *hookRecorder, evt coreevents.Event) {
	if recorder == nil {
		return
	}
	recorder.Record(evt)
}

func (c *compactor) compact(ctx context.Context, hist *message.History, snapshot []message.Message, tokensBefore int) (compactResult, error) {
	if c.model == nil {
		return compactResult{}, errors.New("api: summary model is nil")
	}
	preserve := c.cfg.PreserveCount
	if preserve >= len(snapshot) {
		return compactResult{}, nil
	}
	cut := len(snapshot) - preserve
	older := snapshot[:cut]
	kept := snapshot[cut:]

	preservedPrefix := make([]bool, len(older))

	var initial []message.Message
	if c.cfg.PreserveInitial && c.cfg.InitialCount > 0 {
		n := c.cfg.InitialCount
		if n > len(older) {
			n = len(older)
		}
		initial = make([]message.Message, 0, n)
		for i := 0; i < n; i++ {
			preservedPrefix[i] = true
			initial = append(initial, message.CloneMessage(older[i]))
		}
	}

	var userText []message.Message
	if c.cfg.PreserveUserText && c.cfg.UserTextTokens > 0 {
		var counter message.NaiveCounter
		total := 0
		indices := make([]int, 0)
		for i := len(older) - 1; i >= 0; i-- {
			if preservedPrefix[i] {
				continue
			}
			if older[i].Role != "user" || strings.TrimSpace(older[i].Content) == "" {
				continue
			}
			cost := counter.Count(older[i])
			total += cost
			indices = append(indices, i)
			preservedPrefix[i] = true
			if total >= c.cfg.UserTextTokens {
				break
			}
		}
		if len(indices) > 0 {
			userText = make([]message.Message, 0, len(indices))
			for j := len(indices) - 1; j >= 0; j-- {
				userText = append(userText, message.CloneMessage(older[indices[j]]))
			}
		}
	}

	summarize := make([]message.Message, 0, len(older))
	for i, msg := range older {
		if preservedPrefix[i] {
			continue
		}
		summarize = append(summarize, msg)
	}
	if len(summarize) == 0 {
		return compactResult{}, errNoCompaction
	}

	// Strip media/binary content before sending to the summary model.
	summarize = stripMediaContent(summarize)

	req := model.Request{
		Messages:  convertMessages(summarize),
		System:    summarySystemPrompt,
		Model:     c.cfg.SummaryModel,
		MaxTokens: c.cfg.SummaryMaxTokens,
	}
	resp, err := c.completeSummary(ctx, req)
	if err != nil {
		return compactResult{}, fmt.Errorf("api: compact summary: %w", err)
	}
	summary := strings.TrimSpace(resp.Message.Content)
	if summary == "" {
		summary = "对话摘要为空"
	}

	newMsgs := make([]message.Message, 0, len(initial)+1+len(userText)+len(kept))
	newMsgs = append(newMsgs, message.CloneMessages(initial)...)
	newMsgs = append(newMsgs, message.Message{
		Role:    "system",
		Content: fmt.Sprintf("对话摘要：\n%s", summary),
	})
	newMsgs = append(newMsgs, message.CloneMessages(userText)...)
	newMsgs = append(newMsgs, message.CloneMessages(kept)...)
	hist.Replace(newMsgs)

	// Post-compact restoration: re-inject recently read files.
	if c.cfg.PostCompact.RestoreFiles {
		filePaths := extractRecentFilePaths(older, c.cfg.PostCompact.MaxFilesToRestore)
		if restoreMsgs := buildPostCompactMessages(filePaths, c.cfg.PostCompact.FileTokenBudget); len(restoreMsgs) > 0 {
			restored := hist.All()
			restored = append(restored, restoreMsgs...)
			hist.Replace(restored)
		}
	}

	tokensAfter := hist.TokenCount()
	preservedMsgs := len(initial) + len(userText) + len(kept)
	return compactResult{
		summary:       summary,
		originalMsgs:  len(snapshot),
		preservedMsgs: preservedMsgs,
		tokensBefore:  tokensBefore,
		tokensAfter:   tokensAfter,
	}, nil
}

// messagesEqual performs a shallow content comparison to detect if collapse changed anything.
func messagesEqual(a, b []message.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content {
			return false
		}
	}
	return true
}

// ResetCircuitBreaker resets the consecutive failure counter, re-enabling
// automatic compaction after it was tripped.
func (c *compactor) ResetCircuitBreaker() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.consecutiveFailures = 0
	c.mu.Unlock()
}

func (c *compactor) completeSummary(ctx context.Context, req model.Request) (*model.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil || c.model == nil {
		return nil, errors.New("api: summary model is nil")
	}
	attempts := 1 + c.cfg.MaxRetries
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if delay := c.cfg.RetryDelay; delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
				}
			}
			if fallback := strings.TrimSpace(c.cfg.FallbackModel); fallback != "" {
				req.Model = fallback
			}
		}
		var resp *model.Response
		err := c.model.CompleteStream(ctx, req, func(sr model.StreamResult) error {
			if sr.Final && sr.Response != nil {
				resp = sr.Response
			}
			return nil
		})
		if err == nil && resp != nil {
			return resp, nil
		}
		if err == nil && resp == nil {
			err = errors.New("api: compact summary returned no final response")
		}
		lastErr = err
		if attempts > 1 {
			slog.Warn("api: compact summary attempt failed", "attempt", attempt, "max", attempts, "error", err)
		}
	}

	// PTL fallback: if all attempts failed with prompt-too-long, truncate and retry.
	if isPromptTooLong(lastErr) {
		for ptlRetry := 0; ptlRetry < 2 && len(req.Messages) > 6; ptlRetry++ {
			req.Messages = req.Messages[len(req.Messages)/2:]
			slog.Warn("api: compact PTL retry", "retry", ptlRetry+1, "messages", len(req.Messages))

			var resp *model.Response
			err := c.model.CompleteStream(ctx, req, func(sr model.StreamResult) error {
				if sr.Final && sr.Response != nil {
					resp = sr.Response
				}
				return nil
			})
			if err == nil && resp != nil {
				return resp, nil
			}
			if !isPromptTooLong(err) {
				return nil, err
			}
			lastErr = err
		}
	}

	return nil, lastErr
}

// sessionMemoryCompact uses the memory store content as a summary, avoiding
// a model API call entirely. It keeps recent messages based on token and
// message count thresholds, similar to Claude Code's sessionMemoryCompact.
func (c *compactor) sessionMemoryCompact(hist *message.History, snapshot []message.Message, tokensBefore int) (compactResult, error) {
	if c.memStore == nil {
		return compactResult{}, errors.New("api: no memory store")
	}
	memCtx, err := c.memStore.BuildContext(25000) // 25KB budget
	if err != nil || strings.TrimSpace(memCtx) == "" {
		return compactResult{}, fmt.Errorf("api: session memory empty or error: %w", err)
	}

	cfg := c.cfg.SessionMemoryCompact
	counter := message.NaiveCounter{}

	// Calculate which messages to keep from the tail, working backwards.
	totalTokens := 0
	textMsgCount := 0
	startIndex := len(snapshot)

	for i := len(snapshot) - 1; i >= 0; i-- {
		msg := snapshot[i]
		msgTokens := counter.Count(msg)

		// Stop expanding if we hit the max cap.
		if totalTokens+msgTokens > cfg.MaxTokensToKeep {
			break
		}

		totalTokens += msgTokens
		if msg.Role == "user" || msg.Role == "assistant" {
			if strings.TrimSpace(msg.Content) != "" {
				textMsgCount++
			}
		}
		startIndex = i

		// Stop if we meet both minimums.
		if totalTokens >= cfg.MinTokensToKeep && textMsgCount >= cfg.MinTextMessages {
			break
		}
	}

	// Adjust startIndex to avoid splitting tool_use/tool_result pairs.
	startIndex = adjustIndexForToolPairs(snapshot, startIndex)

	kept := snapshot[startIndex:]
	if len(kept) == 0 {
		return compactResult{}, errors.New("api: session memory compact would keep no messages")
	}

	summary := fmt.Sprintf("会话记忆摘要（零成本压缩）：\n%s", memCtx)
	newMsgs := make([]message.Message, 0, 1+len(kept))
	newMsgs = append(newMsgs, message.Message{
		Role:    "system",
		Content: summary,
	})
	newMsgs = append(newMsgs, message.CloneMessages(kept)...)
	hist.Replace(newMsgs)

	tokensAfter := hist.TokenCount()
	return compactResult{
		summary:       summary,
		originalMsgs:  len(snapshot),
		preservedMsgs: len(kept),
		tokensBefore:  tokensBefore,
		tokensAfter:   tokensAfter,
	}, nil
}

// adjustIndexForToolPairs moves startIndex backwards to avoid splitting
// a tool_use from its tool_result. If the message at startIndex is a
// tool_result, include the preceding assistant message with the tool_use.
func adjustIndexForToolPairs(msgs []message.Message, startIndex int) int {
	if startIndex <= 0 || startIndex >= len(msgs) {
		return startIndex
	}

	// Collect tool_result IDs in the kept range.
	resultIDs := make(map[string]struct{})
	for i := startIndex; i < len(msgs); i++ {
		if msgs[i].Role == "tool" {
			for _, tc := range msgs[i].ToolCalls {
				if tc.ID != "" {
					resultIDs[tc.ID] = struct{}{}
				}
			}
		}
	}

	// Collect tool_use IDs already in the kept range.
	useIDs := make(map[string]struct{})
	for i := startIndex; i < len(msgs); i++ {
		if msgs[i].Role == "assistant" {
			for _, tc := range msgs[i].ToolCalls {
				if tc.ID != "" {
					useIDs[tc.ID] = struct{}{}
				}
			}
		}
	}

	// Find tool_results that need their tool_use included.
	needed := make(map[string]struct{})
	for id := range resultIDs {
		if _, ok := useIDs[id]; !ok {
			needed[id] = struct{}{}
		}
	}

	// Walk backwards to include messages with matching tool_uses.
	for i := startIndex - 1; i >= 0 && len(needed) > 0; i-- {
		if msgs[i].Role == "assistant" {
			for _, tc := range msgs[i].ToolCalls {
				if _, ok := needed[tc.ID]; ok {
					startIndex = i
					delete(needed, tc.ID)
				}
			}
		}
	}

	return startIndex
}

// Base64 minimum length to detect embedded binary data in tool results.
const base64MinLen = 500

// stripMediaContent removes large base64-encoded data from messages before
// sending them to the summary model. This prevents the compaction API call
// from hitting prompt-too-long due to embedded images or binary content.
func stripMediaContent(msgs []message.Message) []message.Message {
	result := make([]message.Message, len(msgs))
	changed := false
	for i, msg := range msgs {
		stripped, didStrip := stripMediaFromMessage(msg)
		if didStrip {
			changed = true
			result[i] = stripped
		} else {
			result[i] = msg
		}
	}
	if !changed {
		return msgs
	}
	return result
}

func stripMediaFromMessage(msg message.Message) (message.Message, bool) {
	changed := false

	// Strip base64 data from tool call results.
	if len(msg.ToolCalls) > 0 {
		newCalls := make([]message.ToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			if stripped, ok := stripBase64FromResult(tc.Result); ok {
				changed = true
				newCalls[i] = message.ToolCall{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
					Result:    stripped,
				}
			} else {
				newCalls[i] = tc
			}
		}
		if changed {
			clone := message.CloneMessage(msg)
			clone.ToolCalls = newCalls
			return clone, true
		}
	}

	// Strip base64 from content blocks (images/documents).
	if len(msg.ContentBlocks) > 0 {
		newBlocks := make([]message.ContentBlock, len(msg.ContentBlocks))
		for i, block := range msg.ContentBlocks {
			switch block.Type {
			case message.ContentBlockImage:
				changed = true
				newBlocks[i] = message.ContentBlock{Type: message.ContentBlockText, Text: "[image]"}
			case message.ContentBlockDocument:
				changed = true
				newBlocks[i] = message.ContentBlock{Type: message.ContentBlockText, Text: "[document]"}
			default:
				newBlocks[i] = block
			}
		}
		if changed {
			clone := message.CloneMessage(msg)
			clone.ContentBlocks = newBlocks
			return clone, true
		}
	}

	return msg, false
}

// stripBase64FromResult detects and replaces base64-encoded data in a tool
// result string. Returns the cleaned string and whether any change was made.
func stripBase64FromResult(result string) (string, bool) {
	if len(result) < base64MinLen {
		return result, false
	}

	// Heuristic: if the result looks like it's mostly base64, replace it.
	// Check for long runs of base64 characters.
	if looksLikeBase64(result) {
		return "[binary data removed for compaction]", true
	}
	return result, false
}

// looksLikeBase64 returns true if the string appears to be base64-encoded
// data (high ratio of base64 alphabet characters and length > threshold).
func looksLikeBase64(s string) bool {
	if len(s) < base64MinLen {
		return false
	}
	// Sample a portion to avoid scanning huge strings.
	sample := s
	if len(sample) > 2000 {
		sample = sample[:2000]
	}
	b64Chars := 0
	for _, r := range sample {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=' {
			b64Chars++
		}
	}
	ratio := float64(b64Chars) / float64(len(sample))
	// If > 90% of characters are base64 alphabet and string is long, it's likely binary data.
	return ratio > 0.9 && len(s) > 1000
}
