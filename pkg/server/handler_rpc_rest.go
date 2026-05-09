package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// rpcRESTPath is the URL prefix for the generic RPC-over-HTTP adapter.
//
// POST /api/rpc/{method} — body is the JSON-RPC params object (or empty),
// the response body is the bare result on success, or {code,message} on error.
//
// The adapter exists so the web client can serve its bootstrap reads
// (project/list, thread/list, settings/get, ...) without forcing a
// WebSocket connection on page load. Streaming methods (turn/send,
// thread/subscribe, ...) are rejected here because their semantics are
// bound to a long-lived clientID — they must keep going through /ws.
const rpcRESTPath = "/api/rpc/"

// methodsRequireWebsocket lists JSON-RPC methods whose return value
// or side-effects depend on a registered WebSocket subscriber. They
// short-circuit with HTTP 405 from the REST adapter.
var methodsRequireWebsocket = map[string]bool{
	"turn/send":          true,
	"thread/subscribe":   true,
	"thread/unsubscribe": true,
	"thread/interrupt":   true,
	"turn/cancel":        true,
	"approval/respond":   true,
	"question/respond":   true,
}

// handleRPCREST translates an HTTP request into a JSON-RPC envelope, runs
// it through the same Handler.HandleRequest pipeline as /ws (so scope
// resolution, RBAC, leak detection, and every handler implementation are
// reused verbatim), then translates the JSON-RPC response back to HTTP.
//
// @Summary RPC over HTTP adapter
// @Description Translates HTTP POST requests into JSON-RPC calls and returns the result. Streaming methods (turn/send, thread/subscribe, etc.) are rejected because they require a WebSocket connection. Body is the JSON-RPC params object; URL path contains the method name.
// @Tags rpc
// @Accept json
// @Produce json
// @Param method path string true "JSON-RPC method name (e.g. project/list, settings/get)"
// @Param body body object false "JSON-RPC params object (or empty)"
// @Success 200 {object} map[string]any "JSON-RPC result"
// @Failure 400 {object} map[string]any "invalid JSON body or missing method"
// @Failure 405 {object} map[string]any "POST required or method requires websocket"
// @Failure 404 {object} map[string]any "method not found"
// @Failure 500 {object} map[string]any "internal error"
// @Router /api/rpc/{method} [post]
func (s *Server) handleRPCREST(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	method := strings.TrimPrefix(r.URL.Path, rpcRESTPath)
	method = strings.Trim(method, "/")
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

// decodeRPCParams reads the request body as a JSON object. Empty body
// or "null" → empty map. Anything else that doesn't unmarshal to an
// object (broken JSON, an array, a scalar) is rejected: every existing
// handler reads only named fields off Params, so a non-object body
// would silently become a no-op and hide the real client bug.
func decodeRPCParams(body io.Reader) (map[string]any, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return map[string]any{}, nil
	}
	var asObject map[string]any
	if err := json.Unmarshal([]byte(trimmed), &asObject); err != nil {
		return nil, err
	}
	if asObject == nil {
		asObject = map[string]any{}
	}
	return asObject, nil
}

// httpStatusForRPCError maps JSON-RPC error codes onto HTTP status codes.
// Codes are defined in pkg/server/types.go and pkg/server/middleware_scope.go.
func httpStatusForRPCError(code int) int {
	switch code {
	case ErrCodeUnauthorized: // -32002
		return http.StatusUnauthorized
	case ErrCodeProjectMissing: // -32003
		return http.StatusBadRequest
	case ErrCodeProjectAccess: // -32004
		return http.StatusForbidden
	case ErrCodeProjectStore: // -32005
		return http.StatusInternalServerError
	case ErrCodeMethodNotFound: // -32601
		return http.StatusNotFound
	case ErrCodeInvalidParams: // -32602
		return http.StatusBadRequest
	case ErrCodeInvalidRequest: // -32600
		return http.StatusBadRequest
	case ErrCodeParse: // -32700
		return http.StatusBadRequest
	case ErrCodeInternal: // -32603
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}
