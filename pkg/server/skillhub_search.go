// skillhub_search.go: keyword search endpoint with limit-based pagination.
package server

import (
	"context"
	"strings"
)

func (h *Handler) handleSkillhubSearch(ctx context.Context, req Request) Response {
	q, _ := req.Params["q"].(string)
	if strings.TrimSpace(q) == "" {
		return h.invalidParams(req.ID, "q is required")
	}
	limit := 20
	if v, ok := req.Params["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	cfg, err := h.loadSkillhubConfig()
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	client, err := h.newSkillhubClient(cfg.Resolved())
	if err != nil {
		return h.invalidParams(req.ID, err.Error())
	}
	rpcCtx, cancel := context.WithTimeout(ctx, skillhubDefaultRPCTimeout)
	defer cancel()
	res, err := client.Search(rpcCtx, q, limit)
	if err != nil {
		return h.internalError(req.ID, err.Error())
	}
	return h.success(req.ID, res)
}
