package jobs

import (
	"context"
	"runtime/debug"
	"time"
)

// tidyStart is how long after the server starts the first round of housekeeping runs: after
// start-up's own work, and soon enough that a server restarted every day still gets it done.
var tidyStart = 10 * time.Minute

// tidy is one piece of housekeeping.
type tidy struct {
	name  string
	every time.Duration
	run   func(ctx context.Context) error
	next  time.Time
}

// Tidy registers housekeeping the server does by itself, whether or not anything else is
// going on: emptying trash whose days are up, refreshing what is due, sweeping files nothing
// uses. It runs first ten minutes after the server starts and then every so often. Register
// while the server starts.
//
// run decides for itself what is due and does it in pages of small Background writes, as a
// job does; run with nothing due must be cheap. One piece runs at a time. It is not a job:
// nobody asked for it, so there is nothing to watch or cancel, and it leaves no row.
func (r *Runner) Tidy(name string, every time.Duration, run func(ctx context.Context) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tidies = append(r.tidies, &tidy{name: name, every: every, run: run})
}

// RunTidy does the registered housekeeping until ctx ends.
func (r *Runner) RunTidy(ctx context.Context) {
	r.mu.Lock()
	tidies := append([]*tidy(nil), r.tidies...)
	r.mu.Unlock()
	start := time.Now().Add(tidyStart)
	for _, t := range tidies {
		t.next = start
	}
	for {
		var first *tidy
		for _, t := range tidies {
			if first == nil || t.next.Before(first.next) {
				first = t
			}
		}
		if first == nil {
			return
		}
		wait := time.NewTimer(time.Until(first.next))
		select {
		case <-ctx.Done():
			wait.Stop()
			return
		case <-wait.C:
		}
		r.tidyOnce(ctx, first)
		first.next = time.Now().Add(first.every)
	}
}

// tidyOnce runs one piece. A failure or a fault in it is logged and it is tried again at its
// next turn; it stops nothing else.
func (r *Runner) tidyOnce(ctx context.Context, t *tidy) {
	defer func() {
		if fault := recover(); fault != nil {
			r.log.Error("housekeeping panicked", "what", t.name, "panic", fault, "stack", string(debug.Stack()))
		}
	}()
	if err := t.run(ctx); err != nil && ctx.Err() == nil {
		r.log.Warn("housekeeping failed", "what", t.name, "error", err)
	}
}
