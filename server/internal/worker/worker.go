// Package worker is the shape every background loop in this server has: wait
// for something to do, do it, wait again.
//
// About fifteen loops polled the database on a fixed 250 millisecond to one
// second timer whether or not there was anything to do. On an idle server with
// nobody connected that was roughly two thousand three hundred database calls a
// minute and about one per cent of a core, spent entirely on asking questions
// whose answer was "nothing" — and it means a disk that can never spin down,
// which on the old, weak hardware this has to run well on is the difference
// between a machine that idles quietly and one that never rests.
//
// A loop here sleeps until one of three things happens: somebody signals that
// work was enqueued, the time it said its next work was due arrives, or a slow
// safety tick fires. The safety tick exists because a missed signal must cost
// latency, never correctness; it is minutes rather than milliseconds, so an idle
// server is genuinely idle.
package worker

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// SafetyTick is how long a loop waits when it knows of nothing to do and nobody
// has signalled it. It is the backstop for a wake that was never sent, so it is
// measured in minutes: the cost of being wrong is that one job starts late, and
// the cost of being frequent is a server that never idles.
var SafetyTick = 5 * time.Minute

// Signal is a coalescing wake. Any number of enqueues between two passes produce
// at most one wake, which is what makes it safe to signal from a hot path.
type Signal struct{ c chan struct{} }

// NewSignal returns a signal with no wake pending.
func NewSignal() *Signal { return &Signal{c: make(chan struct{}, 1)} }

// Wake tells the loop there is something to do. It never blocks.
func (s *Signal) Wake() {
	if s == nil {
		return
	}
	select {
	case s.c <- struct{}{}:
	default:
	}
}

// Take consumes a pending wake without blocking and reports whether there was
// one. A loop that also runs on its own schedule uses it to tell "something
// changed since I last looked" from "my timer fired".
func (s *Signal) Take() bool {
	if s == nil {
		return false
	}
	select {
	case <-s.c:
		return true
	default:
		return false
	}
}

// Wait blocks until a real change is signalled or the caller stops. Unlike
// Run, it has no safety timer; use it only when the work has a durable dirty
// queue and every producer wakes the signal on commit.
func (s *Signal) Wait(ctx context.Context) bool {
	if s == nil {
		<-ctx.Done()
		return false
	}
	select {
	case <-ctx.Done():
		return false
	case <-s.c:
		return true
	}
}

// channel is the receive side, nil-safe so a loop without a signal still works.
func (s *Signal) channel() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.c
}

// Step does one pass and says when it should next run: zero means "nothing is
// due that I know of", in which case the loop waits for a signal or the safety
// tick. A step that found work should return a short interval — there is usually
// more behind it — rather than relying on a second signal.
type Step func(ctx context.Context) time.Duration

// MinSignalInterval is the closest together two passes may be when the second
// was caused by a signal rather than by the step's own schedule.
//
// It exists because the commit signal is deliberately coarse: a loop's own
// writes, and every other loop's, wake everybody. Without a floor that is a
// feedback loop — which is exactly what happened the first time this was wired
// up, and it turned two thousand idle calls a minute into two hundred thousand.
// With the floor, the worst case is the polling interval these loops used to
// have anyway, and the idle case is still nothing at all.
var MinSignalInterval = time.Second

// Run drives step until ctx ends, woken by signal. It runs once immediately,
// because a server that has just started has a backlog by definition.
func Run(ctx context.Context, name string, signal *Signal, step Step) {
	RunWith(ctx, name, nil, signal, step)
}

// RunWith separates the two kinds of wake. An urgent signal comes from the code
// that just created the work and is honoured at once; a shared signal — the
// commit wake, which everybody hears — is floored, because without a floor a
// loop's own writes wake it again immediately and the whole server spins.
func RunWith(ctx context.Context, name string, urgent, shared *Signal, step Step) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	lastPass := time.Time{}
	for {
		if ctx.Err() != nil {
			return
		}
		lastPass = time.Now()
		wait := step(ctx)
		if wait <= 0 || wait > SafetyTick {
			wait = SafetyTick
		}
		timer.Reset(wait)
		floored := false
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-urgent.channel():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-shared.channel():
			floored = true
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
		// The floor applies only to the shared wake, and only ever makes the next
		// pass later than the wake asked for — never later than the step's own
		// schedule. A step that asked to run again in fifty milliseconds gets
		// fifty milliseconds whatever woke it; a loop shaken awake by somebody
		// else's commit while it had nothing of its own to do waits out the
		// interval these loops used to poll at.
		if floored {
			bound := wait
			if MinSignalInterval < bound {
				bound = MinSignalInterval
			}
			if remaining := bound - time.Since(lastPass); remaining > 0 {
				timer.Reset(remaining)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					return
				case <-urgent.channel():
					// The code that created the work says so directly, so the floor
					// — which exists only to stop the shared commit wake spinning the
					// loop — does not apply. The two can arrive a microsecond apart
					// and select picks between ready cases at random, so this is
					// checked here rather than before the wait.
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
				case <-timer.C:
				}
			}
		}
	}
}

// Go starts Run in a contained goroutine and returns a channel that closes when
// it has stopped, which is what the shutdown barrier waits on.
func Go(ctx context.Context, name string, signal *Signal, step Step) <-chan struct{} {
	done := make(chan struct{})
	supervise.Go(name, func() {
		defer close(done)
		Run(ctx, name, signal, step)
	})
	return done
}

// Progress is the "did anything happen?" probe a loop uses when its steps do
// not say so themselves. A lane can supply its own context-scoped committed
// change count, so other actors cannot make an idle pass look productive. A
// scheduler that intentionally follows all actors can use a process-wide count.
type Progress struct {
	count func() uint64
	mark  uint64
}

// NewProgress starts watching the caller's chosen changing-commit count, not
// gate acquisitions: a transaction that wrote nothing still takes the gate,
// and a loop watching acquisitions would see itself and never settle.
func NewProgress(count func() uint64) *Progress {
	p := &Progress{count: count}
	p.Reset()
	return p
}

// Reset takes a new mark.
func (p *Progress) Reset() {
	if p != nil && p.count != nil {
		p.mark = p.count()
	}
}

// Moved reports whether anything was written since the last Reset, and takes a
// new mark.
func (p *Progress) Moved() bool {
	if p == nil || p.count == nil {
		return false
	}
	now := p.count()
	moved := now != p.mark
	p.mark = now
	return moved
}
