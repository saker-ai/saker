package openai

import (
	"context"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// rateLimiter is a per-tenant token-bucket limiter that the gateway
// applies after authMiddleware. The bucket size is RPS (events / second)
// with a small burst headroom; idle visitors are evicted after 10
// minutes so the map doesn't grow without bound when keys churn.
//
// Why a separate limiter (vs reusing BearerRateLimitMiddleware in
// pkg/server): the saker-wide bearer limiter keys by client IP, but the
// gateway has a richer notion of "tenant" (the API key id resolved by
// authMiddleware), and we want the limit to follow the key — not the
// laptop a user happens to be on.
type rateLimiter struct {
	rps   float64
	burst int

	mu       sync.Mutex
	visitors map[string]*rateVisitor

	stopCh chan struct{}
}

type rateVisitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// newRateLimiter constructs a limiter and starts its background eviction
// goroutine. Call Close() at shutdown to stop the goroutine.
//
// rps <= 0 returns nil — callers should treat nil as "rate limiting
// disabled" and skip mounting the middleware entirely.
func newRateLimiter(ctx context.Context, rps float64) *rateLimiter {
	if rps <= 0 {
		return nil
	}
	burst := int(rps)
	if burst < 5 {
		burst = 5
	}
	rl := &rateLimiter{
		rps:      rps,
		burst:    burst,
		visitors: make(map[string]*rateVisitor),
		stopCh:   make(chan struct{}),
	}
	go rl.gcLoop(ctx)
	return rl
}

func (rl *rateLimiter) gcLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.evictIdle(10 * time.Minute)
		}
	}
}

func (rl *rateLimiter) evictIdle(maxIdle time.Duration) {
	cutoff := time.Now().Add(-maxIdle)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for k, v := range rl.visitors {
		if v.lastSeen.Before(cutoff) {
			delete(rl.visitors, k)
		}
	}
}

// Allow records a hit for tenantKey and returns true when the token
// bucket admits the request. Empty tenantKey is treated as "anonymous"
// (a single shared bucket) — should only happen when authMiddleware is
// in dev-bypass mode and the operator chose to leave RPSPerTenant set.
func (rl *rateLimiter) Allow(tenantKey string) bool {
	if tenantKey == "" {
		tenantKey = "anonymous"
	}
	rl.mu.Lock()
	v, ok := rl.visitors[tenantKey]
	if !ok {
		v = &rateVisitor{limiter: rate.NewLimiter(rate.Limit(rl.rps), rl.burst)}
		rl.visitors[tenantKey] = v
	}
	v.lastSeen = time.Now()
	rl.mu.Unlock()
	return v.limiter.Allow()
}

// Close stops the eviction goroutine. Idempotent.
func (rl *rateLimiter) Close() {
	if rl == nil {
		return
	}
	select {
	case <-rl.stopCh:
		// already closed
	default:
		close(rl.stopCh)
	}
}

// rateLimitMiddleware returns a Gin handler that enforces rl. nil rl
// produces a no-op handler so callers don't need to branch.
func rateLimitMiddleware(rl *rateLimiter) gin.HandlerFunc {
	if rl == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		id := IdentityFromContext(c.Request.Context())
		key := id.APIKeyID
		if key == "" {
			key = id.Username
		}
		if !rl.Allow(key) {
			RateLimited(c, "rate limit exceeded for this key (RPSPerTenant)")
			return
		}
		c.Next()
	}
}
