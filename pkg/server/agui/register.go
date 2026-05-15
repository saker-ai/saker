package agui

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/saker-ai/saker/pkg/api"
	"github.com/saker-ai/saker/pkg/conversation"
	"github.com/saker-ai/saker/pkg/project"
)

// Runner is the narrow stream-execution interface the gateway needs.
// *api.Runtime satisfies it directly.
type Runner interface {
	RunStream(ctx context.Context, req api.Request) (<-chan api.StreamEvent, error)
}

// Deps bundles the runtime dependencies for the AG-UI gateway.
type Deps struct {
	Runtime           Runner
	ProjectStore      *project.Store
	ConversationStore *conversation.Store
	Logger            *slog.Logger
	Options           Options
	SessionValidator  func(c *gin.Context) (username, role string, ok bool)
}

// Options holds operator-configurable settings for the AG-UI gateway.
type Options struct {
	Enabled       bool
	DevBypassAuth bool
}

// Gateway carries the runtime dependencies for AG-UI HTTP handlers.
type Gateway struct {
	deps Deps
	hitl *hitlRegistry
}

// RegisterAGUIGateway mounts the AG-UI protocol endpoints on the supplied
// Gin engine and returns the Gateway handle.
//
// Returns (nil, nil) when Options.Enabled is false.
func RegisterAGUIGateway(engine *gin.Engine, deps Deps) (*Gateway, error) {
	if !deps.Options.Enabled {
		return nil, nil
	}
	if engine == nil {
		return nil, errors.New("agui-gw: gin engine is nil")
	}
	if deps.Runtime == nil {
		return nil, errors.New("agui-gw: runtime is required")
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}

	g := &Gateway{deps: deps, hitl: newHITLRegistry()}

	agents := engine.Group("/v1/agents")
	agents.Use(g.authMiddleware())
	{
		agents.POST("/run", g.handleRun)
		agents.POST("/run/agent/:agentId/run", g.handleRun)
		agents.GET("/run/info", g.handleInfo)
		agents.POST("/run/info", g.handleInfo)
		agents.GET("/run/threads", g.handleThreads)
		agents.POST("/run/:runId/approval", g.handleApprovalRespond)
		agents.POST("/run/:runId/answer", g.handleQuestionRespond)
	}

	deps.Logger.Info("agui gateway mounted",
		"dev_bypass", deps.Options.DevBypassAuth,
	)

	return g, nil
}
