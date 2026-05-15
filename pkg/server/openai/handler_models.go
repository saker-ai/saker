package openai

import (
	"net/http"
	"time"

	"github.com/saker-ai/saker/pkg/api"
	"github.com/gin-gonic/gin"
)

// ModelObject mirrors OpenAI's `model` envelope.
type ModelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelListResponse is the GET /v1/models payload shape.
type ModelListResponse struct {
	Object string        `json:"object"`
	Data   []ModelObject `json:"data"`
}

// modelsCreatedAt is the static "created" timestamp returned for every
// listed model. Using one constant keeps the response cacheable and
// avoids implying false freshness — saker doesn't track per-model
// release dates.
var modelsCreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()

// handleModels returns a static OpenAI model list mapping the saker
// ModelTier values to OpenAI-compatible IDs. Clients use this to
// populate UI pickers; the actual provider-side model is resolved from
// saker settings, not from this list.
func (g *Gateway) handleModels(c *gin.Context) {
	tiers := []api.ModelTier{
		api.ModelTierLow,
		api.ModelTierMid,
		api.ModelTierHigh,
	}
	data := make([]ModelObject, 0, len(tiers)+1)
	// "saker-default" lets clients leave the picker on a stable name
	// without hard-coding a tier; the gateway resolves it to whichever
	// tier saker's defaultModel setting picks.
	data = append(data, ModelObject{
		ID:      "saker-default",
		Object:  "model",
		Created: modelsCreatedAt,
		OwnedBy: "saker",
	})
	for _, t := range tiers {
		data = append(data, ModelObject{
			ID:      "saker-" + string(t),
			Object:  "model",
			Created: modelsCreatedAt,
			OwnedBy: "saker",
		})
	}
	c.JSON(http.StatusOK, ModelListResponse{Object: "list", Data: data})
}
