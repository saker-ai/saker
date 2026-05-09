package canvas

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cinience/saker/pkg/api"
	"github.com/cinience/saker/pkg/tool"
)

// Runtime is the minimal slice of api.Runtime the executor needs. Decoupling
// via an interface lets tests provide a fake implementation, and lets the
// caller (pkg/server) inject a thin wrapper that forwards to the package-
// private cacheMediaForProject helper without leaking server internals.
type Runtime interface {
	ExecuteTool(ctx context.Context, name string, params map[string]any) (*tool.ToolResult, error)
	Run(ctx context.Context, req api.Request) (*api.Response, error)
	ProjectRoot() string
	CacheMedia(ctx context.Context, rawURL, mediaType string) (path, url string, err error)
}

// NotifyFunc broadcasts a per-thread event to subscribers. The executor
// uses it for live status updates ("node X went running", "run finished").
// Wire to handler.notifySubscribers when constructing the Executor.
type NotifyFunc func(threadID, method string, params map[string]any)

// Executor is the orchestration brain. One instance per process.
type Executor struct {
	Runtime Runtime
	DataDir string
	Tracker *RunTracker
	Notify  NotifyFunc
	Logger  *slog.Logger

	// SaveInterval rate-limits intermediate canvas writes during a run.
	// A final flush always happens regardless of this value.
	SaveInterval time.Duration

	// PerNodeTimeout caps a single tool dispatch. Defaults to 10 minutes,
	// matching handleToolRun.
	PerNodeTimeout time.Duration
}

// RunOptions configures one execution.
type RunOptions struct {
	ThreadID string
	NodeIDs  []string      // empty → all gen nodes
	SkipDone bool          // skip nodes whose data.status == "done"
	Timeout  time.Duration // overall run timeout, default 30 min
}

// runtimeDefaults clones e with sensible defaults filled in.
func (e *Executor) runtimeDefaults() {
	if e.SaveInterval == 0 {
		e.SaveInterval = 200 * time.Millisecond
	}
	if e.PerNodeTimeout == 0 {
		e.PerNodeTimeout = 10 * time.Minute
	}
	if e.Logger == nil {
		e.Logger = slog.Default()
	}
}

// RunAsync starts a run in the background and returns the runId immediately.
// Caller polls via Tracker.Get or cancels via Tracker.Cancel.
func (e *Executor) RunAsync(parent context.Context, opts RunOptions) (string, error) {
	if e == nil {
		return "", errors.New("canvas: nil executor")
	}
	if e.Tracker == nil {
		return "", errors.New("canvas: nil tracker")
	}
	if opts.ThreadID == "" {
		return "", errors.New("canvas: threadID is required")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	if timeout > 60*time.Minute {
		timeout = 60 * time.Minute
	}

	// Detach from caller so HTTP request lifetime doesn't kill the run,
	// but keep cancel for explicit Tracker.Cancel calls.
	bgCtx, cancel := context.WithTimeout(context.Background(), timeout)
	runID := e.Tracker.Create(opts.ThreadID, cancel)

	go func() {
		defer cancel()
		summary, err := e.runInternal(bgCtx, runID, opts)
		if err != nil && summary == nil {
			summary = &RunSummary{
				RunID:      runID,
				ThreadID:   opts.ThreadID,
				Status:     RunStatusError,
				FinishedAt: time.Now(),
				Error:      err.Error(),
			}
		}
		e.Tracker.Update(runID, summary)
		if e.Notify != nil {
			e.Notify(opts.ThreadID, "canvas/run-finished", map[string]any{
				"runId":   runID,
				"status":  summary.Status,
				"summary": summary,
			})
		}
	}()
	_ = parent // parent is only used by callers that prefer RunSync; ignored here on purpose
	return runID, nil
}

// RunSync executes synchronously and returns the final summary. Useful for
// tests and for the REST endpoint when callers want the result inline.
func (e *Executor) RunSync(parent context.Context, opts RunOptions) (*RunSummary, error) {
	if e == nil {
		return nil, errors.New("canvas: nil executor")
	}
	if e.Tracker == nil {
		return nil, errors.New("canvas: nil tracker")
	}
	if opts.ThreadID == "" {
		return nil, errors.New("canvas: threadID is required")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	runID := e.Tracker.Create(opts.ThreadID, cancel)
	summary, err := e.runInternal(ctx, runID, opts)
	e.Tracker.Update(runID, summary)
	return summary, err
}

// runInternal is the shared body for sync and async runs. It owns the
// canvas document, persists it after each node, and reports per-node status
// to subscribers.
func (e *Executor) runInternal(ctx context.Context, runID string, opts RunOptions) (*RunSummary, error) {
	e.runtimeDefaults()

	doc, err := Load(e.DataDir, opts.ThreadID)
	if err != nil {
		return &RunSummary{RunID: runID, ThreadID: opts.ThreadID, Status: RunStatusError, FinishedAt: time.Now(), Error: err.Error()}, err
	}
	graph := NewGraph(doc)

	order, err := graph.TopoOrder()
	if err != nil {
		return &RunSummary{RunID: runID, ThreadID: opts.ThreadID, Status: RunStatusError, FinishedAt: time.Now(), Error: err.Error()}, err
	}

	wanted := selectNodes(graph, order, opts.NodeIDs)

	summary := &RunSummary{
		RunID:     runID,
		ThreadID:  opts.ThreadID,
		StartedAt: time.Now(),
		Status:    RunStatusRunning,
		Total:     len(wanted),
		Nodes:     make([]NodeRunResult, 0, len(wanted)),
	}
	e.Tracker.Update(runID, summary)
	e.notifyRun(opts.ThreadID, runID, RunStatusRunning, "")

	saver := newRateLimitedSaver(e.DataDir, opts.ThreadID, e.SaveInterval, e.Logger)

	for _, id := range wanted {
		if ctxErr := ctx.Err(); ctxErr != nil {
			summary.Status = cancelStatusFor(ctxErr)
			summary.Error = ctxErr.Error()
			break
		}

		node := graph.Get(id)
		if node == nil {
			continue
		}

		nodeRes := NodeRunResult{NodeID: id, NodeType: node.NodeType()}

		if opts.SkipDone && node.DataString("status") == NodeStatusDone {
			nodeRes.Status = NodeStatusSkipped
			summary.Skipped++
			summary.Nodes = append(summary.Nodes, nodeRes)
			e.Tracker.Update(runID, summary)
			continue
		}

		startedAt := time.Now()
		MarkRunning(node)
		saver.requestSave(doc)
		e.notifyNode(opts.ThreadID, id, NodeStatusRunning, nil)

		built, err := BuildParams(graph, node)
		if err != nil {
			MarkError(node, err.Error(), nil)
			saver.requestSave(doc)
			nodeRes.Status = NodeStatusError
			nodeRes.Error = err.Error()
			nodeRes.DurationMs = time.Since(startedAt).Milliseconds()
			summary.Failed++
			summary.Nodes = append(summary.Nodes, nodeRes)
			e.Tracker.Update(runID, summary)
			e.notifyNode(opts.ThreadID, id, NodeStatusError, map[string]any{"error": err.Error()})
			continue
		}
		nodeRes.Tool = built.ToolName

		mediaURL, mediaPath, sourceURL, mediaType, dispatchErr := e.dispatch(ctx, built)
		nodeRes.DurationMs = time.Since(startedAt).Milliseconds()

		if dispatchErr != nil {
			MarkError(node, dispatchErr.Error(), built.Params)
			AppendGenHistory(node, GenHistoryEntry{
				ID:        NewHistoryEntryID(),
				Prompt:    node.DataString("prompt"),
				CreatedAt: nowMillis(),
				Status:    NodeStatusError,
				Error:     dispatchErr.Error(),
				Params:    built.Params,
			})
			saver.requestSave(doc)
			nodeRes.Status = NodeStatusError
			nodeRes.Error = dispatchErr.Error()
			summary.Failed++
			summary.Nodes = append(summary.Nodes, nodeRes)
			e.Tracker.Update(runID, summary)
			e.notifyNode(opts.ThreadID, id, NodeStatusError, map[string]any{"error": dispatchErr.Error()})
			continue
		}

		var resultNodeID string
		if mediaURL != "" && mediaType != "" && !built.UseLLM {
			resultNodeID = AppendResultNode(doc, node, mediaType, mediaURL, mediaPath, sourceURL, truncate(node.DataString("prompt"), 30))
			AppendFlowEdge(doc, node.ID, resultNodeID)
			// Refresh the graph so downstream gen nodes can see the freshly
			// added result via CollectLinkedImageNodes / ReferenceBundles.
			graph = NewGraph(doc)
		}

		AppendGenHistory(node, GenHistoryEntry{
			ID:            NewHistoryEntryID(),
			Prompt:        node.DataString("prompt"),
			MediaURL:      mediaURL,
			MediaPath:     mediaPath,
			Params:        built.Params,
			CreatedAt:     nowMillis(),
			Status:        NodeStatusDone,
			ResultNodeIDs: filterEmpty([]string{resultNodeID}),
		})
		MarkPending(node)
		saver.requestSave(doc)

		nodeRes.Status = NodeStatusDone
		nodeRes.ResultURL = mediaURL
		nodeRes.ResultNodeID = resultNodeID
		summary.Succeeded++
		summary.Nodes = append(summary.Nodes, nodeRes)
		e.Tracker.Update(runID, summary)
		e.notifyNode(opts.ThreadID, id, NodeStatusDone, map[string]any{
			"resultUrl":    mediaURL,
			"resultNodeId": resultNodeID,
		})
	}

	if err := saver.flush(doc); err != nil {
		summary.Error = err.Error()
		if summary.Status == RunStatusRunning {
			summary.Status = RunStatusError
		}
	}

	if summary.Status == RunStatusRunning {
		if summary.Failed > 0 {
			summary.Status = RunStatusError
		} else {
			summary.Status = RunStatusDone
		}
	}
	summary.FinishedAt = time.Now()
	return summary, nil
}

// dispatch resolves the right runtime path for the built params and
// extracts (mediaURL, mediaPath, sourceURL, mediaType) from the result.
// LLM-bound textGen returns (text, "", "", "text") so callers can decide
// whether to emit a result node.
func (e *Executor) dispatch(ctx context.Context, built *BuildResult) (mediaURL, mediaPath, sourceURL, mediaType string, err error) {
	if built == nil {
		return "", "", "", "", errors.New("canvas: nil BuildResult")
	}

	nodeCtx, cancel := context.WithTimeout(ctx, e.PerNodeTimeout)
	defer cancel()

	if built.UseLLM {
		prompt, _ := built.Params["prompt"].(string)
		resp, err := e.Runtime.Run(nodeCtx, api.Request{Prompt: prompt, Ephemeral: true})
		if err != nil {
			return "", "", "", "", err
		}
		var text string
		if resp != nil && resp.Result != nil {
			text = resp.Result.Output
		}
		return text, "", "", "text", nil
	}

	res, err := e.Runtime.ExecuteTool(nodeCtx, built.ToolName, built.Params)
	if err != nil {
		return "", "", "", "", err
	}
	if res == nil {
		return "", "", "", "", errors.New("canvas: tool returned nil result")
	}
	if !res.Success {
		msg := res.Output
		if msg == "" {
			msg = "tool reported failure"
		}
		return "", "", "", "", errors.New(msg)
	}

	mediaURL, mediaType = extractMedia(res, built.ToolName)
	if mediaURL == "" {
		return "", "", "", "", fmt.Errorf("canvas: tool %s returned no media_url", built.ToolName)
	}
	if e.Runtime != nil && (mediaType == "image" || mediaType == "video" || mediaType == "audio") {
		if path, stableURL, cacheErr := e.Runtime.CacheMedia(nodeCtx, mediaURL, mediaType); cacheErr == nil {
			if stableURL != "" {
				sourceURL = mediaURL
				mediaURL = stableURL
				mediaPath = path
			}
		} else {
			e.Logger.Warn("canvas: cache media failed", "url", mediaURL, "err", cacheErr)
		}
	}
	return mediaURL, mediaPath, sourceURL, mediaType, nil
}

// extractMedia pulls the media_url / mediaType out of a ToolResult's
// Structured payload. Mirrors the path useGenerate.ts walks
// (`result.value.structured.media_url`).
func extractMedia(res *tool.ToolResult, toolName string) (string, string) {
	if res == nil || res.Structured == nil {
		return "", ""
	}
	m, ok := res.Structured.(map[string]any)
	if !ok {
		return "", ""
	}
	url, _ := m["media_url"].(string)
	mediaType, _ := m["media_type"].(string)
	if mediaType == "" {
		mediaType = guessMediaType(toolName)
	}
	return url, mediaType
}

func guessMediaType(toolName string) string {
	switch toolName {
	case "generate_image":
		return "image"
	case "generate_video":
		return "video"
	case "text_to_speech", "generate_music":
		return "audio"
	default:
		return ""
	}
}

func cancelStatusFor(err error) string {
	if errors.Is(err, context.Canceled) {
		return RunStatusCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return RunStatusError
	}
	return RunStatusError
}

func (e *Executor) notifyRun(threadID, runID, status, errMsg string) {
	if e.Notify == nil {
		return
	}
	payload := map[string]any{"runId": runID, "status": status}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	e.Notify(threadID, "canvas/run-status", payload)
}

func (e *Executor) notifyNode(threadID, nodeID, status string, extra map[string]any) {
	if e.Notify == nil {
		return
	}
	payload := map[string]any{"nodeId": nodeID, "status": status}
	for k, v := range extra {
		payload[k] = v
	}
	e.Notify(threadID, "canvas/node-status", payload)
}

func selectNodes(g *Graph, order, requested []string) []string {
	if len(requested) == 0 {
		out := make([]string, 0, len(order))
		for _, id := range order {
			if n := g.Get(id); n != nil && IsExecutableNodeType(n.NodeType()) {
				out = append(out, id)
			}
		}
		return out
	}
	want := make(map[string]bool, len(requested))
	for _, id := range requested {
		want[id] = true
	}
	out := make([]string, 0, len(requested))
	for _, id := range order {
		if !want[id] {
			continue
		}
		if n := g.Get(id); n != nil && IsExecutableNodeType(n.NodeType()) {
			out = append(out, id)
		}
	}
	return out
}

func filterEmpty(in []string) []string {
	out := in[:0]
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// rateLimitedSaver coalesces canvas writes during a run so a 50-node DAG
// doesn't write the JSON file 50 times. flush() always runs at the end.
type rateLimitedSaver struct {
	mu       sync.Mutex
	dataDir  string
	threadID string
	min      time.Duration
	last     time.Time
	dirty    bool
	logger   *slog.Logger
}

func newRateLimitedSaver(dataDir, threadID string, minInterval time.Duration, logger *slog.Logger) *rateLimitedSaver {
	return &rateLimitedSaver{
		dataDir:  dataDir,
		threadID: threadID,
		min:      minInterval,
		logger:   logger,
	}
}

func (s *rateLimitedSaver) requestSave(doc *Document) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true
	if time.Since(s.last) < s.min {
		return
	}
	if err := Save(s.dataDir, s.threadID, doc); err != nil {
		if s.logger != nil {
			s.logger.Warn("canvas: intermediate save failed", "err", err)
		}
		return
	}
	s.last = time.Now()
	s.dirty = false
}

func (s *rateLimitedSaver) flush(doc *Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := Save(s.dataDir, s.threadID, doc); err != nil {
		return err
	}
	s.last = time.Now()
	s.dirty = false
	return nil
}
