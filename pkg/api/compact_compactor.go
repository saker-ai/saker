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
