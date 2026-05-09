package cache

import (
	"context"

	"github.com/cinience/saker/pkg/artifact"
	"github.com/cinience/saker/pkg/tool"
)

// Store persists pipeline results keyed by deterministic artifact cache keys.
type Store interface {
	Load(context.Context, artifact.CacheKey) (*tool.ToolResult, bool, error)
	Save(context.Context, artifact.CacheKey, *tool.ToolResult) error
}
