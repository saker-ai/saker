package pipeline_test

import (
	"context"
	"testing"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/pipeline"
	"github.com/saker-ai/saker/pkg/runtime/checkpoint"
	"github.com/saker-ai/saker/pkg/tool"
	"github.com/stretchr/testify/require"
)

func TestCheckpoint_CreateAndResume(t *testing.T) {
	store := checkpoint.NewMemoryStore()
	rt := newTestRuntime(t, func(o *api.Options) {
		o.CheckpointStore = store
	})

	const sid = "test-session"

	// Run pipeline with checkpoint — should interrupt
	resp, err := rt.Run(context.Background(), api.Request{
		SessionID: sid,
		Pipeline: &pipeline.Step{
			Checkpoint: &pipeline.Checkpoint{
				Name: "mid-point",
				Step: pipeline.Step{Name: "echo-cp", Tool: "echo", With: map[string]any{"text": "checkpoint-data"}},
			},
		},
	})
	require.NoError(t, err)
	require.True(t, resp.Result.Interrupted, "checkpoint should interrupt")
	requireCheckpoint(t, resp)
	cpID := resp.Result.CheckpointID

	// Resume from checkpoint
	resp2, err := rt.Run(context.Background(), api.Request{
		SessionID:            sid,
		ResumeFromCheckpoint: cpID,
	})
	require.NoError(t, err)
	require.NotNil(t, resp2.Result)
}

func TestCheckpoint_MemoryStore(t *testing.T) {
	store := checkpoint.NewMemoryStore()
	rt := newTestRuntime(t, func(o *api.Options) {
		o.CheckpointStore = store
	})

	const sid = "mem-store-session"

	resp, err := rt.Run(context.Background(), api.Request{
		SessionID: sid,
		Pipeline: &pipeline.Step{
			Batch: &pipeline.Batch{
				Steps: []pipeline.Step{
					{Name: "step1", Tool: "echo", With: map[string]any{"text": "one"}},
					{
						Checkpoint: &pipeline.Checkpoint{
							Name: "after-step1",
							Step: pipeline.Step{Name: "step2", Tool: "echo", With: map[string]any{"text": "two"}},
						},
					},
					{Name: "step3", Tool: "echo", With: map[string]any{"text": "three"}},
				},
			},
		},
	})
	require.NoError(t, err)
	require.True(t, resp.Result.Interrupted)
	cpID := resp.Result.CheckpointID

	// Resume — should execute step3
	resp2, err := rt.Run(context.Background(), api.Request{
		SessionID:            sid,
		ResumeFromCheckpoint: cpID,
	})
	require.NoError(t, err)
	requireOutput(t, resp2, "three")
	requireNotInterrupted(t, resp2)
}

func TestCheckpoint_FileStore(t *testing.T) {
	dir := t.TempDir()
	store, err := checkpoint.NewFileStore(dir + "/cp.json")
	require.NoError(t, err)

	const sid = "file-store-session"

	rt := newTestRuntime(t, func(o *api.Options) {
		o.CheckpointStore = store
	})

	resp, err := rt.Run(context.Background(), api.Request{
		SessionID: sid,
		Pipeline: &pipeline.Step{
			Checkpoint: &pipeline.Checkpoint{
				Name: "file-cp",
				Step: pipeline.Step{Name: "echo", Tool: "echo", With: map[string]any{"text": "persisted"}},
			},
		},
	})
	require.NoError(t, err)
	require.True(t, resp.Result.Interrupted)
	cpID := resp.Result.CheckpointID

	// Simulate restart: create new FileStore from same path
	store2, err := checkpoint.NewFileStore(dir + "/cp.json")
	require.NoError(t, err)
	rt2 := newTestRuntime(t, func(o *api.Options) {
		o.CheckpointStore = store2
	})

	resp2, err := rt2.Run(context.Background(), api.Request{
		SessionID:            sid,
		ResumeFromCheckpoint: cpID,
	})
	require.NoError(t, err)
	require.NotNil(t, resp2.Result)
}

func TestCheckpoint_SessionIsolation(t *testing.T) {
	store := checkpoint.NewMemoryStore()
	rt := newTestRuntime(t, func(o *api.Options) {
		o.CheckpointStore = store
	})

	resp, err := rt.Run(context.Background(), api.Request{
		SessionID: "session-A",
		Pipeline: &pipeline.Step{
			Checkpoint: &pipeline.Checkpoint{
				Name: "isolated",
				Step: pipeline.Step{Name: "echo", Tool: "echo", With: map[string]any{"text": "A"}},
			},
		},
	})
	require.NoError(t, err)
	require.True(t, resp.Result.Interrupted)
	cpID := resp.Result.CheckpointID

	// Resume with different session should fail
	_, err = rt.Run(context.Background(), api.Request{
		SessionID:            "session-B",
		ResumeFromCheckpoint: cpID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session")
}

func TestCheckpoint_ResumeNonexistent(t *testing.T) {
	store := checkpoint.NewMemoryStore()
	rt := newTestRuntime(t, func(o *api.Options) {
		o.CheckpointStore = store
	})

	_, err := rt.Run(context.Background(), api.Request{
		ResumeFromCheckpoint: "nonexistent-id",
	})
	require.Error(t, err)
}

func TestCheckpoint_BatchWithCheckpoint_SkipsCompleted(t *testing.T) {
	ct := &counterTool{}
	store := checkpoint.NewMemoryStore()
	rt := newTestRuntime(t, func(o *api.Options) {
		o.CheckpointStore = store
		o.CustomTools = []tool.Tool{echoTool{}, ct}
	})

	const sid = "batch-cp-session"

	// Batch: step1 → checkpoint(step2) → step3
	resp, err := rt.Run(context.Background(), api.Request{
		SessionID: sid,
		Pipeline: &pipeline.Step{
			Batch: &pipeline.Batch{
				Steps: []pipeline.Step{
					{Name: "s1", Tool: "counter"},
					{
						Checkpoint: &pipeline.Checkpoint{
							Name: "cp-mid",
							Step: pipeline.Step{Name: "s2", Tool: "counter"},
						},
					},
					{Name: "s3", Tool: "counter"},
				},
			},
		},
	})
	require.NoError(t, err)
	require.True(t, resp.Result.Interrupted)
	countAfterCP := ct.count.Load()
	require.Equal(t, int64(2), countAfterCP, "should have executed s1 and s2")

	cpID := resp.Result.CheckpointID
	resp2, err := rt.Run(context.Background(), api.Request{
		SessionID:            sid,
		ResumeFromCheckpoint: cpID,
	})
	require.NoError(t, err)
	requireNotInterrupted(t, resp2)
	require.Equal(t, int64(3), ct.count.Load(), "resume should only execute s3")
}
