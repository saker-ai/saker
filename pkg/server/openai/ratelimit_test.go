package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestNewRateLimiter_DisabledWhenZero(t *testing.T) {
	if rl := newRateLimiter(context.Background(), 0); rl != nil {
		t.Error("rps=0 should return nil")
	}
	if rl := newRateLimiter(context.Background(), -1); rl != nil {
		t.Error("rps<0 should return nil")
	}
}

func TestRateLimiter_AllowsThenRejects(t *testing.T) {
	rl := newRateLimiter(context.Background(), 1)
	defer rl.Close()
	// Burst is max(int(rps), 5) → 5 for rps=1.
	for i := 0; i < 5; i++ {
		if !rl.Allow("tenant-A") {
			t.Fatalf("burst[%d] should be allowed", i)
		}
	}
	if rl.Allow("tenant-A") {
		t.Error("expected reject after burst exhausted")
	}
}

func TestRateLimiter_TenantsAreIndependent(t *testing.T) {
	rl := newRateLimiter(context.Background(), 1)
	defer rl.Close()
	for i := 0; i < 5; i++ {
		_ = rl.Allow("A")
	}
	if !rl.Allow("B") {
		t.Error("tenant B should not be affected by tenant A's quota")
	}
}

func TestRateLimiter_AnonymousFallback(t *testing.T) {
	rl := newRateLimiter(context.Background(), 1)
	defer rl.Close()
	if !rl.Allow("") {
		t.Error("empty key should be admitted under 'anonymous' bucket")
	}
}

func TestRateLimiter_Close_Idempotent(t *testing.T) {
	rl := newRateLimiter(context.Background(), 5)
	rl.Close()
	rl.Close() // must not panic
	var nilRL *rateLimiter
	nilRL.Close() // must not panic on nil receiver
}

func TestRateLimiter_EvictIdle(t *testing.T) {
	rl := newRateLimiter(context.Background(), 5)
	defer rl.Close()
	rl.Allow("ghost")
	if _, ok := rl.visitors["ghost"]; !ok {
		t.Fatal("expected visitor to be tracked")
	}
	rl.evictIdle(0) // anything older than "right now" -> evict all
	if _, ok := rl.visitors["ghost"]; ok {
		t.Error("expected visitor to be evicted")
	}
}

func TestRateLimitMiddleware_NilRateLimiter(t *testing.T) {
	router := gin.New()
	router.Use(rateLimitMiddleware(nil))
	router.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("nil rl should be no-op, got status %d", rec.Code)
	}
}

func TestRateLimitMiddleware_429AfterBurst(t *testing.T) {
	rl := newRateLimiter(context.Background(), 1)
	defer rl.Close()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		// Inject a known identity so the middleware keys deterministically.
		withIdentity(c, Identity{APIKeyID: "key-A"})
		c.Next()
	})
	router.Use(rateLimitMiddleware(rl))
	router.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("burst[%d] = %d, want 200", i, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("post-burst = %d, want 429", rec.Code)
	}
}

func TestRateLimitMiddleware_DifferentKeysIndependent(t *testing.T) {
	rl := newRateLimiter(context.Background(), 1)
	defer rl.Close()

	makeRouter := func(keyID string) *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			withIdentity(c, Identity{APIKeyID: keyID})
			c.Next()
		})
		r.Use(rateLimitMiddleware(rl))
		r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
		return r
	}

	rA := makeRouter("A")
	rB := makeRouter("B")
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		rA.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	}
	rec := httptest.NewRecorder()
	rB.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("tenant B should not be limited by A, got status %d", rec.Code)
	}
}

func TestRateLimitMiddleware_FallsBackToUsername(t *testing.T) {
	rl := newRateLimiter(context.Background(), 1)
	defer rl.Close()

	r := gin.New()
	r.Use(func(c *gin.Context) {
		withIdentity(c, Identity{Username: "anon-user"}) // no APIKeyID
		c.Next()
	})
	r.Use(rateLimitMiddleware(rl))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("burst[%d] = %d, want 200", i, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected username-keyed bucket to also rate-limit, got %d", rec.Code)
	}
	// Sanity: a different username escapes the limit.
	r2 := gin.New()
	r2.Use(func(c *gin.Context) {
		withIdentity(c, Identity{Username: "other"})
		c.Next()
	})
	r2.Use(rateLimitMiddleware(rl))
	r2.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	rec = httptest.NewRecorder()
	r2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("different username should not be limited, got %d", rec.Code)
	}
}

// Sanity check that the GC loop can run without panicking and respects ctx.
func TestRateLimiter_GCLoopRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rl := newRateLimiter(ctx, 5)
	cancel()
	// Give the loop a beat to notice the cancel; if it leaks we'd see
	// it in `go test -race` runs on CI.
	time.Sleep(20 * time.Millisecond)
	rl.Close()
}
