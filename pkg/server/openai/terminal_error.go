package openai

import (
	"io"

	"github.com/cinience/saker/pkg/runhub"
)

// writeTerminalErrorIfNeeded inspects the run's terminal status after
// the subscriber chan closes and, for any non-completed termination,
// emits an OpenAI-shaped error envelope as a standard SSE
// `event: error` frame. Caller writes `[DONE]` immediately after — the
// frame just annotates *why* the stream ended.
//
// Mapping (per plan §E.4):
//
//	RunStatusCancelled → code=run_cancelled  (client_disconnect or DELETE)
//	RunStatusExpired   → code=run_expired    (retention window passed)
//	RunStatusFailed    → code=run_failed     (internal error)
//	RunStatusCompleted → no frame (clean end-of-stream)
//	any other status   → no frame (defensive: chan close on a non-terminal
//	                     run shouldn't happen, but we don't synthesize an
//	                     error in case it ever does)
//
// Returned error is the underlying io.Writer error (client disconnect
// mid-write); callers can ignore it because the chan-close path is
// already terminating the response.
func writeTerminalErrorIfNeeded(w io.Writer, hubRun *runhub.Run) error {
	if hubRun == nil {
		return nil
	}
	var (
		code    string
		message string
	)
	switch hubRun.Status() {
	case runhub.RunStatusCancelled:
		code = "run_cancelled"
		message = "run was cancelled (client disconnect or explicit DELETE)"
	case runhub.RunStatusExpired:
		code = "run_expired"
		message = "run aged out before completion (retention window elapsed)"
	case runhub.RunStatusFailed:
		code = "run_failed"
		message = "run failed with an internal error"
	default:
		return nil
	}
	return WriteErrorEvent(w, ErrorEnvelope{Error: ErrorPayload{
		Type:    ErrTypeAPI,
		Code:    code,
		Message: message,
	}})
}
