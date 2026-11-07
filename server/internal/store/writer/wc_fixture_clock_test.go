package writer

import (
	"sync/atomic"
	"testing"
	"time"
)

// Default correctness tests exercise the real native opening, governor and
// command protocol without charging runner descheduling to the 25 ms quantum.
// Workload Actions and explicit capture runs select real time before any birth
// or governed turn. No product build or workload caller can override this clock.
func wcCompositionFixtureClock(t *testing.T, realClock bool) (now func() time.Time, warmup func()) {
	t.Helper()
	if realClock {
		return time.Now, func() {
			timer := time.NewTimer(2 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			}
		}
	}
	// Keep wall-clock context timers in the future, as in the N1 protocol
	// fixtures. Atomic ticks also support the concurrent lifecycle controls.
	origin := time.Now().Add(time.Hour)
	var ticks atomic.Int64
	return func() time.Time {
			return origin.Add(time.Duration(ticks.Add(1)) * time.Microsecond)
		}, func() {
			// A known positive playing interval, outside every command turn.
			ticks.Add(2000)
		}
}

func TestWCCompositionFixtureClockSeparatesRunnerDelay(t *testing.T) {
	protocolNow, _ := wcCompositionFixtureClock(t, false)
	realNow, _ := wcCompositionFixtureClock(t, true)
	protocolBefore, realBefore := protocolNow(), realNow()
	// Deliberately exceed the admitted quantum without executing a command.
	// Protocol controls must ignore this wall delay; measurement clocks must not.
	timer := time.NewTimer(30 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	if delta := protocolNow().Sub(protocolBefore); delta != time.Microsecond {
		t.Fatal("runner delay entered protocol time", delta)
	}
	if delta := realNow().Sub(realBefore); delta < 25*time.Millisecond {
		t.Fatal("workload clock lost wall time", delta)
	}
}
