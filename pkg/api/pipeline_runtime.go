package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/pipeline"
	runtimecache "github.com/cinience/saker/pkg/runtime/cache"
	"github.com/cinience/saker/pkg/runtime/checkpoint"
	"github.com/cinience/saker/pkg/runtime/skills"
	"github.com/cinience/saker/pkg/tool"
	"github.com/google/uuid"
	"maps"
)

func (rt *Runtime) runPipeline(ctx context.Context, req Request) (*Response, error) {
	normalized := req.normalized(rt.mode, defaultSessionID(rt.mode.EntryPoint))
	if normalized.SessionID == "" {
		normalized.SessionID = defaultSessionID(rt.mode.EntryPoint)
	}
	if normalized.RequestID == "" {
		normalized.RequestID = uuid.New().String()
	}

	if strings.TrimSpace(normalized.ResumeFromCheckpoint) != "" {
		return rt.resumePipeline(ctx, normalized)
	}
	if normalized.Pipeline == nil {
		return nil, errors.New("api: pipeline is required")
	}

	timeline := &timelineCollector{}
	result, checkpointID, interrupted, err := rt.executePipelineTree(ctx, *normalized.Pipeline, pipeline.Input{}, normalized.SessionID, timeline)
	if err != nil {
		return nil, err
	}
	return rt.pipelineResponse(normalized, result, checkpointID, interrupted, timeline), nil
}

func (rt *Runtime) resumePipeline(ctx context.Context, req Request) (*Response, error) {
	if rt.checkpoints == nil {
		return nil, errors.New("api: checkpoint store not configured")
	}
	entry, err := rt.checkpoints.Load(ctx, req.ResumeFromCheckpoint)
	if err != nil {
		return nil, err
	}
	if entry.SessionID != "" && req.SessionID != "" && entry.SessionID != req.SessionID {
		return nil, fmt.Errorf("api: checkpoint %s does not belong to session %s", req.ResumeFromCheckpoint, req.SessionID)
	}

	timeline := &timelineCollector{}
	timeline.add(TimelineEntry{Kind: TimelineCheckpointResume, CheckpointID: req.ResumeFromCheckpoint})
	result := entry.Result
	checkpointID := ""
	interrupted := false
	if entry.Remaining != nil {
		next, nextCheckpointID, nextInterrupted, err := rt.executePipelineTree(ctx, *entry.Remaining, entry.Input, entry.SessionID, timeline)
		if err != nil {
			return nil, err
		}
		result = pipeline.MergeResults(result, next)
		checkpointID = nextCheckpointID
		interrupted = nextInterrupted
	}
	if !interrupted {
		_ = rt.checkpoints.Delete(ctx, req.ResumeFromCheckpoint)
	}
	return rt.pipelineResponse(req, result, checkpointID, interrupted, timeline), nil
}

func (rt *Runtime) pipelineResponse(req Request, result pipeline.Result, checkpointID string, interrupted bool, timeline *timelineCollector) *Response {
	stopReason := "completed"
	if interrupted {
		stopReason = "interrupted"
	}
	var timelineEntries []TimelineEntry
	if timeline != nil {
		timeline.add(TimelineEntry{Kind: TimelineTokenSnapshot, Usage: &model.Usage{}})
		timelineEntries = timeline.snapshot()
	}
	return &Response{
		Mode:      req.Mode,
		RequestID: req.RequestID,
		Result: &Result{
			Output:       result.Output,
			StopReason:   stopReason,
			Artifacts:    append([]artifact.ArtifactRef(nil), result.Artifacts...),
			Lineage:      result.Lineage,
			Structured:   result.Structured,
			CheckpointID: checkpointID,
			Interrupted:  interrupted,
		},
		Timeline:        timelineEntries,
		ProjectConfig:   rt.Settings(),
		Settings:        rt.Settings(),
		SandboxSnapshot: rt.sandboxReport(),
		Tags:            maps.Clone(req.Tags),
	}
}

func (rt *Runtime) executePipelineTree(ctx context.Context, step pipeline.Step, input pipeline.Input, sessionID string, timeline *timelineCollector) (pipeline.Result, string, bool, error) {
	if step.Batch != nil {
		current := pipeline.CloneInput(input)
		var final pipeline.Result
		for i, child := range step.Batch.Steps {
			if child.Checkpoint != nil {
				checkpointResult, err := rt.newPipelineExecutor(sessionID, timeline).Execute(ctx, child.Checkpoint.Step, current)
				if err != nil {
					return pipeline.Result{}, "", false, err
				}
				final = pipeline.MergeResults(final, checkpointResult)
				current = pipeline.InputFromResult(current, checkpointResult)
				var remaining *pipeline.Step
				if i+1 < len(step.Batch.Steps) {
					remaining = &pipeline.Step{Batch: &pipeline.Batch{Steps: append([]pipeline.Step(nil), step.Batch.Steps[i+1:]...)}}
				}
				checkpointID, err := rt.checkpoints.Save(ctx, checkpoint.Entry{
					SessionID: sessionID,
					Remaining: remaining,
					Input:     current,
					Result:    final,
				})
				if err != nil {
					return pipeline.Result{}, "", false, err
				}
				if timeline != nil {
					timeline.add(TimelineEntry{Kind: TimelineCheckpointCreate, CheckpointID: checkpointID, Name: child.Checkpoint.Name})
				}
				return final, checkpointID, true, nil
			}
			result, nestedCheckpointID, interrupted, err := rt.executePipelineTree(ctx, child, current, sessionID, timeline)
			if err != nil {
				return pipeline.Result{}, "", false, err
			}
			final = pipeline.MergeResults(final, result)
			current = pipeline.InputFromResult(current, result)
			if interrupted {
				return final, nestedCheckpointID, true, nil
			}
		}
		return final, "", false, nil
	}
	if step.Checkpoint != nil {
		result, err := rt.newPipelineExecutor(sessionID, timeline).Execute(ctx, step.Checkpoint.Step, input)
		if err != nil {
			return pipeline.Result{}, "", false, err
		}
		checkpointID, err := rt.checkpoints.Save(ctx, checkpoint.Entry{
			SessionID: sessionID,
			Input:     pipeline.InputFromResult(input, result),
			Result:    result,
		})
		if err != nil {
			return pipeline.Result{}, "", false, err
		}
		if timeline != nil {
			timeline.add(TimelineEntry{Kind: TimelineCheckpointCreate, CheckpointID: checkpointID, Name: step.Checkpoint.Name})
		}
		return result, checkpointID, true, nil
	}
	result, err := rt.newPipelineExecutor(sessionID, timeline).Execute(ctx, step, input)
	if err != nil {
		return pipeline.Result{}, "", false, err
	}
	return result, "", false, nil
}

func (rt *Runtime) newPipelineExecutor(sessionID string, timeline *timelineCollector) pipeline.Executor {
	return pipeline.Executor{
		Cache: timelineCacheStore{base: rt.cacheStore, timeline: timeline},
		RunTool: func(ctx context.Context, step pipeline.Step, refs []artifact.ArtifactRef) (*tool.ToolResult, error) {
			recordTimelineInputArtifacts(timeline, step.Name, refs)
			params := cloneArguments(step.With)
			if params == nil {
				params = map[string]any{}
			}
			params["step"] = step.Name
			if len(refs) > 0 {
				params["artifacts"] = refs
			}
			if timeline != nil {
				timeline.add(TimelineEntry{Kind: TimelineToolCall, Name: step.Name})
			}
			started := time.Now()
			res, err := rt.executor.Execute(ctx, tool.Call{
				Name:      step.Tool,
				Params:    params,
				Path:      rt.sbRoot,
				Host:      "localhost",
				SessionID: sessionID,
			})
			if err != nil {
				return nil, err
			}
			if res == nil {
				return nil, nil
			}
			recordTimelineResult(timeline, step.Name, started, res.Result)
			return res.Result, nil
		},
		RunSkill: func(ctx context.Context, step pipeline.Step, refs []artifact.ArtifactRef) (*tool.ToolResult, error) {
			if rt.skReg == nil {
				return nil, fmt.Errorf("pipeline: skills registry not configured")
			}
			recordTimelineInputArtifacts(timeline, step.Name, refs)
			meta := cloneArguments(step.With)
			if meta == nil {
				meta = map[string]any{}
			}
			if len(refs) > 0 {
				meta["artifacts"] = refs
			}
			if timeline != nil {
				timeline.add(TimelineEntry{Kind: TimelineToolCall, Name: step.Name})
			}
			started := time.Now()
			res, err := rt.skReg.Execute(ctx, step.Skill, skills.ActivationContext{
				Prompt:   step.Name,
				Metadata: meta,
			})
			if err != nil {
				return nil, err
			}
			out := &tool.ToolResult{}
			switch val := res.Output.(type) {
			case string:
				out.Output = val
			default:
				out.Structured = val
			}
			if arts, ok := res.Metadata["artifacts"].([]artifact.ArtifactRef); ok {
				out.Artifacts = append([]artifact.ArtifactRef(nil), arts...)
			}
			recordTimelineResult(timeline, step.Name, started, out)
			return out, nil
		},
	}
}

func recordTimelineInputArtifacts(timeline *timelineCollector, stepName string, refs []artifact.ArtifactRef) {
	if timeline == nil {
		return
	}
	for _, ref := range refs {
		copied := ref
		timeline.add(TimelineEntry{Kind: TimelineInputArtifact, Name: stepName, Artifact: &copied})
	}
}

func recordTimelineResult(timeline *timelineCollector, stepName string, started time.Time, res *tool.ToolResult) {
	if timeline == nil || res == nil {
		return
	}
	timeline.add(TimelineEntry{Kind: TimelineToolResult, Name: stepName, Output: res.Output})
	timeline.add(TimelineEntry{Kind: TimelineLatencySnapshot, Name: stepName, Duration: time.Since(started)})
	for _, generated := range res.Artifacts {
		copied := generated
		timeline.add(TimelineEntry{Kind: TimelineGeneratedArtifact, Name: stepName, Artifact: &copied})
	}
}

type timelineCacheStore struct {
	base     runtimecache.Store
	timeline *timelineCollector
}

func (t timelineCacheStore) Load(ctx context.Context, key artifact.CacheKey) (*tool.ToolResult, bool, error) {
	if t.base == nil {
		return nil, false, nil
	}
	result, ok, err := t.base.Load(ctx, key)
	if t.timeline != nil {
		kind := TimelineCacheMiss
		if ok {
			kind = TimelineCacheHit
		}
		t.timeline.add(TimelineEntry{Kind: kind, CacheKey: string(key)})
	}
	return result, ok, err
}

func (t timelineCacheStore) Save(ctx context.Context, key artifact.CacheKey, result *tool.ToolResult) error {
	if t.base == nil {
		return nil
	}
	return t.base.Save(ctx, key, result)
}
