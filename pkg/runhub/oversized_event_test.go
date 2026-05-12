package runhub

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingOversizedHooks captures OnOversizedEvent calls and the originating
// tenant labels so the test can assert both the count and the tenant
// attribution under concurrent load.
type countingOversizedHooks struct {
	noopHooks
	count   atomic.Uint64
	mu      sync.Mutex
	tenants []string
}

func (c *countingOversizedHooks) OnOversizedEvent(tenant string) {
	c.count.Add(1)
	c.mu.Lock()
	c.tenants = append(c.tenants, tenant)
	c.mu.Unlock()
}

// TestPublish_MaxEventBytes covers Run.Publish's payload size cap (E.1):
//
//   - cap=0           → unbounded; any size accepted (legacy behavior)
//   - cap=N, size=N   → boundary inclusive; accepted
//   - cap=N, size=N+1 → first byte over → rejected (seq=0 + metric)
//   - cap=N, size=0   → empty payloads always pass
//
// The test runs against MemoryHub (the PersistentHub path is exercised in
// TestDeliverExternal_MaxEventBytes below).
func TestPublish_MaxEventBytes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		maxBytes     int64
		payload      []byte
		wantSeqZero  bool
		wantOversize uint64
	}{
		{"unbounded_passes_huge", 0, make([]byte, 16*1024), false, 0},
		{"empty_payload_always_passes", 1024, nil, false, 0},
		{"size_equal_cap_passes", 1024, make([]byte, 1024), false, 0},
		{"size_over_cap_by_one_rejected", 1024, make([]byte, 1025), true, 1},
		{"way_over_cap_rejected", 100, make([]byte, 10_000), true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hooks := &countingOversizedHooks{}
			hub := NewMemoryHub(Config{
				MaxEventBytes: tc.maxBytes,
				Metrics:       hooks,
			})
			t.Cleanup(hub.Shutdown)
			run, err := hub.Create(CreateOptions{
				TenantID:  "tenant-a",
				ExpiresAt: time.Now().Add(time.Hour),
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			seq := run.Publish("chunk", tc.payload)
			gotZero := seq == 0
			if gotZero != tc.wantSeqZero {
				t.Errorf("seq=%d wantZero=%v gotZero=%v", seq, tc.wantSeqZero, gotZero)
			}
			if got := hooks.count.Load(); got != tc.wantOversize {
				t.Errorf("oversized count = %d, want %d", got, tc.wantOversize)
			}
			if tc.wantOversize > 0 {
				hooks.mu.Lock()
				if len(hooks.tenants) == 0 || hooks.tenants[0] != "tenant-a" {
					t.Errorf("oversize tenant labels = %v, want first=tenant-a", hooks.tenants)
				}
				hooks.mu.Unlock()
			}
		})
	}
}

// TestDeliverExternal_MaxEventBytes covers the cross-process path: events
// arriving from a peer (PG NOTIFY) must also honor the size cap, and the
// rejection must NOT advance nextSeq (so a follow-up Publish on the same
// run can still allocate the right seq).
func TestDeliverExternal_MaxEventBytes(t *testing.T) {
	t.Parallel()
	hooks := &countingOversizedHooks{}
	hub := NewMemoryHub(Config{
		MaxEventBytes: 100,
		Metrics:       hooks,
	})
	t.Cleanup(hub.Shutdown)
	run, err := hub.Create(CreateOptions{
		TenantID:  "tenant-x",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Mix of accepted (size=50) and rejected (size=200) external events.
	events := []Event{
		{Seq: 1, Type: "chunk", Data: make([]byte, 50)},
		{Seq: 2, Type: "chunk", Data: make([]byte, 200)}, // rejected
		{Seq: 3, Type: "chunk", Data: make([]byte, 30)},
	}
	run.DeliverExternal(events)

	if got := hooks.count.Load(); got != 1 {
		t.Errorf("oversized external count = %d, want 1", got)
	}
	// Snapshot the ring — only the two accepted events should be there.
	snap := run.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("ring size = %d, want 2 (oversized event must be skipped)", len(snap))
	}
	if snap[0].Seq != 1 || snap[1].Seq != 3 {
		t.Errorf("ring seqs = [%d,%d], want [1,3]", snap[0].Seq, snap[1].Seq)
	}
}

// TestPublish_OversizedDoesntStarveSubscribers is a paranoia check: an
// oversized publish must not advance the seq counter, hold the lock, or
// otherwise affect a concurrently-streaming subscriber.
func TestPublish_OversizedDoesntStarveSubscribers(t *testing.T) {
	t.Parallel()
	hub := NewMemoryHub(Config{
		MaxEventBytes: 256,
		Metrics:       NopMetricsHooks(),
	})
	t.Cleanup(hub.Shutdown)
	run, err := hub.Create(CreateOptions{ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ch, _, unsub := run.Subscribe()
	defer unsub()

	// First a rejected oversize publish. Should not advance seq.
	if seq := run.Publish("chunk", make([]byte, 1024)); seq != 0 {
		t.Fatalf("oversized publish returned seq=%d, want 0", seq)
	}
	// Then a normal publish. Should be seq=1 (oversized rejection didn't
	// burn the seq counter).
	seq := run.Publish("chunk", []byte("ok"))
	if seq != 1 {
		t.Fatalf("normal publish after oversized: seq=%d, want 1 (oversize must not advance nextSeq)", seq)
	}
	select {
	case evt := <-ch:
		if evt.Seq != 1 {
			t.Errorf("subscriber got seq=%d, want 1", evt.Seq)
		}
	case <-time.After(time.Second):
		t.Fatalf("subscriber starved after oversized event preceded a normal one")
	}
}
