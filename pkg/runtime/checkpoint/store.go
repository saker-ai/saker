package checkpoint

import (
	"context"
	"errors"
	"time"

	"github.com/cinience/saker/pkg/pipeline"
)

var ErrNotFound = errors.New("checkpoint: not found")

// ErrStoreNil is returned when a nil checkpoint store receives a call.
var ErrStoreNil = errors.New("checkpoint: store is nil")

// Entry captures the resumable state after a pipeline interruption.
type Entry struct {
	ID        string
	SessionID string
	Remaining *pipeline.Step
	Input     pipeline.Input
	Result    pipeline.Result
	CreatedAt time.Time
}

// Store persists resumable checkpoint state for pipeline-backed runs.
type Store interface {
	Save(context.Context, Entry) (string, error)
	Load(context.Context, string) (Entry, error)
	Delete(context.Context, string) error
}
