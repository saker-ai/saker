package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/runtime/cache"
	"github.com/cinience/saker/pkg/tool"
	"golang.org/x/sync/errgroup"
)

// Input carries the runtime inputs used for pipeline execution.
type Input struct {
	Artifacts   []artifact.ArtifactRef
	Collections map[string][]artifact.ArtifactRef
	Items       []Result
}

// Result captures the output of executing a pipeline step.
type Result struct {
	Output     string
	Summary    string
	Artifacts  []artifact.ArtifactRef
	Structured any
	Preview    *tool.Preview
	Items      []Result
	Lineage    artifact.LineageGraph
}

// Executor runs lightweight multimodal pipeline steps against injected runtime surfaces.
type Executor struct {
	RunTool  func(context.Context, Step, []artifact.ArtifactRef) (*tool.ToolResult, error)
	RunSkill func(context.Context, Step, []artifact.ArtifactRef) (*tool.ToolResult, error)
	Cache    cache.Store
}

// Execute runs a declared pipeline step and returns its aggregated result.
func (e Executor) Execute(ctx context.Context, step Step, input Input) (Result, error) {
	return e.execute(ctx, step, input)
}

func (e Executor) execute(ctx context.Context, step Step, input Input) (Result, error) {
	switch {
	case step.Batch != nil:
		return e.executeBatch(ctx, *step.Batch, input)
	case step.FanOut != nil:
		return e.executeFanOut(ctx, *step.FanOut, input)
	case step.FanIn != nil:
		return e.executeFanIn(*step.FanIn, input), nil
	case step.Retry != nil:
		return e.executeRetry(ctx, *step.Retry, input)
	case step.Checkpoint != nil:
		return e.execute(ctx, step.Checkpoint.Step, input)
	case step.Conditional != nil:
		return Result{}, fmt.Errorf("pipeline: conditional steps are not yet supported; validate pipeline before execution (step %q)", step.Name)
	default:
		return e.executeLeaf(ctx, step, input)
	}
}

func (e Executor) executeBatch(ctx context.Context, batch Batch, input Input) (Result, error) {
	current := CloneInput(input)
	var final Result
	for _, child := range batch.Steps {
		result, err := e.execute(ctx, child, current)
		if err != nil {
			return Result{}, err
		}
		final = MergeResults(final, result)
		current.Items = cloneResults(result.Items)
		if len(result.Artifacts) > 0 {
			current.Artifacts = append([]artifact.ArtifactRef(nil), result.Artifacts...)
		}
	}
	return final, nil
}

func (e Executor) executeFanOut(ctx context.Context, fanOut FanOut, input Input) (Result, error) {
	refs := input.Collections[fanOut.Collection]
	if len(refs) == 0 && fanOut.Collection == "" {
		refs = input.Artifacts
	}
	if len(refs) == 0 {
		return Result{}, nil
	}

	concurrency := fanOut.Concurrency
	if concurrency <= 0 {
		concurrency = len(refs)
	}

	results := make([]Result, len(refs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)

	for i, ref := range refs {
		i, ref := i, ref
		g.Go(func() error {
			child, err := e.execute(gctx, fanOut.Step, Input{
				Artifacts:   []artifact.ArtifactRef{ref},
				Collections: CloneCollections(input.Collections),
			})
			if err != nil {
				return err
			}
			results[i] = child
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return Result{}, err
	}

	out := Result{}
	for _, r := range results {
		out.Items = append(out.Items, r)
		out.Artifacts = append(out.Artifacts, r.Artifacts...)
		out.Lineage.Edges = append(out.Lineage.Edges, r.Lineage.Edges...)
	}
	return out, nil
}

func (e Executor) executeFanIn(fanIn FanIn, input Input) Result {
	values := make([]string, 0, len(input.Items))
	for _, item := range input.Items {
		values = append(values, item.Output)
	}
	return Result{
		Structured: map[string]any{
			fanIn.Into: values,
		},
		Items: cloneResults(input.Items),
	}
}

func (e Executor) executeRetry(ctx context.Context, retry Retry, input Input) (Result, error) {
	attempts := retry.Attempts
	if attempts <= 0 {
		attempts = 1
	}
	backoff := time.Duration(retry.BackoffMs) * time.Millisecond
	const maxBackoff = 30 * time.Second
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 && backoff > 0 {
			wait := backoff << (i - 1)
			if wait > maxBackoff {
				wait = maxBackoff
			}
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(wait):
			}
		}
		result, err := e.execute(ctx, retry.Step, input)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	return Result{}, lastErr
}

func (e Executor) executeLeaf(ctx context.Context, step Step, input Input) (Result, error) {
	refs := input.Artifacts
	if len(step.Input) > 0 {
		refs = step.Input
	}
	cacheKey := e.cacheKey(step, refs)
	if e.Cache != nil && cacheKey != "" {
		if cached, ok, err := e.Cache.Load(ctx, cacheKey); err != nil {
			return Result{}, err
		} else if ok {
			return toolResultToPipelineResult(cached, refs, step.Name), nil
		}
	}

	var (
		res *tool.ToolResult
		err error
	)
	switch {
	case step.Tool != "":
		if e.RunTool == nil {
			return Result{}, fmt.Errorf("pipeline: tool runner not configured for step %q", step.Name)
		}
		res, err = e.RunTool(ctx, step, refs)
	case step.Skill != "":
		if e.RunSkill == nil {
			return Result{}, fmt.Errorf("pipeline: skill runner not configured for step %q", step.Name)
		}
		res, err = e.RunSkill(ctx, step, refs)
	default:
		return Result{}, fmt.Errorf("pipeline: step %q has no executable target", step.Name)
	}
	if err != nil {
		return Result{}, err
	}
	if res == nil {
		return Result{}, nil
	}

	result := Result{
		Output:     res.Output,
		Summary:    res.Summary,
		Artifacts:  append([]artifact.ArtifactRef(nil), res.Artifacts...),
		Structured: res.Structured,
		Preview:    res.Preview,
	}
	for _, src := range refs {
		for _, derived := range result.Artifacts {
			result.Lineage.AddEdge(src, derived, step.Name)
		}
	}
	if e.Cache != nil && cacheKey != "" {
		if err := e.Cache.Save(ctx, cacheKey, pipelineResultToToolResult(result)); err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

func (e Executor) cacheKey(step Step, refs []artifact.ArtifactRef) artifact.CacheKey {
	target := step.Tool
	if target == "" {
		target = step.Skill
	}
	if target == "" {
		return ""
	}
	return artifact.NewCacheKey(target, step.With, refs)
}

func toolResultToPipelineResult(res *tool.ToolResult, refs []artifact.ArtifactRef, stepName string) Result {
	if res == nil {
		return Result{}
	}
	result := Result{
		Output:     res.Output,
		Summary:    res.Summary,
		Artifacts:  append([]artifact.ArtifactRef(nil), res.Artifacts...),
		Structured: res.Structured,
		Preview:    res.Preview,
	}
	for _, src := range refs {
		for _, derived := range result.Artifacts {
			result.Lineage.AddEdge(src, derived, stepName)
		}
	}
	return result
}

func pipelineResultToToolResult(result Result) *tool.ToolResult {
	return &tool.ToolResult{
		Output:     result.Output,
		Summary:    result.Summary,
		Artifacts:  append([]artifact.ArtifactRef(nil), result.Artifacts...),
		Structured: result.Structured,
		Preview:    result.Preview,
	}
}

func MergeResults(base, next Result) Result {
	if next.Output != "" {
		base.Output = next.Output
	}
	if next.Summary != "" {
		base.Summary = next.Summary
	}
	if next.Structured != nil {
		base.Structured = next.Structured
	}
	if next.Preview != nil {
		base.Preview = next.Preview
	}
	if len(next.Artifacts) > 0 {
		base.Artifacts = append([]artifact.ArtifactRef(nil), next.Artifacts...)
	}
	if len(next.Items) > 0 {
		base.Items = cloneResults(next.Items)
	}
	base.Lineage.Edges = append(base.Lineage.Edges, next.Lineage.Edges...)
	return base
}

func CloneInput(in Input) Input {
	return Input{
		Artifacts:   append([]artifact.ArtifactRef(nil), in.Artifacts...),
		Collections: CloneCollections(in.Collections),
		Items:       cloneResults(in.Items),
	}
}

func CloneCollections(in map[string][]artifact.ArtifactRef) map[string][]artifact.ArtifactRef {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]artifact.ArtifactRef, len(in))
	for key, refs := range in {
		out[key] = append([]artifact.ArtifactRef(nil), refs...)
	}
	return out
}

func cloneResults(in []Result) []Result {
	if len(in) == 0 {
		return nil
	}
	out := make([]Result, len(in))
	copy(out, in)
	return out
}

// InputFromResult derives the next pipeline input from the previous input and a step result.
func InputFromResult(previous Input, result Result) Input {
	next := CloneInput(previous)
	next.Items = append([]Result(nil), result.Items...)
	if len(result.Artifacts) > 0 {
		next.Artifacts = append([]artifact.ArtifactRef(nil), result.Artifacts...)
	}
	return next
}

// CloneResult returns a shallow-safe copy of a pipeline result with independent slices.
func CloneResult(r Result) Result {
	out := r
	if len(r.Artifacts) > 0 {
		out.Artifacts = append([]artifact.ArtifactRef(nil), r.Artifacts...)
	}
	if len(r.Items) > 0 {
		out.Items = make([]Result, len(r.Items))
		copy(out.Items, r.Items)
	}
	if len(r.Lineage.Edges) > 0 {
		out.Lineage.Edges = append([]artifact.LineageEdge(nil), r.Lineage.Edges...)
	}
	return out
}
