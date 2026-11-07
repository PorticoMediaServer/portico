package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
	"unsafe"
)

func TestInventoryTimeAndLeaseBoundaries(t *testing.T) {
	w, _, clock := testExecutor(t)
	for _, name := range []string{"live", "deadline_equal_lease", "expired", "expired_equal", "short_lease", "deadline_elapsed", "backward", "backward_after_step", "zero_lease", "negative_lease", "caller_cancel", "turn_cancel", "cancel_on_clock", "turn_cancel_on_clock", "wrong_purpose", "closed", "negative_time", "overflow_time"} {
		t.Run(name, func(t *testing.T) {
			base := time.Now().Add(time.Hour).Truncate(time.Millisecond)
			if name == "overflow_time" {
				base = time.Unix(1<<62, 0)
			}
			*clock = base
			purpose := InventoryAccept
			if name == "wrong_purpose" {
				purpose = BasicAccept
			}
			custody := &testCustody{}
			tx, err := w.begin(t.Context(), purpose, testRecipe(), custody)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.rollback(nil)
			turn := &commandTurn{transaction: tx}
			caller, cancel := context.WithCancel(t.Context())
			defer cancel()
			expires := base.Add(time.Minute).UnixMilli()
			var want error
			oldNow := w.now
			switch name {
			case "deadline_equal_lease":
				expires = base.Add(MaxCommandSafetyHold).UnixMilli()
			case "expired":
				expires = base.Add(-time.Millisecond).UnixMilli()
				want = ErrConflict
			case "expired_equal":
				expires = base.UnixMilli()
				want = ErrConflict
			case "short_lease":
				expires = base.Add(MaxCommandSafetyHold - time.Millisecond).UnixMilli()
				want = ErrConflict
			case "deadline_elapsed":
				*clock = base.Add(MaxCommandSafetyHold)
				want = context.DeadlineExceeded
			case "backward":
				*clock = base.Add(-time.Nanosecond)
				want = ErrQuantum
			case "backward_after_step":
				*clock = base.Add(2 * time.Millisecond)
				if err := tx.before(Work{1, 1, 1, 1, time.Microsecond}); err != nil {
					t.Fatal(err)
				}
				*clock = base.Add(time.Millisecond)
				want = ErrQuantum
			case "zero_lease":
				expires = 0
				want = ErrInvalid
			case "negative_lease":
				expires = -1
				want = ErrInvalid
			case "caller_cancel":
				cancel()
				want = context.Canceled
			case "turn_cancel":
				tx.cancel()
				want = context.Canceled
			case "cancel_on_clock":
				w.now = func() time.Time { cancel(); return base }
				want = context.Canceled
			case "turn_cancel_on_clock":
				w.now = func() time.Time { tx.cancel(); return base }
				want = context.Canceled
			case "wrong_purpose":
				want = ErrInvalid
			case "closed":
				if _, err := tx.rollback(nil); err != nil {
					t.Fatal(err)
				}
				want = ErrClosed
			case "negative_time":
				w.now = func() time.Time { return time.Unix(-1000, 0) }
				want = ErrInvalid
			case "overflow_time":
				expires = 1<<63 - 1
				want = ErrInvalid
			}
			got, err := turn.inventoryTimeMS(caller, expires)
			w.now = oldNow
			if want == nil {
				if err != nil || got != base.UnixMilli() {
					t.Fatal(got, err)
				}
			} else if !errors.Is(err, want) || got != 0 {
				t.Fatal("untrusted timestamp escaped", got, err, want)
			}
			// Restore the virtual clock before private physical cleanup.
			*clock = base
		})
	}
}

func TestInventoryTerminalRechecksWithoutChangingAcceptedTime(t *testing.T) {
	w, _, clock := testExecutor(t)
	base := clock.Truncate(time.Millisecond)
	*clock = base
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	turn := &commandTurn{transaction: tx}
	input := &inventoryAcceptCommand{valid: true, request: inventoryAcceptRequest{run: inventoryRun{leaseExpiresMS: base.Add(time.Minute).UnixMilli()}}}
	accepted, err := turn.inventoryTimeMS(t.Context(), input.request.run.leaseExpiresMS)
	if err != nil {
		t.Fatal(err)
	}
	*clock = base.Add(time.Millisecond)
	if err := checkInventoryTerminal(t.Context(), turn, input); err != nil {
		t.Fatal(err)
	}
	if accepted != base.UnixMilli() {
		t.Fatal("terminal recheck rewrote accepted time")
	}
	*clock = base.Add(time.Minute)
	if err := checkInventoryTerminal(t.Context(), turn, input); !errors.Is(err, ErrConflict) {
		t.Fatal("expired lease reached terminal", err)
	}
	*clock = base
}

func TestInventoryTimeInvalidAndClockPanic(t *testing.T) {
	for _, turn := range []*commandTurn{nil, {}, {transaction: &transaction{}}} {
		if ms, err := turn.inventoryTimeMS(t.Context(), 1); ms != 0 || !errors.Is(err, ErrInvalid) {
			t.Fatal(ms, err)
		}
	}
	w, _, _ := testExecutor(t)
	custody := &testCustody{}
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), custody)
	if err != nil {
		t.Fatal(err)
	}
	turn := &commandTurn{transaction: tx}
	if err := checkInventoryTerminal(t.Context(), turn, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	boom := errors.New("injected trusted clock panic")
	old := w.now
	w.now = func() time.Time { panic(boom) }
	func() {
		defer func() {
			if got := recover(); got != boom {
				t.Fatalf("primary panic replaced: %v", got)
			}
		}()
		_, _ = turn.inventoryTimeMS(t.Context(), 1<<63-1)
	}()
	w.now = old
	if !tx.done || !tx.cleanupComplete || !w.fenced || custody.returns != 1 || w.db.Stats().InUse != 0 {
		t.Fatal("clock panic leaked physical custody", tx.done, tx.cleanupComplete, w.fenced, custody.returns)
	}
}

func TestInventoryTurnTimeHighWater(t *testing.T) {
	w, _, clock := testExecutor(t)
	for _, mode := range []string{"helper_then_helper", "helper_then_statement_and_terminal", "helper_then_commit"} {
		t.Run(mode, func(t *testing.T) {
			base := time.Now().Add(time.Hour).Truncate(time.Millisecond)
			*clock = base
			tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.rollback(nil)
			turn := &commandTurn{transaction: tx}
			input := &inventoryAcceptCommand{valid: true, request: inventoryAcceptRequest{run: inventoryRun{leaseExpiresMS: base.Add(time.Minute).UnixMilli()}}}
			*clock = base.Add(2 * time.Millisecond)
			accepted, err := turn.inventoryTimeMS(t.Context(), input.request.run.leaseExpiresMS)
			if err != nil || accepted != clock.UnixMilli() {
				t.Fatal(accepted, err)
			}
			*clock = base.Add(time.Millisecond)
			switch mode {
			case "helper_then_helper":
				if got, err := turn.inventoryTimeMS(t.Context(), input.request.run.leaseExpiresMS); got != 0 || !errors.Is(err, ErrQuantum) {
					t.Errorf("backward helper sample escaped: %d %v", got, err)
				}
			case "helper_then_statement_and_terminal":
				remaining := tx.quantum.Remaining()
				if err := tx.before(Work{1, 1, 1, 1, time.Microsecond}); !errors.Is(err, ErrQuantum) {
					t.Errorf("statement admitted before acceptance time: %v", err)
				}
				if tx.quantum.Remaining() != remaining {
					t.Error("backward statement consumed budget")
				}
				if err := checkInventoryTerminal(t.Context(), turn, input); !errors.Is(err, ErrQuantum) {
					t.Errorf("terminal preceded accepted time: %v", err)
				}
			case "helper_then_commit":
				physical, err := tx.commit()
				if !errors.Is(err, ErrQuantum) || physical.state != rolledBack || tx.commitAttempted {
					t.Errorf("backward terminal admission committed: %+v %v", physical, err)
				}
			}
			*clock = base.Add(3 * time.Millisecond)
		})
	}
}

func TestTransactionSetupWallClockHighWater(t *testing.T) {
	w, _, _ := testExecutor(t)
	start := time.Now().Add(time.Hour)
	sample := start.Add(time.Millisecond)
	wall := reflect.ValueOf(&sample).Elem().FieldByName("wall")
	// Simulate a wall-clock correction while the monotonic clock advances.
	// Go has no public API for setting those clocks independently. Keep this
	// representation access test-only, and verify both orderings explicitly.
	reflect.NewAt(wall.Type(), unsafe.Pointer(wall.UnsafeAddr())).Elem().SetUint(reflect.ValueOf(start.Add(-time.Millisecond)).FieldByName("wall").Uint())
	if sample.Sub(start) != time.Millisecond || !sample.Round(0).Before(start.Round(0)) {
		t.Fatal("fixture did not preserve forward monotonic/backward wall time")
	}
	calls := 0
	w.now = func() time.Time {
		calls++
		switch calls {
		case 1:
			return start
		case 2:
			return sample
		default:
			return start.Add(2 * time.Millisecond)
		}
	}
	custody := &testCustody{}
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), custody)
	if tx != nil {
		defer tx.rollback(nil)
	}
	if tx != nil || !errors.Is(err, ErrQuantum) {
		t.Fatalf("setup admitted backward wall sample: tx=%v err=%v", tx != nil, err)
	}
	if custody.checks != 1 || custody.returns != 1 || w.db.Stats().InUse != 0 {
		t.Fatal("setup refusal entered native work or leaked custody", custody, w.db.Stats())
	}
	if !w.mu.TryLock() {
		t.Fatal("setup refusal stranded writer mutex")
	}
	w.mu.Unlock()
}
