// Package openai implements an OpenAI-compatible HTTP/SSE inbound gateway.
// It lives under pkg/server/openai (not pkg/server itself) so the CLI/TUI
// binaries — which never start an HTTP server — link zero gateway code.
//
// The whole package is gated behind two switches:
//   - the saker --server CLI flag (process is in server mode), AND
//   - --openai-gw-enabled=true (operator opted in for this server)
//
// When either is unset, Register is never called and no /v1/* routes appear.
package openai

import (
	"errors"
	"strings"
	"time"

	"github.com/saker-ai/saker/pkg/project/dialect"
)

// Options carries operator-side gateway configuration. Everything here is
// read once at server start; per-request knobs live in ExtraBody instead.
type Options struct {
	// Enabled is the master kill switch. False means Register is a no-op.
	Enabled bool

	// MaxRuns caps the total in-flight runs the in-memory hub will track.
	// New runs past this cap are rejected with 429. Zero falls back to the
	// default (256).
	MaxRuns int

	// MaxRunsPerTenant caps in-flight runs for a single Bearer key. Zero
	// disables the per-tenant cap. Default 32.
	MaxRunsPerTenant int

	// RPSPerTenant is the per-Bearer-key request rate cap (requests/second
	// + small burst). Zero disables. Default 10.
	RPSPerTenant int

	// RingSize is the per-run event ring buffer length used for reconnect
	// replay. Zero falls back to 512.
	RingSize int

	// ExpiresAfterSeconds is the default Run idle/await timeout. Used when
	// the client does not pass extra_body.expires_after_seconds. Zero falls
	// back to 600 (10 minutes), matching the design doc default.
	ExpiresAfterSeconds int

	// AssistantsCompat enables the legacy Assistants v1 routes
	// (/v1/threads/.../runs/.../submit_tool_outputs). Off by default; turn
	// on only for clients that cannot use the chat-completions tool path.
	AssistantsCompat bool

	// DevBypassAuth, when true, accepts requests without a valid Bearer key
	// by attributing them to the localhost identity. Intended for local
	// development; never enable in production. Mirrors OPENAI_GW_DEV_BYPASS=true.
	DevBypassAuth bool

	// MaxRequestBodyBytes caps how much of the POST body (chat.completions
	// and friends) we'll read before bailing with HTTP 413. Generous default
	// (10 MiB) so multi-image messages fit; clamp tighter in production if
	// you want to harden against accidental upload abuse. Zero falls back to
	// the default.
	MaxRequestBodyBytes int64

	// ErrorDetailMode chooses how much of an internal failure leaks into the
	// SSE error chunk:
	//   - "dev"  → "[saker error] <raw msg>"  (default; useful when iterating)
	//   - "prod" → "[saker error] internal error (run_id=<id>)"
	// In prod mode the run_id lets operators correlate to logs without
	// surfacing the raw error text (which can include model/provider URLs,
	// stack-trace fragments, or transient infra detail).
	ErrorDetailMode string

	// RunHubDSN selects the runhub backend:
	//   - "" (empty)         → in-memory hub; runs lost on process exit.
	//                          The default — preserves zero-config behavior.
	//   - "sqlite://path" or
	//     a bare path        → embedded sqlite-backed PersistentHub. Single
	//                          process; reconnect after restart works,
	//                          cross-process fan-out does not.
	//   - "postgres://..."   → Postgres-backed PersistentHub with
	//                          LISTEN/NOTIFY for cross-process fan-out.
	//                          Requires the binary to be built with
	//                          `-tags postgres`; see .docs §10.
	//
	// DSN parsing reuses pkg/project/dialect; the operator's own PRAGMA /
	// connection options are honored.
	RunHubDSN string

	// RunHubBatchSize bounds how many events the persistent hub's async
	// writer accumulates before issuing one InsertEventsBatch. Zero falls
	// back to 64. Tune up for throughput, down for tail latency.
	RunHubBatchSize int

	// RunHubBatchBufferSize sets the enqueue chan capacity. When the
	// producer outruns the writer, the oldest queued envelope is dropped
	// (counted in saker_runhub_batch_drops_total) so Run.Publish never
	// blocks. Zero falls back to 1024.
	RunHubBatchBufferSize int

	// RunHubBatchInterval bounds the writer's idle window — even a
	// partially filled buffer is flushed every interval so a low-rate
	// stream doesn't stall. Zero falls back to 50ms.
	RunHubBatchInterval time.Duration

	// RunHubGCInterval is how often the hub sweeper runs. Smaller →
	// faster reclaim of expired/terminal rows but more wakeups; larger
	// → fewer wakeups, longer steady-state row count. Zero falls back
	// to 30s.
	RunHubGCInterval time.Duration

	// RunHubTerminalRetention is how long a finished run row sticks
	// around after it reaches a terminal state, so a reconnecting
	// client can still observe the final status event. PersistentHub
	// uses the same window for store-row deletion. Zero falls back to
	// 60s.
	RunHubTerminalRetention time.Duration

	// RunHubMaxEventBytes caps the byte size of any single SSE event
	// payload that flows through Run.Publish (and DeliverExternal on the
	// PG cross-process path). Oversized events are rejected and counted
	// in saker_runhub_oversized_events_total. Zero = unbounded (legacy
	// behavior, NOT recommended in production). Default 1 MiB — tighter
	// than MaxRequestBodyBytes because the per-event budget is much
	// smaller than the per-request budget; the latter must accommodate
	// multi-image requests, the former is a single delta chunk.
	RunHubMaxEventBytes int64

	// RunHubSubscriberIdleTimeout is the wall-clock window a per-run
	// subscriber channel may sit without receiving a successful fan-out
	// before the GC sweeper closes it (and increments
	// saker_runhub_subscribers_idle_evicted_total). Targets leaked SSE
	// streams: a client that disconnected without firing its unsub
	// closure leaves a subscriber pinned forever, exhausting per-run
	// fan-out budget. Zero = disabled (legacy behavior). Default 0 so
	// nothing changes for operators that haven't measured their event
	// rate floor; recommend 5–15 minutes once measured.
	RunHubSubscriberIdleTimeout time.Duration

	// RunHubSinkBreakerThreshold is the number of consecutive batch
	// flush failures that trips the persistent hub's circuit breaker
	// open. While Open, batch flushes are skipped (counted via
	// saker_runhub_sink_breaker_skipped_total) so a stuck store doesn't
	// burn CPU + log volume on every interval. Zero disables the
	// breaker (every flush calls the store regardless of failure run);
	// default 10 — large enough to ride out single-batch hiccups,
	// small enough to react within seconds at default BatchInterval.
	RunHubSinkBreakerThreshold int

	// RunHubSinkBreakerCooldown is how long the breaker stays Open
	// before transitioning to HalfOpen and allowing one probe call.
	// Zero with a non-zero threshold latches the breaker Open until
	// restart (operator opt-in for fail-fast envs); default 30s.
	RunHubSinkBreakerCooldown time.Duration

	// RunHubPGCopyThreshold gates the postgres-only COPY-based bulk
	// insert path. When the runtime driver is postgres AND a single
	// InsertEventsBatch carries at least this many rows, the call goes
	// through pgx.CopyFrom into a TEMP staging table (preserves the
	// existing ON CONFLICT DO NOTHING dedup semantics that vanilla
	// CopyFrom can't express). Default 50 — below that, the temp-table
	// + 2-statement tx overhead dominates the multi-row INSERT path's
	// already-cheap parse-once-execute-many. Zero disables the COPY
	// path entirely; ignored on non-postgres drivers.
	RunHubPGCopyThreshold int
}

// Defaults returns Options with all the documented baseline values filled
// in. Used by tests and by the cmd_server flag-binding code.
func Defaults() Options {
	return Options{
		Enabled:               false,
		MaxRuns:               256,
		MaxRunsPerTenant:      32,
		RPSPerTenant:          10,
		RingSize:              512,
		ExpiresAfterSeconds:   600,
		AssistantsCompat:      false,
		DevBypassAuth:         false,
		MaxRequestBodyBytes:   10 * 1024 * 1024,
		ErrorDetailMode:       ErrorDetailDev,
		RunHubBatchSize:         64,
		RunHubBatchBufferSize:   1024,
		RunHubBatchInterval:     50 * time.Millisecond,
		RunHubGCInterval:           30 * time.Second,
		RunHubTerminalRetention:    60 * time.Second,
		RunHubMaxEventBytes:        1 * 1024 * 1024,
		RunHubSinkBreakerThreshold: 10,
		RunHubSinkBreakerCooldown:  30 * time.Second,
		RunHubPGCopyThreshold:      50,
	}
}

// ErrorDetailMode constants for Options.ErrorDetailMode.
const (
	ErrorDetailDev  = "dev"
	ErrorDetailProd = "prod"
)

// Validate normalizes any zero values to defaults and returns an error for
// settings outside the allowed range. Called once by Register before any
// route is mounted.
func (o *Options) Validate() error {
	d := Defaults()
	if o.MaxRuns <= 0 {
		o.MaxRuns = d.MaxRuns
	}
	if o.MaxRunsPerTenant < 0 {
		return errors.New("openai-gw: MaxRunsPerTenant must be >= 0")
	}
	if o.MaxRunsPerTenant == 0 {
		o.MaxRunsPerTenant = d.MaxRunsPerTenant
	}
	if o.RPSPerTenant < 0 {
		return errors.New("openai-gw: RPSPerTenant must be >= 0")
	}
	if o.RPSPerTenant == 0 {
		o.RPSPerTenant = d.RPSPerTenant
	}
	if o.RingSize <= 0 {
		o.RingSize = d.RingSize
	}
	if o.ExpiresAfterSeconds <= 0 {
		o.ExpiresAfterSeconds = d.ExpiresAfterSeconds
	}
	if o.ExpiresAfterSeconds < 60 || o.ExpiresAfterSeconds > 86400 {
		return errors.New("openai-gw: ExpiresAfterSeconds must be between 60 and 86400")
	}
	if o.MaxRequestBodyBytes < 0 {
		return errors.New("openai-gw: MaxRequestBodyBytes must be >= 0")
	}
	if o.MaxRequestBodyBytes == 0 {
		o.MaxRequestBodyBytes = d.MaxRequestBodyBytes
	}
	switch o.ErrorDetailMode {
	case "":
		o.ErrorDetailMode = d.ErrorDetailMode
	case ErrorDetailDev, ErrorDetailProd:
		// ok
	default:
		return errors.New("openai-gw: ErrorDetailMode must be 'dev' or 'prod'")
	}
	if dsn := strings.TrimSpace(o.RunHubDSN); dsn != "" {
		// Just validate the scheme is a known dialect; we don't open the
		// store here because the runhub PersistentHub takes ownership of
		// Open/Close, and we don't want Validate to leak file handles.
		if _, _, err := dialect.ParseDSN(dsn); err != nil {
			return errors.New("openai-gw: invalid RunHubDSN: " + err.Error())
		}
		o.RunHubDSN = dsn
	}
	if o.RunHubBatchSize < 0 {
		return errors.New("openai-gw: RunHubBatchSize must be >= 0")
	}
	if o.RunHubBatchSize == 0 {
		o.RunHubBatchSize = d.RunHubBatchSize
	}
	if o.RunHubBatchSize > 10000 {
		return errors.New("openai-gw: RunHubBatchSize must be <= 10000")
	}
	if o.RunHubBatchBufferSize < 0 {
		return errors.New("openai-gw: RunHubBatchBufferSize must be >= 0")
	}
	if o.RunHubBatchBufferSize == 0 {
		o.RunHubBatchBufferSize = d.RunHubBatchBufferSize
	}
	if o.RunHubBatchBufferSize < o.RunHubBatchSize {
		return errors.New("openai-gw: RunHubBatchBufferSize must be >= RunHubBatchSize")
	}
	if o.RunHubBatchInterval < 0 {
		return errors.New("openai-gw: RunHubBatchInterval must be >= 0")
	}
	if o.RunHubBatchInterval == 0 {
		o.RunHubBatchInterval = d.RunHubBatchInterval
	}
	if o.RunHubBatchInterval > time.Minute {
		return errors.New("openai-gw: RunHubBatchInterval must be <= 1m")
	}
	if o.RunHubGCInterval < 0 {
		return errors.New("openai-gw: RunHubGCInterval must be >= 0")
	}
	if o.RunHubGCInterval == 0 {
		o.RunHubGCInterval = d.RunHubGCInterval
	}
	if o.RunHubGCInterval < time.Second || o.RunHubGCInterval > time.Hour {
		return errors.New("openai-gw: RunHubGCInterval must be between 1s and 1h")
	}
	if o.RunHubTerminalRetention < 0 {
		return errors.New("openai-gw: RunHubTerminalRetention must be >= 0")
	}
	if o.RunHubTerminalRetention == 0 {
		o.RunHubTerminalRetention = d.RunHubTerminalRetention
	}
	if o.RunHubTerminalRetention < time.Second || o.RunHubTerminalRetention > 24*time.Hour {
		return errors.New("openai-gw: RunHubTerminalRetention must be between 1s and 24h")
	}
	if o.RunHubMaxEventBytes < 0 {
		return errors.New("openai-gw: RunHubMaxEventBytes must be >= 0")
	}
	if o.RunHubMaxEventBytes == 0 {
		// Explicit zero — operator opted out of the cap. Leave as 0 so
		// runhub.Config.MaxEventBytes treats it as unbounded; do NOT
		// fall back to the default (the operator just disabled it).
	}
	if o.RunHubMaxEventBytes > 0 && o.RunHubMaxEventBytes > o.MaxRequestBodyBytes {
		return errors.New("openai-gw: RunHubMaxEventBytes must be <= MaxRequestBodyBytes (a single event can't exceed the inbound request body cap)")
	}
	if o.RunHubSubscriberIdleTimeout < 0 {
		return errors.New("openai-gw: RunHubSubscriberIdleTimeout must be >= 0")
	}
	if o.RunHubSubscriberIdleTimeout > 0 && o.RunHubSubscriberIdleTimeout < time.Second {
		// A timeout below the GC interval floor (1s) would mean every sweep
		// could evict a subscriber that just received an event a few hundred
		// ms ago — likely a misconfiguration.
		return errors.New("openai-gw: RunHubSubscriberIdleTimeout must be 0 (disabled) or >= 1s")
	}
	if o.RunHubSubscriberIdleTimeout > 24*time.Hour {
		return errors.New("openai-gw: RunHubSubscriberIdleTimeout must be <= 24h")
	}
	if o.RunHubSinkBreakerThreshold < 0 {
		return errors.New("openai-gw: RunHubSinkBreakerThreshold must be >= 0")
	}
	if o.RunHubSinkBreakerThreshold == 0 {
		// Explicit zero — operator opted out. Leave as 0 so the breaker
		// is disabled; do NOT fall back to the default (the operator
		// just disabled it).
	}
	if o.RunHubSinkBreakerThreshold > 1000 {
		// Higher than this and the breaker takes minutes to react,
		// defeating the point. Almost certainly a misconfiguration.
		return errors.New("openai-gw: RunHubSinkBreakerThreshold must be <= 1000")
	}
	if o.RunHubSinkBreakerCooldown < 0 {
		return errors.New("openai-gw: RunHubSinkBreakerCooldown must be >= 0")
	}
	if o.RunHubSinkBreakerCooldown == 0 && o.RunHubSinkBreakerThreshold > 0 {
		// Don't auto-fill — a zero cooldown with a non-zero threshold
		// latches the breaker Open until restart. That's a valid
		// fail-fast posture, but operators must opt in explicitly
		// rather than inherit the default. So leave as zero here and
		// let the runhub layer interpret zero-cooldown as "no
		// recovery". (The Defaults() helper sets a sane 30s for
		// fresh configs.)
	}
	if o.RunHubSinkBreakerCooldown > 24*time.Hour {
		return errors.New("openai-gw: RunHubSinkBreakerCooldown must be <= 24h")
	}
	if o.RunHubPGCopyThreshold < 0 {
		return errors.New("openai-gw: RunHubPGCopyThreshold must be >= 0")
	}
	if o.RunHubPGCopyThreshold == 0 {
		// Explicit zero — operator opted out of the COPY fast path.
		// Leave as 0; do NOT fall back to the default. Every batch
		// goes through the prepared multi-row INSERT.
	}
	if o.RunHubPGCopyThreshold > 100000 {
		// Above this the threshold effectively disables COPY because
		// the batch size cap (RunHubBatchSize) is way smaller. Refuse
		// rather than silently disable so misconfigurations surface.
		return errors.New("openai-gw: RunHubPGCopyThreshold must be <= 100000")
	}
	return nil
}

// ExpiresAfter returns the configured idle/await timeout as a duration.
func (o Options) ExpiresAfter() time.Duration {
	return time.Duration(o.ExpiresAfterSeconds) * time.Second
}
