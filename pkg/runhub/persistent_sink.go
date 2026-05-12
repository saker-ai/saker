package runhub

import (
	"context"
	"log/slog"
	"time"

	"github.com/cinience/saker/pkg/runhub/store"
)

// dbSink implements eventSink against a *store.Store. As of Stage B the
// hot-path write is a non-blocking enqueue into the shared batchWriter;
// the actual InsertEventsBatch + Notify happens inside the writer
// goroutine. loadSince stays a direct synchronous read because the
// caller (Run.SnapshotSince) already holds its own context budget.
//
// Sink errors at write time are absorbed by the writer (logged inside
// flush); write itself never errors because the only failure mode
// (queue full) is handled by drop-oldest in batchWriter.write.
type dbSink struct {
	store   *store.Store
	logger  *slog.Logger
	metrics MetricsHooks
	writer  *batchWriter
}

// newDBSink constructs a sink and its associated batch writer. The
// caller (PersistentHub) owns shutdown — calling sink.shutdown() drains
// the writer. breakerThreshold <= 0 disables the circuit breaker.
func newDBSink(st *store.Store, logger *slog.Logger, metrics MetricsHooks, batchSize, batchBuffer int, batchInterval time.Duration, breakerThreshold int, breakerCooldown time.Duration) *dbSink {
	return &dbSink{
		store:   st,
		logger:  logger,
		metrics: metrics,
		writer:  newBatchWriter(st, logger, metrics, batchSize, batchBuffer, batchInterval, breakerThreshold, breakerCooldown),
	}
}

// write enqueues one event for asynchronous persistence. Always returns
// nil — the only failure mode is "queue full", and batchWriter.write
// handles that internally with drop-oldest backpressure (counted via
// MetricsHooks.OnBatchDrop). Run.Publish has no useful response to a
// sink failure anyway.
func (s *dbSink) write(_ context.Context, runID, tenantID string, e Event) error {
	s.writer.write(eventEnvelope{runID: runID, tenantID: tenantID, e: e})
	return nil
}

func (s *dbSink) loadSince(ctx context.Context, runID string, sinceSeq int) ([]Event, error) {
	rows, err := s.store.LoadEventsSince(ctx, runID, sinceSeq)
	if err != nil {
		s.logger.Warn("runhub: store LoadEventsSince failed", "run_id", runID, "since", sinceSeq, "err", err)
		return nil, err
	}
	out := make([]Event, len(rows))
	for i, r := range rows {
		out[i] = Event{Seq: r.Seq, Type: r.Type, Data: r.Data}
	}
	return out, nil
}

// shutdown drains the batch writer and waits for it to exit. Called by
// PersistentHub.Shutdown before store.Close.
func (s *dbSink) shutdown() {
	if s.writer != nil {
		s.writer.shutdown()
	}
}

// flush forces the async writer to drain its current queue
// synchronously. Caller blocks until every event accepted by write
// before this call has been persisted. PersistentHub.Flush wraps this
// for external use; tests use it as a synchronization fence.
func (s *dbSink) flush() {
	if s.writer != nil {
		s.writer.flushSync()
	}
}
