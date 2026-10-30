package playbackruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// NEW-28 part 2: a producer's readiness monitor stops it for a retune or an
// authority refusal, never for one failed check under load.
func TestLinearMonitorVerdict(t *testing.T) {
	busy := fmt.Errorf("begin: %w", errors.New("database is locked"))
	if stop, n := linearMonitorVerdict(true, nil, 3); !stop || n != 0 {
		t.Fatalf("a retune must stop the producer: %v %d", stop, n)
	}
	if stop, n := linearMonitorVerdict(false, nil, 4); stop || n != 0 {
		t.Fatalf("a clean check resets the count: %v %d", stop, n)
	}
	// The profile's Live TV switch turned off comes out of the resolver as a
	// control fault (liveTVAllowed): it stops the producer at once.
	if stop, _ := linearMonitorVerdict(false, liveTVAllowedFault(), 0); !stop {
		t.Fatal("a Live TV switch refusal must stop the producer at once")
	}
	for _, e := range []error{&playback.ControlFault{Code: "source_changed", HTTPStatus: 409}, &playback.ControlFault{Code: "lease_expired", HTTPStatus: 410}, identity.ErrUnauthorized, identity.ErrNotVisible, identity.ErrContentRestricted, identity.ErrForbidden, fmt.Errorf("wrapped: %w", identity.ErrContentRestricted)} {
		if stop, _ := linearMonitorVerdict(false, e, 0); !stop {
			t.Fatalf("%v must stop the producer", e)
		}
	}
	count := 0
	for tick := 1; tick < linearMonitorPatience; tick++ {
		var stop bool
		stop, count = linearMonitorVerdict(false, busy, count)
		if stop || count != tick {
			t.Fatalf("tick %d: a transient error stopped the producer (%v, %d)", tick, stop, count)
		}
	}
	if stop, _ := linearMonitorVerdict(false, busy, count); !stop {
		t.Fatal("a check that keeps failing must stop the producer eventually")
	}
	if stop, _ := linearMonitorVerdict(false, sql.ErrConnDone, 0); stop {
		t.Fatal("one infrastructure error must not stop the producer")
	}
}

// The monitor itself: one failed check (a busy database) followed by clean
// ones leaves the producer running and ready; ten failures in a row stop it,
// with the reason logged.
func TestLinearMonitorSurvivesOneFailedCheck(t *testing.T) {
	busy := errors.New("database is locked")
	for _, c := range []struct {
		name     string
		failures int
		stopped  bool
	}{{"one failure then clean", 1, false}, {"nine failures then clean", linearMonitorPatience - 1, false}, {"ten failures", linearMonitorPatience, true}} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ticks := make(chan time.Time)
			calls, readies := 0, 0
			check := func(context.Context) (bool, error) {
				calls++
				if calls <= c.failures {
					return false, busy
				}
				return false, nil
			}
			var logged []string
			logf := func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
			result := make(chan bool, 1)
			go func() { result <- linearMonitor(ctx, ticks, check, func(context.Context) { readies++ }, logf) }()
			for i := 0; i < c.failures+3; i++ {
				select {
				case ticks <- time.Now():
				case stopped := <-result:
					if !c.stopped {
						t.Fatalf("stopped after %d ticks: %v", i, logged)
					}
					if !stopped || len(logged) != 1 || !strings.Contains(logged[0], "database is locked") || !strings.Contains(logged[0], "failed checks in a row") {
						t.Fatalf("stop without its reason: %v %v", stopped, logged)
					}
					if readies != c.failures-1 {
						t.Fatalf("readiness ran %d times before the stop, want %d (every tick that didn't stop)", readies, c.failures-1)
					}
					return
				}
			}
			if c.stopped {
				t.Fatalf("still running after %d failures", c.failures)
			}
			cancel()
			if stopped := <-result; stopped || len(logged) != 0 {
				t.Fatalf("a surviving producer was stopped or logged: %v %v", stopped, logged)
			}
			// Readiness comes from the buffer on every tick that didn't stop
			// the producer, failed checks included: under load a slow or
			// failing check never keeps a playable channel in "preparing".
			if readies != c.failures+3 {
				t.Fatalf("readiness ran %d times, want %d", readies, c.failures+3)
			}
		})
	}
	// A retune stops it at once, logged.
	ctx := context.Background()
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	var logged []string
	if !linearMonitor(ctx, ticks, func(context.Context) (bool, error) { return true, nil }, func(context.Context) {}, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }) || len(logged) != 1 || !strings.Contains(logged[0], "retune") {
		t.Fatalf("retune: %v", logged)
	}
}

// liveTVAllowedFault is the error liveTVAllowed returns for a profile whose
// Live TV switch is off.
func liveTVAllowedFault() error { return channelFault("feature_restricted", 403) }
