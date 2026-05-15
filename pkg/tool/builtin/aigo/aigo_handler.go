package aigo

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	sdk "github.com/godeps/aigo"
	"github.com/godeps/aigo/tooldef"

	"github.com/saker-ai/saker/pkg/tool"
)

func (t *AigoTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if t.client == nil {
		return nil, fmt.Errorf("aigo: client is nil")
	}

	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	// Validate params against schema constraints (enums, required) before calling the API.
	if err := tooldef.ValidateParams(t.def, params); err != nil {
		slog.Warn("[aigo] validation failed", "tool", t.def.Name, "error", err)
		return &tool.ToolResult{
			Success: false,
			Output:  formatInvalidParams(t.def, params, err),
		}, nil
	}

	task := buildTask(t.def.Name, params)
	slog.Debug("[aigo] task built", "tool", t.def.Name, "prompt", task.Prompt, "size", task.Size, "refs", len(task.References))

	// DryRun check: surface warnings before executing.
	engineName := stringParam(params, "engine")
	if engineName == "" && len(t.engines) > 0 {
		engineName = t.engines[0]
	}
	if engineName != "" {
		if dr, err := t.client.DryRun(engineName, task); err == nil && len(dr.Warnings) > 0 {
			slog.Debug("[aigo] dryrun warnings", "tool", t.def.Name, "warnings", dr.Warnings)
			_ = dr // warnings are informational
		}
	}

	return t.executeSync(ctx, params, task)
}

// StreamExecute implements tool.StreamingTool, emitting periodic progress
// updates for long-running operations (e.g. video generation) while the
// engine polls internally via WaitForCompletion.
func (t *AigoTool) StreamExecute(ctx context.Context, params map[string]interface{}, emit func(string, bool)) (*tool.ToolResult, error) {
	if t.client == nil {
		return nil, fmt.Errorf("aigo: client is nil")
	}

	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}

	if err := tooldef.ValidateParams(t.def, params); err != nil {
		return &tool.ToolResult{
			Success: false,
			Output:  formatInvalidParams(t.def, params, err),
		}, nil
	}

	task := buildTask(t.def.Name, params)

	// For slow capabilities (video), use the SDK's native progress callback
	// to surface real polling progress through the SSE pipeline.
	var opts []sdk.ExecuteOption
	cap := toolCapability[t.def.Name]
	if slowCapabilities[cap] && emit != nil {
		opts = append(opts, sdk.WithProgress(func(evt sdk.ProgressEvent) {
			switch evt.Phase {
			case "submitted":
				emit(fmt.Sprintf("[%s] task submitted", t.def.Name), false)
			case "polling":
				emit(fmt.Sprintf("[%s] generating... attempt %d, %s elapsed",
					t.def.Name, evt.Attempt, evt.Elapsed.Truncate(time.Second)), false)
			case "completed":
				emit(fmt.Sprintf("[%s] completed in %s",
					t.def.Name, evt.Elapsed.Truncate(time.Second)), false)
			}
		}))
	}

	return t.executeSync(ctx, params, task, opts...)
}

// resolveEngines returns the best engine for this execution, applying smart
// routing for video generation based on reference assets:
//   - no references        → t2v (text-to-video)
//   - 1 image reference    → i2v (image-to-video)
//   - multiple refs / video → r2v (reference-to-video, up to 5 mixed refs)
func (t *AigoTool) resolveEngines(params map[string]interface{}, task sdk.AgentTask) []string {
	if t.def.Name != "generate_video" || len(task.References) == 0 {
		return t.engines
	}

	imageCount := 0
	hasVideoRef := false
	for _, ref := range task.References {
		switch ref.Type {
		case sdk.ReferenceTypeImage:
			imageCount++
		case sdk.ReferenceTypeVideo:
			hasVideoRef = true
		}
	}

	if imageCount == 0 && !hasVideoRef {
		return t.engines
	}

	// Determine target suffix based on reference pattern.
	targetSuffix := "-i2v"
	if imageCount > 1 || hasVideoRef {
		targetSuffix = "-r2v"
	}

	// Find matching engine from the registered list.
	for _, eng := range t.engines {
		if strings.HasSuffix(eng, targetSuffix) {
			slog.Info("[aigo] smart route", "tool", t.def.Name, "images", imageCount, "has_video", hasVideoRef, "engine", eng)
			return []string{eng}
		}
	}
	// Fallback: any engine with reference support.
	for _, eng := range t.engines {
		if strings.Contains(eng, "-i2v") || strings.Contains(eng, "-r2v") {
			slog.Info("[aigo] smart route fallback", "tool", t.def.Name, "images", imageCount, "has_video", hasVideoRef, "engine", eng)
			return []string{eng}
		}
	}
	return t.engines
}

func (t *AigoTool) executeSync(ctx context.Context, params map[string]interface{}, task sdk.AgentTask, opts ...sdk.ExecuteOption) (*tool.ToolResult, error) {
	start := time.Now()
	engines := t.resolveEngines(params, task)

	// If caller specified an engine, try it directly.
	if eng := stringParam(params, "engine"); eng != "" {
		slog.Info("[aigo] calling engine", "tool", t.def.Name, "engine", eng)
		result, err := t.client.ExecuteTask(ctx, eng, task, opts...)
		if err != nil {
			slog.Error("[aigo] engine FAILED", "tool", t.def.Name, "engine", eng, "elapsed", time.Since(start), "error", err)
			return nil, fmt.Errorf("aigo %s (engine %s): %w", t.def.Name, eng, err)
		}
		slog.Info("[aigo] engine OK", "tool", t.def.Name, "engine", eng, "elapsed", time.Since(start), "result_len", len(result.Value))
		tr, terr := toToolResult(result, t.def.Name)
		if terr != nil {
			slog.Error("[aigo] engine INVALID", "tool", t.def.Name, "engine", eng, "elapsed", time.Since(start), "error", terr)
			return nil, terr
		}
		return tr, nil
	}

	// Single engine: direct call.
	if len(engines) == 1 {
		slog.Info("[aigo] calling single engine", "tool", t.def.Name, "engine", engines[0])
		result, err := t.client.ExecuteTask(ctx, engines[0], task, opts...)
		if err != nil {
			slog.Error("[aigo] engine FAILED", "tool", t.def.Name, "engine", engines[0], "elapsed", time.Since(start), "error", err)
			return nil, fmt.Errorf("aigo %s: %w", t.def.Name, err)
		}
		slog.Info("[aigo] engine OK", "tool", t.def.Name, "engine", engines[0], "elapsed", time.Since(start), "result_len", len(result.Value))
		tr, terr := toToolResult(result, t.def.Name)
		if terr != nil {
			slog.Error("[aigo] engine INVALID", "tool", t.def.Name, "engine", engines[0], "elapsed", time.Since(start), "error", terr)
			return nil, terr
		}
		return tr, nil
	}

	// Multiple engines: fallback.
	slog.Info("[aigo] calling with fallback engines", "tool", t.def.Name, "engines", engines)
	fr, err := t.client.ExecuteTaskWithFallback(ctx, engines, task, opts...)
	if err != nil {
		slog.Error("[aigo] fallback FAILED", "tool", t.def.Name, "elapsed", time.Since(start), "error", err)
		return nil, fmt.Errorf("aigo %s: %w", t.def.Name, err)
	}
	slog.Info("[aigo] fallback OK", "tool", t.def.Name, "elapsed", time.Since(start), "engine", fr.Engine, "result_len", len(fr.Output.Value))
	tr, terr := toToolResult(fr.Output, t.def.Name)
	if terr != nil {
		slog.Error("[aigo] fallback INVALID", "tool", t.def.Name, "elapsed", time.Since(start), "engine", fr.Engine, "error", terr)
		return nil, terr
	}
	return tr, nil
}

func toToolResult(result sdk.Result, toolName string) (*tool.ToolResult, error) {
	cap := toolCapability[toolName]

	// Backstop for the "engine returned task_id instead of URL" bug class:
	// for media-producing tools, refuse a non-URL value loud-and-fast so the
	// agent loop sees a real error rather than the canvas silently writing a
	// UUID into <video src=>. The legitimate fix lives in the engine factory
	// (WaitForCompletion); this is the last line of defense.
	if mediaCapabilities[cap] && !isMediaURL(result.Value) {
		return nil, fmt.Errorf(
			"aigo %s: engine returned %q which is not a media URL — likely an unresumed async task. "+
				"Configure waitForCompletion=true on the provider, or wire a Resumer for the engine.",
			toolName, result.Value)
	}

	tr := &tool.ToolResult{
		Success: true,
		Output:  result.Value,
	}
	meta := map[string]interface{}{}
	if result.Metadata != nil {
		for k, v := range result.Metadata {
			meta[k] = v
		}
	}
	switch cap {
	case "image", "image_edit":
		meta["media_type"] = "image"
	case "video", "video_edit":
		meta["media_type"] = "video"
	case "tts", "music":
		meta["media_type"] = "audio"
	case "3d":
		meta["media_type"] = "3d"
	case "asr":
		meta["media_type"] = "text"
	}
	if result.Value != "" {
		meta["media_url"] = result.Value
	}
	if len(meta) > 0 {
		tr.Structured = meta
	}
	return tr, nil
}

// formatInvalidParams enriches the bare "parameter X is required" error
// from tooldef.ValidateParams with two extra hints — the schema's required
// field list and the keys the model actually passed — so a confused model
// has concrete information to self-correct on its next iteration.
//
// Background: in the eddaff17 thread incident the model emitted a
// generate_image call with no prompt; the bare error gave it nothing to
// hook onto and the next iteration produced garbage instead of a fixed
// call. Surfacing the schema in-band is cheap and gives the model the
// context to recover without user intervention.
func formatInvalidParams(def tooldef.ToolDef, params map[string]interface{}, err error) string {
	provided := make([]string, 0, len(params))
	for k := range params {
		provided = append(provided, k)
	}
	sort.Strings(provided)

	required := append([]string(nil), def.Parameters.Required...)
	sort.Strings(required)

	out := fmt.Sprintf(
		"Invalid parameters for tool %q: %s.\n  required: [%s]\n  provided: [%s]\n  hint: re-emit the call with all required fields populated.",
		def.Name,
		err,
		strings.Join(required, ", "),
		strings.Join(provided, ", "),
	)
	// Special case: zero parameters delivered, but the schema clearly
	// requires some. This is the eddaff17 fingerprint — usually means the
	// upstream API proxy stripped tool_use.input rather than the model
	// emitting a literally empty call. The extra note steers a confused
	// model toward retrying the WHOLE call, not just adding one field.
	if len(provided) == 0 && len(required) > 0 {
		out += "\n  note: tool was called with no parameters at all — if this was unintentional," +
			" the API proxy may have dropped tool_use.input. Re-emit the entire call with every" +
			" required field populated, do not simply patch one missing field."
	}
	return out
}
