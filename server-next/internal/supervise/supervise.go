// Package supervise contains the process's panic containment.
//
// net/http recovers a panic raised inside a handler, so HTTP handlers were never
// the process-kill vector. Every other goroutine was: one panic in a search
// fan-out, an HLS producer, a scanner batch or any of the twenty long-lived
// supervisor loops took the whole server down, and "the server restarted and I
// don't know why" is the worst failure a home appliance can have.
//
// Nothing here changes behaviour on the success path. A goroutine that does not
// panic runs exactly as it did. A goroutine that does is logged with its stack,
// counted so the owner's diagnostics can show it, and — for a stateless
// long-lived loop — restarted with backoff.
//
// The division is deliberate. Go recovers and ends that goroutine: right for
// per-request fan-out and fire-and-forget work, where the request fails and
// nothing else is affected. Supervise restarts: right for a loop whose state
// lives in the database rather than in the goroutine, where not running at all
// is the worse outcome. A loop holding in-memory state it would resume with half
// built is not a candidate for either, and is left to fail loudly.
package supervise

import (
	"context"
	"log"
	"runtime/debug"
	"sync/atomic"
	"time"
)

// panics counts every goroutine panic this package has contained. It is
// published in GET /v1/admin/diagnostics/concurrency: a server quietly eating
// panics is not a healthy server, and the counter is how that becomes visible
// instead of being buried in a log nobody reads.
var panics atomic.Uint64

// restarts counts supervised loops that had to be started again.
var restarts atomic.Uint64

// Panics reports how many goroutine panics have been contained.
func Panics() uint64 { return panics.Load() }

// Restarts reports how many supervised loops have been restarted.
func Restarts() uint64 { return restarts.Load() }

// note records one contained panic.
func note(name string, value any, stack []byte) {
	panics.Add(1)
	log.Printf("error: [server] panic contained in %s: %v\n%s", name, value, stack)
}

// Recover is the deferred half of containment, for a goroutine that spells its
// own body out rather than passing a closure:
//
//	go func() { defer supervise.Recover("name"); … }()
//
// It never re-panics, so the goroutine simply ends. Prefer Go; this exists for
// the handful of sites whose goroutine has to be started by a bare `go`.
func Recover(name string) {
	if value := recover(); value != nil {
		note(name, value, debug.Stack())
	}
}

// Go starts fn in a goroutine that cannot take the process down with it.
//
// name is what appears in the log and it is worth spending a moment on: it is
// the only thing an owner or a future reader has to locate the goroutine that
// failed. "catalog.search.group" beats "worker".
func Go(name string, fn func()) {
	go func() {
		defer Recover(name)
		fn()
	}()
}

// Backoff is how long a supervised loop waits before starting again. The first
// restart is immediate, because the overwhelming case is a bad row or a bad
// message that the next iteration will not see; from there it doubles, so a loop
// that panics on everything it touches does not spin.
var (
	// BackoffInitial is the wait after the second consecutive failure.
	BackoffInitial = 250 * time.Millisecond
	// BackoffMax caps it. A loop this broken needs an owner, not a faster retry.
	BackoffMax = 30 * time.Second
	// BackoffReset is how long a loop must run before its failures are treated as
	// unrelated rather than as a spin.
	BackoffReset = time.Minute
)

// Supervise runs fn until ctx ends, restarting it with backoff if it panics.
//
// Only for loops whose state is in the database rather than in the goroutine: a
// scanner between batches, a retention pass, a refresh ticker. A loop that would
// resume with half-built in-memory state belongs behind Go, where the panic ends
// that goroutine and nothing pretends it is still running.
func Supervise(ctx context.Context, name string, fn func(context.Context)) {
	Go(name, func() { Loop(ctx, name, fn) })
}

// Loop is Supervise's body, for a caller that already owns its goroutine.
func Loop(ctx context.Context, name string, fn func(context.Context)) {
	ctx = context.WithValue(ctx, operationKey{}, name)
	wait := BackoffInitial
	for ctx.Err() == nil {
		started := time.Now()
		if !runOnce(ctx, name, fn) {
			// A clean return means the loop decided it was done; ctx ending is the
			// usual reason and restarting would fight it.
			return
		}
		if ctx.Err() != nil {
			return
		}
		restarts.Add(1)
		if time.Since(started) >= BackoffReset {
			wait = BackoffInitial
			log.Printf("restarting %s after a contained panic", name)
			continue
		}
		log.Printf("restarting %s in %s after a contained panic", name, wait)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if wait *= 2; wait > BackoffMax {
			wait = BackoffMax
		}
	}
}

// runOnce runs fn and reports whether it panicked.
func runOnce(ctx context.Context, name string, fn func(context.Context)) (panicked bool) {
	defer func() {
		if value := recover(); value != nil {
			note(name, value, debug.Stack())
			panicked = true
		}
	}()
	fn(ctx)
	return false
}
