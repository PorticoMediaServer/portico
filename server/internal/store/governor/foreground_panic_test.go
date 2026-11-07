package governor

import (
	"context"
	"testing"
	"time"
)

type foregroundPanicContext struct {
	context.Context
	point string
	errs  int
	value any
}

func (c *foregroundPanicContext) Deadline() (time.Time, bool) {
	if c.point == "before" {
		panic(c.value)
	}
	return c.Context.Deadline()
}
func (c *foregroundPanicContext) Err() error {
	c.errs++
	// The fourth Err is the post-grant handoff check, after the two new
	// bounded-admission entry/handoff checks; keep the ownership fault there.
	if c.point == "grant" && c.errs == 4 {
		panic(c.value)
	}
	return c.Context.Err()
}
func (c *foregroundPanicContext) Done() <-chan struct{} {
	if c.point == "wait" {
		panic(c.value)
	}
	return c.Context.Done()
}

func TestForegroundAdmissionPanicMatrix(t *testing.T) {
	// The eight review points plus the promoted final-clock variant. No native
	// IO or success deadline is involved; clock/context faults are deterministic.
	for _, point := range []string{"before", "admit", "wait", "wait-clock", "grant", "grant-clock", "promoted-grant", "release", "promoted-clock"} {
		t.Run(point, func(t *testing.T) {
			gov, a, clock := foregroundSetup(t)
			g := gov.(*ledger)
			q := foregroundQ(clock)
			marker := &struct{ point string }{point}
			ctx := &foregroundPanicContext{Context: t.Context(), point: point, value: marker}
			var all []*Grant
			if point == "wait" || point == "wait-clock" || point == "promoted-grant" || point == "promoted-clock" {
				all = fillLive(t, a, q)
			}
			if point == "promoted-grant" || point == "promoted-clock" {
				ctx.point = "grant"
				if point == "promoted-clock" {
					ctx.point = ""
				}
				g.foregroundWait = func(_ context.Context, _ time.Time, wake <-chan struct{}) {
					retire(t, a, all[0])
					all = all[1:]
					<-wake
				}
			}
			var released *Grant
			if point == "release" {
				released = admitLive(t, a, q)
			}
			calls := 0
			g.config.Now = func() time.Time {
				calls++
				if (point == "admit" && calls == 2) || ((point == "grant-clock" || point == "wait-clock") && calls == 3) || (point == "promoted-clock" && calls == 4) || point == "release" {
					panic(marker)
				}
				return clock.now()
			}
			var caught any
			func() {
				defer func() { caught = recover() }()
				if point == "release" {
					_ = a.Retired(released)
				} else {
					_ = a.AdmitForeground(ctx, q)
				}
			}()
			if caught != marker {
				t.Error("primary panic changed", caught)
			}
			unlocked := g.mu.TryLock()
			if !unlocked {
				g.mu.Unlock()
				t.Error("panic left ledger mutex locked")
			} else {
				g.mu.Unlock()
			}
			g.config.Now = clock.now
			u := a.ForegroundUsage()
			if u.Live != len(all) || u.Waiters != 0 || a.Usage()[Foreground][MemoryBytes] != int64(len(all))*4096 {
				t.Error("panic stranded metadata", u, a.Usage())
			}
			// On a failing mutant, clean leaked metadata too, without leaving a
			// stalled goroutine/test or masking the assertions above.
			var remaining []*Grant
			for _, h := range g.live {
				remaining = append(remaining, h.grant)
			}
			retireLive(t, a, remaining)
		})
	}
}

func TestForegroundPanicRollbackPreservesPrimary(t *testing.T) {
	gov, a, clock := foregroundSetup(t)
	g := gov.(*ledger)
	q := foregroundQ(clock)
	all := fillLive(t, a, q)
	primary, secondary := new(int), new(int)
	ctx := &foregroundPanicContext{Context: t.Context(), point: "wait", value: primary}
	calls := 0
	g.config.Now = func() time.Time {
		calls++
		if calls >= 4 {
			panic(secondary)
		}
		return clock.now()
	}
	var caught any
	func() { defer func() { caught = recover() }(); _ = a.AdmitForeground(ctx, q) }()
	if caught != primary {
		t.Error("cleanup replaced primary panic", caught)
	}
	if !g.mu.TryLock() {
		g.mu.Unlock()
		t.Error("cleanup locked ledger")
	} else {
		g.mu.Unlock()
	}
	g.config.Now = clock.now
	if a.ForegroundUsage().Live != 8 || a.ForegroundUsage().Waiters != 0 || a.Usage()[Foreground][MemoryBytes] != 8*4096 {
		t.Error("secondary fault lost metadata retirement", a.ForegroundUsage(), a.Usage())
	}
	retireLive(t, a, all)
}
