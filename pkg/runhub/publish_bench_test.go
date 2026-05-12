package runhub

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cinience/saker/pkg/runhub/store"
)

// benchPayload is the same 64-byte payload across every benchmark so
// b.SetBytes reports a clean events/sec view that's directly comparable
// across the three backends. 64 bytes is in the same ballpark as a
// typical OpenAI chat-completion delta (one delta.content string,
// JSON-encoded).
var benchPayload = []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

// BenchmarkPublish_Memory measures the MemoryHub fast path: ring write
// + subscriber fan-out, no sink. Upper bound for everything else — every
// persistence backend pays additional cost on top of this baseline.
func BenchmarkPublish_Memory(b *testing.B) {
	hub := NewMemoryHub(Config{RingSize: 4096})
	b.Cleanup(hub.Shutdown)
	run, err := hub.Create(CreateOptions{TenantID: "bench", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		b.Fatalf("Create: %v", err)
	}
	b.SetBytes(int64(len(benchPayload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		run.Publish("chunk", benchPayload)
	}
}

// BenchmarkPublish_Memory_Parallel exercises the same MemoryHub fast path
// from multiple goroutines so we can quantify Run.mu contention. Compare
// the per-op cost of this benchmark to the single-threaded variant: a
// well-scaled lock should give roughly N× throughput at -cpu=N (or, in
// per-op terms, the parallel ns/op should be much closer to the
// single-thread number than to N× it). If the parallel ns/op is more
// than ~1.5× the single-thread number at -cpu=8, Run.mu is the bottleneck
// and Stage G.3 (lock split) becomes worthwhile.
func BenchmarkPublish_Memory_Parallel(b *testing.B) {
	hub := NewMemoryHub(Config{RingSize: 4096})
	b.Cleanup(hub.Shutdown)
	run, err := hub.Create(CreateOptions{TenantID: "bench", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		b.Fatalf("Create: %v", err)
	}
	b.SetBytes(int64(len(benchPayload)))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			run.Publish("chunk", benchPayload)
		}
	})
}

// BenchmarkPublish_SQLiteBatch measures the default async batch path:
// each Publish enqueues a non-blocking envelope and the writer goroutine
// flushes in batches of 64 every 50ms. This is what production sees;
// compare to BenchmarkPublish_SQLiteSyncish for the win from batching.
func BenchmarkPublish_SQLiteBatch(b *testing.B) {
	hub := openBenchHub(b, PersistentConfig{Config: Config{RingSize: 4096}})
	run, err := hub.Create(CreateOptions{TenantID: "bench", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		b.Fatalf("Create: %v", err)
	}
	b.SetBytes(int64(len(benchPayload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		run.Publish("chunk", benchPayload)
	}
	b.StopTimer()
	hub.Flush()
}

// BenchmarkPublish_SQLiteBatch_Parallel exercises the persistent path
// (ring + sink enqueue) from multiple goroutines. Same lock-contention
// signal as the Memory_Parallel variant, but on the production hub
// where the sink chan adds another contended mutex. If this scales much
// worse than Memory_Parallel, the bottleneck is in the sink path
// (batchWriter.enqueue), not Run.mu.
func BenchmarkPublish_SQLiteBatch_Parallel(b *testing.B) {
	hub := openBenchHub(b, PersistentConfig{Config: Config{RingSize: 4096}})
	run, err := hub.Create(CreateOptions{TenantID: "bench", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		b.Fatalf("Create: %v", err)
	}
	b.SetBytes(int64(len(benchPayload)))
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			run.Publish("chunk", benchPayload)
		}
	})
	b.StopTimer()
	hub.Flush()
}

// BenchmarkPublish_SQLiteSyncish forces the writer to flush after every
// envelope (BatchSize=1, BatchInterval=1µs), approximating the pre-Stage-B
// synchronous insert pattern. The delta vs SQLiteBatch quantifies the
// throughput win from amortizing one fsync over a full batch — the
// number we wanted Stage B to move.
func BenchmarkPublish_SQLiteSyncish(b *testing.B) {
	hub := openBenchHub(b, PersistentConfig{
		Config:        Config{RingSize: 4096},
		BatchSize:     1,
		BatchInterval: time.Microsecond,
	})
	run, err := hub.Create(CreateOptions{TenantID: "bench", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		b.Fatalf("Create: %v", err)
	}
	b.SetBytes(int64(len(benchPayload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		run.Publish("chunk", benchPayload)
	}
	b.StopTimer()
	hub.Flush()
}

// openBenchHub builds a PersistentHub against a fresh sqlite file under
// b.TempDir so each benchmark runs against an isolated DB with no
// leftover rows. b.Cleanup wires the shutdown so the writer goroutine
// drains and exits before the next iteration starts.
func openBenchHub(b *testing.B, cfg PersistentConfig) *PersistentHub {
	b.Helper()
	path := filepath.Join(b.TempDir(), "bench.db")
	s, err := store.Open(store.Config{DSN: path})
	if err != nil {
		b.Fatalf("store.Open: %v", err)
	}
	if cfg.Store == nil {
		cfg.Store = s
	}
	h, err := NewPersistentHub(cfg)
	if err != nil {
		_ = s.Close()
		b.Fatalf("NewPersistentHub: %v", err)
	}
	b.Cleanup(h.Shutdown)
	return h
}
