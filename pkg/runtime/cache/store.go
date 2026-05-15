package cache

import (
	"context"

	"github.com/saker-ai/saker/pkg/artifact"
	"github.com/saker-ai/saker/pkg/tool"
)

// Store persists pipeline results keyed by deterministic artifact cache keys.
type Store interface {
	Load(context.Context, artifact.CacheKey) (*tool.ToolResult, bool, error)
	Save(context.Context, artifact.CacheKey, *tool.ToolResult) error
}
