package writer

import (
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"testing"
	"time"
)

func replaceEffectiveTestGovernor(t *testing.T, r *effectiveRuntime, clock func() time.Time) *governor.Authority {
	t.Helper()
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = r.plan.need
	}
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 2, Now: clock, WAL: r.wal})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.VerifyComposition(g, r.wal); err != nil {
		t.Fatal(err)
	}
	r.governor, r.authority = g, a
	return a
}

func TestEffectiveFacadeClockPanicAtEveryGrantStep(t *testing.T) {
	// Count the real public replay's governor clock boundaries, then inject at
	// EVERY boundary. In particular step2 is Check during binding after Try.
	w, cap, command, executor, _ := effectiveFacadeFixture(t)
	seedEffectiveReceipt(t, executor, command)
	calls := 0
	replaceEffectiveTestGovernor(t, w.effective, func() time.Time { calls++; return executor.now() })
	if _, err := w.ResolveEffective(t.Context(), cap, effectiveReference(command)); err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatal("binding boundary was not reached", calls)
	}
	for at := 1; at <= calls; at++ {
		t.Run(fmt.Sprintf("clock_%02d", at), func(t *testing.T) {
			w, cap, command, executor, _ := effectiveFacadeFixture(t)
			seedEffectiveReceipt(t, executor, command)
			primary := errors.New("injected governor clock boundary")
			count := 0
			a := replaceEffectiveTestGovernor(t, w.effective, func() time.Time {
				count++
				if count == at {
					panic(primary)
				}
				return executor.now()
			})
			func() {
				defer func() {
					if got := recover(); got != primary {
						t.Fatal("primary clock panic replaced or swallowed", got)
					}
				}()
				_, _ = w.ResolveEffective(t.Context(), cap, effectiveReference(command))
				t.Fatal("injection boundary not reached", at)
			}()
			if a.Usage() != ([4]governor.Amounts{}) || executor.db.Stats().InUse != 0 {
				t.Fatal("clock panic stranded grant/connection", at, a.Usage(), executor.db.Stats())
			}
			if !w.effective.fenced {
				t.Fatal("panicked runtime was not fenced")
			}
			if err := w.effective.wal.Publish(w.effective.wal.Observation(), governor.WALState{PageSize: 4096}, executor.now()); err != nil {
				t.Fatal("WAL writer reservation stranded", at, err)
			}
		})
	}
}
