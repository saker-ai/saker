package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
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
//
// Routes are registered onto the gin engine in registerRPCRoutes
// (gin_routes_rpc.go); the dispatcher body lives in dispatchRPCREST there.
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
