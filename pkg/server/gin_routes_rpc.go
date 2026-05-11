package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// registerRPCRoutes wires the RPC-over-HTTP adapter onto a gin router
// group. The JSON-RPC method name lives in the URL path and may contain
// slashes (e.g. "thread/list", "settings/get") so we keep the wildcard
// catch-all here — but the dispatcher itself is now a thin gin handler
// that pulls the method out of c.Param instead of strings.TrimPrefix.
//
//	POST /api/rpc/*method   → method dispatched through Handler.HandleRequest
//
// A 10MB body limit is applied because some methods accept big media
// payloads (canvas/document, project/import).
func (s *Server) registerRPCRoutes(authed *gin.RouterGroup) {
	grp := authed.Group("/api/rpc", BodySizeLimitMiddleware(10*1024*1024))
	grp.POST("/*method", s.ginRPCDispatch())
	// gin returns 404 for non-POST verbs on /api/rpc/<anything> when
	// HandleMethodNotAllowed is off; the engine sets it on globally so
	// non-POST requests return 405 to match the legacy contract.
}

// ginRPCDispatch is the gin handler that replaces handleRPCREST. It
// extracts the method name from the route param (no manual TrimPrefix /
// Trim of leading slashes) and hands off to the existing JSON-RPC plumbing.
func (s *Server) ginRPCDispatch() gin.HandlerFunc {
	return func(c *gin.Context) {
		// gin's *method wildcard always starts with "/" — strip it.
		method := strings.Trim(c.Param("method"), "/")
		// Reuse the shared dispatcher; it handles method validation,
		// streaming-method rejection, JSON envelope marshalling, and
		// the rpc-error → http-status mapping.
		s.dispatchRPCREST(c.Writer, c.Request, method)
	}
}

// dispatchRPCREST contains the body of the legacy handleRPCREST without the
// path parsing. It still takes (w, r) so unit tests can drive it directly
// and so the same code services any future HTTP entry point that wants to
// adapt JSON-RPC over a different URL shape.
func (s *Server) dispatchRPCREST(w http.ResponseWriter, r *http.Request, method string) {
	if method == "" {
		http.Error(w, "missing method", http.StatusBadRequest)
		return
	}

	if methodsRequireWebsocket[method] {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"code":    ErrCodeMethodNotFound,
			"message": "method requires websocket: " + method,
		})
		return
	}

	params, err := decodeRPCParams(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    ErrCodeInvalidParams,
			"message": "invalid JSON body: " + err.Error(),
		})
		return
	}

	// HTTP requests don't register a subscriber, so the clientID is just a
	// per-request token used to satisfy handler signatures. The "http-"
	// prefix makes leaked log lines easy to spot vs real ws clients.
	clientID := "http-" + uuid.New().String()

	req := Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	}
	resp := s.handler.HandleRequest(r.Context(), clientID, req)

	if resp.Error != nil {
		writeJSON(w, httpStatusForRPCError(resp.Error.Code), map[string]any{
			"code":    resp.Error.Code,
			"message": resp.Error.Message,
		})
		return
	}
	writeJSON(w, http.StatusOK, resp.Result)
}
