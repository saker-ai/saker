package toolbuiltin

import (
	"fmt"
	"sync"
)

// canvasImageBudgetPerThread caps the cumulative image bytes a single
// chat thread can attach via canvas_get_node across one process lifetime.
// 20 MiB is roughly four max-size canvas attachments (canvasMaxImageBytes
// is 5 MiB). Past that, a confused model is almost certainly in a fetch-
// loop regression rather than doing useful work — refusing the next
// attachment forces it to either summarize what it already saw or stop.
const canvasImageBudgetPerThread = 20 << 20 // 20 MiB

// canvasImageBudget tracks per-thread cumulative bytes attached as image
// ContentBlocks via canvas_get_node. The budget defends against runaway
// fetch loops where a model repeatedly re-reads the same large attachment
// and bloats its own context to the point of failure (eddaff17 incident).
//
// The tracker is process-global by design: even though chat threads are
// long-lived, the budget exists to bound a single agent run, not user
// behaviour over weeks. A run that legitimately needs >20 MiB of canvas
// imagery is itself the bug we want to surface.
//
// All methods are safe for concurrent use.
type canvasImageBudget struct {
	mu        sync.Mutex
	perThread map[string]int64
}

// newCanvasImageBudget returns a fresh tracker. Tools that share an
// instance share the same per-thread accounting; tools constructed
// independently (e.g. one per HTTP runtime) keep separate budgets.
func newCanvasImageBudget() *canvasImageBudget {
	return &canvasImageBudget{perThread: map[string]int64{}}
}

// Reserve records a planned attachment of `bytes` bytes for `threadID`.
// Returns nil when the attachment fits, or an error explaining the cap
// when it would not. On error the byte count is NOT incremented so the
// thread can still attach smaller items later.
//
// Empty threadID is allowed and bypasses budgeting — there's nothing to
// scope a per-thread cap to. Caller policy determines whether unscoped
// reads are allowed at all (canvas_get_node already requires threadID).
func (b *canvasImageBudget) Reserve(threadID string, bytes int64) error {
	if b == nil {
		return nil
	}
	if threadID == "" || bytes <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.perThread == nil {
		b.perThread = map[string]int64{}
	}
	used := b.perThread[threadID]
	if used+bytes > canvasImageBudgetPerThread {
		return fmt.Errorf(
			"per-thread image budget exceeded: %d bytes already attached, this attachment (+%d) would exceed the %d-byte cap",
			used, bytes, canvasImageBudgetPerThread,
		)
	}
	b.perThread[threadID] = used + bytes
	return nil
}

// Used returns the bytes currently attributed to threadID. Useful for
// tests and diagnostic logging.
func (b *canvasImageBudget) Used(threadID string) int64 {
	if b == nil || threadID == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.perThread[threadID]
}
