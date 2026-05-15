package openai

import (
	"errors"
	"net/http"

	"github.com/saker-ai/saker/pkg/runhub"
	"github.com/gin-gonic/gin"
)

// handleRunsCancel serves DELETE /v1/runs/:id — cancels an in-flight
// run by id. Idempotent: cancelling an already-terminal run still
// returns 204 because the operation's *intent* (the run is no longer
// running for the caller) holds.
//
// Tenant scoping mirrors the reconnect endpoint: a cross-tenant cancel
// is reported as 404 (not 403) so we never confirm the existence of
// someone else's run id. The unknown-id case returns the same 404 for
// the same existence-leak-prevention reason.
//
// Response shape:
//   - 204 No Content on success (no body)
//   - 404 + OpenAI error envelope when the id is unknown / cross-tenant
//   - 500 + envelope when the underlying store transport breaks
//
// The handler does NOT call hub.Remove. The terminal run row sticks
// around until the GC retention window elapses so a client polling
// /v1/runs/:id/events still sees the final cancelled status event.
func (g *Gateway) handleRunsCancel(c *gin.Context) {
	runID := c.Param("id")
	if runID == "" {
		InvalidRequest(c, "missing run id")
		return
	}

	hubRun, err := g.hub.Get(runID)
	if err != nil {
		// runhub.ErrNotFound → 404; any other error is operator-visible.
		if errors.Is(err, runhub.ErrNotFound) {
			NotFound(c, "no such run")
			return
		}
		ServerError(c, "failed to load run: "+err.Error())
		return
	}

	identity := IdentityFromContext(c.Request.Context())
	if !runOwnedByIdentity(hubRun, identity) {
		// Don't leak existence — same shape as the unknown-id path.
		NotFound(c, "no such run")
		return
	}

	if err := g.hub.Cancel(runID); err != nil {
		// Cancel already validated existence above; the only failure
		// path here is a race with GC eviction. Treat that as 404 so
		// the caller's mental model ("the run is gone") matches.
		if errors.Is(err, runhub.ErrNotFound) {
			NotFound(c, "no such run")
			return
		}
		ServerError(c, "failed to cancel run: "+err.Error())
		return
	}

	c.Status(http.StatusNoContent)
}
