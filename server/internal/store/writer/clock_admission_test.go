package writer

import (
	"errors"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func backwardWallSample(t *testing.T, start time.Time) time.Time {
	t.Helper()
	sample := start.Add(time.Millisecond)
	wall := reflect.ValueOf(&sample).Elem().FieldByName("wall")
	// The test changes only wall time; the monotonic clock still advances.
	reflect.NewAt(wall.Type(), unsafe.Pointer(wall.UnsafeAddr())).Elem().SetUint(reflect.ValueOf(start.Add(-time.Millisecond)).FieldByName("wall").Uint())
	if sample.Sub(start) != time.Millisecond || !sample.Round(0).Before(start.Round(0)) {
		t.Fatal("invalid independent clock fixture")
	}
	return sample
}

func TestEffectiveBeginClockReadHighWater(t *testing.T) {
	w, clock, request, identity := effectiveBeginWholeFixture(t)
	input, err := newEffectiveStageBeginCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	base := *clock
	run := func(at int) (EffectiveStageBeginResult, error, int) {
		turn, _ := effectiveBeginWholeTurn(t, w)
		original := w.now
		calls := 0
		w.now = func() time.Time {
			calls++
			if calls == at {
				return base.Add(-time.Nanosecond)
			}
			return base
		}
		result, err := applyEffectiveStageBegin(t.Context(), turn, input)
		w.now = original
		if closeErr := turn.closeRows(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if _, rollbackErr := turn.transaction.rollback(nil); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		return result, err, calls
	}
	baseline, err, calls := run(0)
	if err != nil || baseline.ImageID <= 0 || calls < 6 {
		t.Fatal("missing native baseline", baseline, err, calls)
	}
	// Two lookup statements precede the lease read. Exactly three effects
	// follow the applied-time read: image insert, owner CAS and receipt insert.
	for name, at := range map[string]int{"lease": 3, "applied": calls - 3} {
		t.Run(name, func(t *testing.T) {
			result, err, _ := run(at)
			if result != (EffectiveStageBeginResult{}) || !errors.Is(err, ErrQuantum) {
				t.Fatal("unchecked domain clock escaped", result, err, at)
			}
		})
	}
}

func TestTransactionCommitStartClockHighWater(t *testing.T) {
	w, _, clock := testExecutor(t)
	base := *clock
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	calls := 0
	w.now = func() time.Time {
		calls++
		if calls == 2 {
			return base
		}
		return base.Add(time.Millisecond)
	}
	physical, err := tx.commit()
	if !errors.Is(err, ErrQuantum) || physical.state != rolledBack || tx.commitAttempted {
		t.Fatal("unchecked COMMIT-start clock admitted native work", physical, err, tx.commitAttempted)
	}
}

func TestRuntimeAdmissionClockHighWater(t *testing.T) {
	for _, mode := range []string{"wal_before", "before_try", "before_try_panic", "begin", "wal_after"} {
		t.Run(mode, func(t *testing.T) {
			w, cap, command, e, _ := effectiveFacadeFixture(t)
			base := time.Now().Add(time.Hour)
			backward := backwardWallSample(t, base)
			a := replaceEffectiveTestGovernor(t, w.effective, func() time.Time { return base.Add(5 * time.Millisecond) })
			e.now = func() time.Time { return base }
			if mode == "wal_before" {
				if _, err := w.ResolveEffective(t.Context(), cap, effectiveReference(command)); err != nil {
					t.Fatal("initial observed clock", err)
				}
			}
			afterCommit := false
			var fault *faultConnector
			if mode == "wal_after" {
				if err := e.db.Close(); err != nil {
					t.Fatal(err)
				}
				connector, err := sqliteconn.Connector(w.effective.path, e.config)
				if err != nil {
					t.Fatal(err)
				}
				fault = &faultConnector{Connector: connector, afterNativeCommit: func() { afterCommit = true }}
				installFaultPool(e, fault)
				primeEffectiveFixturePool(t, e)
			}
			calls := 0
			primary := errors.New("injected pre-Try clock panic")
			e.now = func() time.Time {
				calls++
				switch mode {
				case "wal_before":
					return backward
				case "before_try":
					if calls == 1 {
						return base
					}
					if calls == 2 {
						return backward
					}
				case "before_try_panic":
					if calls == 2 {
						panic(primary)
					}
				case "begin":
					if calls == 1 {
						return base
					}
					if calls == 2 {
						return base.Add(2 * time.Millisecond)
					}
					if calls == 3 {
						return base.Add(time.Millisecond)
					}
				case "wal_after":
					if afterCommit {
						return base.Add(time.Millisecond)
					}
					if calls >= 4 {
						return base.Add(2 * time.Millisecond)
					}
					return base
				}
				return base.Add(3 * time.Millisecond)
			}
			if mode == "before_try_panic" {
				func() {
					defer func() {
						if got := recover(); got != primary {
							t.Fatal("pre-Try primary panic changed", got)
						}
					}()
					_, _ = w.ResolveEffective(t.Context(), cap, effectiveReference(command))
					t.Fatal("pre-Try clock panic was swallowed")
				}()
			} else {
				result, err := w.ResolveEffective(t.Context(), cap, effectiveReference(command))
				if !reflect.DeepEqual(result, EffectiveStageResult{}) || !errors.Is(err, ErrQuantum) {
					t.Fatal("backward admission clock escaped", result, err)
				}
			}
			if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
				t.Fatal("clock refusal leaked custody", a.Usage(), e.db.Stats())
			}
			if fault != nil && fault.commits.Load() != 1 {
				t.Fatal("post-COMMIT clock refusal erased physical outcome", fault.commits.Load())
			}
			if mode == "wal_after" {
				e.now = func() time.Time { return base.Add(time.Millisecond) }
				retry, err := w.ResolveEffective(t.Context(), cap, effectiveReference(command))
				if !reflect.DeepEqual(retry, EffectiveStageResult{}) || !errors.Is(err, ErrQuantum) {
					t.Fatal("failed post-return sample lost the turn high-water", retry, err)
				}
			}
			if _, err := w.effective.wal.RestartRisk(base.Add(4 * time.Millisecond)); err == nil {
				t.Fatal("backward observation left WAL headroom admissible")
			}
		})
	}
}
