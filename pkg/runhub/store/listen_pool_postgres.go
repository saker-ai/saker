//go:build postgres

// Shared LISTEN connection pool. One pgx.Conn multiplexes every per-run
// LISTEN that PersistentHub starts, so a hub serving N concurrent runs
// holds 1 pgx connection instead of N. The pool also owns the
// auto-reconnect path — on connection drop the reader goroutine
// reconnects with exponential backoff, re-LISTENs every still-subscribed
// channel, and pushes a synthetic "evt" payload to every subscriber so
// the consumer's poll-fallback (LoadEventsSince) closes the gap on any
// notifications missed during the outage.
//
// Goroutine ownership: pgx.Conn is NOT goroutine-safe, so exactly one
// goroutine (readerLoop) ever touches the conn. Subscribe / Unsubscribe
// communicate with the reader by mutating the subscribers/refcount map
// under poolMu and waking the reader by canceling its current
// WaitForNotification context (waitCancel).
//
// Listener API stays unchanged (Notifications + Close) so the
// PersistentHub code path is identical to the legacy per-Listener
// implementation; the only visible difference is one connection.

package store

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// poolNotificationBuffer is the per-subscriber chan capacity. Mirrors
// the original per-listener buffer (see pgxNotificationBuffer) so a slow
// consumer behaves identically — overflow drops the payload and the
// consumer's LoadEventsSince fallback fills the gap.
const poolNotificationBuffer = 64

// reconnectInitialBackoff and reconnectMaxBackoff bound the exponential
// retry between dropped pgx connections. Jittered ±20% to avoid
// thundering-herd reconnects when an upstream pgbouncer cycles.
const (
	reconnectInitialBackoff = 200 * time.Millisecond
	reconnectMaxBackoff     = 30 * time.Second
)

// reconnectSyntheticPayload is the payload pushed to every subscriber
// of every channel after a reconnect. The string is opaque to consumers
// (they only check that the channel produced something); using a
// distinctive value makes pg-flowing payloads easier to tell apart from
// pool-injected ones in tcpdump / logs.
const reconnectSyntheticPayload = "__reconnect__"

// listenPool is the shared LISTEN multiplexer. Exactly one instance per
// Store; lazily created by ensurePool() on first Subscribe.
type listenPool struct {
	dsn         string
	logger      func(success bool) // = Store.onReconnect; nil-safe
	mu          sync.Mutex
	subscribers map[string][]chan string // channel -> subscriber chans
	listening   map[string]struct{}      // channels currently LISTEN'd on conn
	wakeCh      chan struct{}            // tickle reader to reconcile
	stop        chan struct{}            // close to terminate readerLoop
	done        chan struct{}            // closed when readerLoop exits
}

// newListenPool spawns the pool and its single reader goroutine.
// Caller is responsible for shutdown via shutdown().
func newListenPool(dsn string, onReconnect func(bool)) *listenPool {
	p := &listenPool{
		dsn:         dsn,
		logger:      onReconnect,
		subscribers: make(map[string][]chan string),
		listening:   make(map[string]struct{}),
		wakeCh:      make(chan struct{}, 1),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	go p.readerLoop()
	return p
}

// shutdown stops the reader goroutine, closes the conn, and closes
// every subscriber chan so consumers' range loops exit cleanly.
// Idempotent.
func (p *listenPool) shutdown() {
	select {
	case <-p.stop:
		return
	default:
		close(p.stop)
	}
	p.wake()
	<-p.done
	// Drain subscribers — close every chan. Reader has exited so no new
	// sends will race the closes.
	p.mu.Lock()
	for ch, subs := range p.subscribers {
		for _, c := range subs {
			close(c)
		}
		delete(p.subscribers, ch)
	}
	p.listening = nil
	p.mu.Unlock()
}

// subscribe registers a fresh consumer for channel and returns its
// payload chan plus an unsubscribe function. The first subscriber for a
// channel triggers a LISTEN on the shared conn (via the reader), which
// happens asynchronously — the consumer doesn't block on the LISTEN
// round trip.
//
// Returns an error only when the pool is already shutting down.
func (p *listenPool) subscribe(channel string) (<-chan string, func(), error) {
	if strings.TrimSpace(channel) == "" {
		return nil, nil, errors.New("runhub/store: subscribe requires non-empty channel")
	}
	select {
	case <-p.stop:
		return nil, nil, errors.New("runhub/store: listen pool shut down")
	default:
	}
	ch := make(chan string, poolNotificationBuffer)
	p.mu.Lock()
	p.subscribers[channel] = append(p.subscribers[channel], ch)
	p.mu.Unlock()
	p.wake() // ask reader to reconcile (may need to LISTEN)

	unsub := func() {
		p.mu.Lock()
		subs := p.subscribers[channel]
		for i, existing := range subs {
			if existing == ch {
				subs = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		empty := len(subs) == 0
		if empty {
			delete(p.subscribers, channel)
		} else {
			p.subscribers[channel] = subs
		}
		p.mu.Unlock()
		// Close the consumer's chan so its range loop exits. Safe — the
		// reader fans out under the lock and we just removed ch from the
		// list, so no further send can target this chan.
		close(ch)
		if empty {
			p.wake() // reader will UNLISTEN
		}
	}
	return ch, unsub, nil
}

// wake nudges the reader goroutine to re-check its desired LISTEN set.
// Non-blocking (wakeCh has capacity 1) so a burst of subscribe/unsub
// calls coalesces into a single reconcile pass.
func (p *listenPool) wake() {
	select {
	case p.wakeCh <- struct{}{}:
	default:
	}
}

// readerLoop is the single goroutine that owns the pgx.Conn. It opens
// the connection, syncs the LISTEN set with subscribers, drains
// notifications, and reconnects with backoff on conn failure.
func (p *listenPool) readerLoop() {
	defer close(p.done)
	backoff := reconnectInitialBackoff
	for {
		select {
		case <-p.stop:
			return
		default:
		}
		conn, err := pgx.Connect(context.Background(), p.dsn)
		if err != nil {
			p.reportReconnect(false)
			if !p.sleepBackoff(backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		// Successful (re)connect.
		p.reportReconnect(true)
		backoff = reconnectInitialBackoff
		// Replay synthetic payload to every existing subscriber so they
		// requery LoadEventsSince and don't miss anything that fired
		// during the outage. Done BEFORE reLISTEN so a fresh subscriber
		// added at the same instant doesn't get a duplicate.
		p.fanoutSynthetic()
		// Run the connection until it errors or stop fires.
		drained := p.runConn(conn)
		_ = conn.Close(context.Background())
		// Wipe listening set so the next connection's reconcile re-LISTENs
		// every active channel from scratch.
		p.mu.Lock()
		p.listening = make(map[string]struct{})
		p.mu.Unlock()
		if !drained {
			return
		}
	}
}

// runConn drives one pgx.Conn from connect to drop. Reconciles LISTEN
// set on every wake, dispatches notifications, returns false when stop
// fires (so the outer loop exits cleanly).
func (p *listenPool) runConn(conn *pgx.Conn) bool {
	for {
		if !p.reconcile(conn) {
			return true // conn error during LISTEN/UNLISTEN
		}
		// Per-iteration cancellable wait. Subscribe/unsub call wake() to
		// cancel waitCtx so this goroutine returns from
		// WaitForNotification and we can re-reconcile.
		waitCtx, waitCancel := context.WithCancel(context.Background())
		// Goroutine that watches stop + wakeCh and cancels waitCtx.
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			select {
			case <-p.stop:
			case <-p.wakeCh:
			case <-waitCtx.Done():
			}
			waitCancel()
		}()
		notif, err := conn.WaitForNotification(waitCtx)
		waitCancel()
		<-watchDone
		select {
		case <-p.stop:
			return false
		default:
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				// Wake or shutdown — loop and reconcile.
				continue
			}
			// Real error — give up on this conn, reconnect.
			return true
		}
		p.dispatch(notif.Channel, notif.Payload)
	}
}

// reconcile syncs the on-conn LISTEN set with the desired subscribers
// map. Issues LISTEN for new channels and UNLISTEN for channels that no
// longer have subscribers. Returns false on conn error.
func (p *listenPool) reconcile(conn *pgx.Conn) bool {
	p.mu.Lock()
	desired := make(map[string]struct{}, len(p.subscribers))
	for ch := range p.subscribers {
		desired[ch] = struct{}{}
	}
	current := p.listening
	add := make([]string, 0)
	remove := make([]string, 0)
	for ch := range desired {
		if _, ok := current[ch]; !ok {
			add = append(add, ch)
		}
	}
	for ch := range current {
		if _, ok := desired[ch]; !ok {
			remove = append(remove, ch)
		}
	}
	p.mu.Unlock()

	for _, ch := range add {
		if _, err := conn.Exec(context.Background(), "LISTEN "+pgx.Identifier{ch}.Sanitize()); err != nil {
			return false
		}
		p.mu.Lock()
		p.listening[ch] = struct{}{}
		p.mu.Unlock()
	}
	for _, ch := range remove {
		if _, err := conn.Exec(context.Background(), "UNLISTEN "+pgx.Identifier{ch}.Sanitize()); err != nil {
			return false
		}
		p.mu.Lock()
		delete(p.listening, ch)
		p.mu.Unlock()
	}
	return true
}

// dispatch fans out one notification to every subscriber of channel.
// Slow subscribers drop the payload (matches per-Listener semantics).
func (p *listenPool) dispatch(channel, payload string) {
	p.mu.Lock()
	subs := p.subscribers[channel]
	// Snapshot under the lock so unsub mid-fanout doesn't trip on a
	// freshly closed chan. After the snapshot, sends are non-blocking so
	// even a closed-chan send (race with concurrent unsub) is bounded —
	// but unsub close()s only after removing from the slice, which means
	// snapshot ⇒ send is race-free as long as we copy here.
	snap := make([]chan string, len(subs))
	copy(snap, subs)
	p.mu.Unlock()
	for _, ch := range snap {
		select {
		case ch <- payload:
		default:
			// Buffer full — drop. Consumer's LoadEventsSince covers the gap.
		}
	}
}

// fanoutSynthetic pushes one synthetic payload per channel to every
// subscriber. Lets consumers bridge a reconnect by reissuing
// LoadEventsSince and picking up anything that fired on PG while the
// conn was down.
func (p *listenPool) fanoutSynthetic() {
	p.mu.Lock()
	channels := make([]string, 0, len(p.subscribers))
	for ch := range p.subscribers {
		channels = append(channels, ch)
	}
	p.mu.Unlock()
	for _, ch := range channels {
		p.dispatch(ch, reconnectSyntheticPayload)
	}
}

// reportReconnect invokes the operator-supplied callback. nil-safe.
func (p *listenPool) reportReconnect(success bool) {
	if p.logger != nil {
		p.logger(success)
	}
}

// sleepBackoff sleeps dur with jitter, returning false if stop fires
// (so the reader exits instead of finishing the nap).
func (p *listenPool) sleepBackoff(dur time.Duration) bool {
	d := jitter(dur)
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.stop:
		return false
	case <-t.C:
		return true
	}
}

// nextBackoff doubles the current backoff up to the cap. Jitter is
// applied at sleep time, not here, so the cap stays a hard cap.
func nextBackoff(cur time.Duration) time.Duration {
	next := cur * 2
	if next > reconnectMaxBackoff {
		return reconnectMaxBackoff
	}
	return next
}

// jitter returns d ± 20% to spread reconnect storms.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	delta := float64(d) * 0.2
	offset := (rand.Float64()*2 - 1) * delta
	return d + time.Duration(offset)
}

// ensurePool returns the Store's listenPool, lazily constructing it on
// first call. Returns an error only when the driver isn't postgres
// (callers should have driver-checked already).
func (s *Store) ensurePool() (*listenPool, error) {
	if s.driver != "postgres" {
		return nil, fmt.Errorf("runhub/store: listen pool requires postgres driver, got %s", s.driver)
	}
	s.listenMu.Lock()
	defer s.listenMu.Unlock()
	if s.listenState != nil {
		if existing, ok := s.listenState.(*listenPool); ok {
			return existing, nil
		}
	}
	p := newListenPool(s.dsn, s.onReconnect)
	s.listenState = p
	return p, nil
}

// poolShutdown shuts the pool down if one was constructed. Called by
// Store.Close.
func (s *Store) poolShutdown() {
	s.listenMu.Lock()
	state := s.listenState
	s.listenState = nil
	s.listenMu.Unlock()
	if p, ok := state.(*listenPool); ok && p != nil {
		p.shutdown()
	}
}
