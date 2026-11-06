package writer

import (
	"context"
	"errors"
	"testing"
	"time"
)

func ownerWindowOrder(t *testing.T, times ...time.Time) {
	t.Helper()
	for i, at := range times {
		if at.IsZero() || (i > 0 && at.Before(times[i-1])) {
			t.Fatalf("owner boundary %d missing or out of order: %v", i, times)
		}
	}
}

func TestOwnerWindowCommitAndRollback(t *testing.T) {
	for _, commit := range []bool{true, false} {
		t.Run(map[bool]string{true: "commit", false: "rollback"}[commit], func(t *testing.T) {
			w, path, _ := testExecutor(t)
			fault := panicDriver(t, w, path)
			var beginReturned time.Time
			fault.afterNative = func(q string, _ context.Context) {
				if q == "BEGIN IMMEDIATE" {
					beginReturned = time.Now()
				}
			}
			c := &testCustody{}
			tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), c)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.rollback(nil)
			setup := tx.setupReturnedAt
			if !tx.setupComplete || !tx.terminalEnteredAt.IsZero() {
				t.Fatal("setup claimed a terminal boundary")
			}
			ownerWindowOrder(t, tx.setupEnteredAt, beginReturned, setup, time.Now())
			fault.beforeNative = func(q string, _ context.Context) {
				if q == "COMMIT" || q == "ROLLBACK" {
					ownerWindowOrder(t, setup, tx.terminalEnteredAt, time.Now())
					if !tx.terminalReturnedAt.IsZero() || !tx.nativeReturnedAt.IsZero() {
						t.Fatal("terminal boundary published before native work")
					}
				}
			}
			c.retireHook = func() {
				ownerWindowOrder(t, tx.terminalEnteredAt, tx.connectionReturnedAt, tx.nativeReturnedAt, tx.custodyRetirementStartedAt, time.Now())
				if !tx.custodyRetiredAt.IsZero() || !tx.custodyRetirementReturnedAt.IsZero() || !tx.terminalReturnedAt.IsZero() {
					t.Fatal("retirement boundary published before custody returned")
				}
			}
			var result turnResult
			if commit {
				result, err = tx.commit()
			} else {
				result, err = tx.rollback(nil)
			}
			if err != nil {
				t.Fatal(result, err)
			}
			ownerWindowOrder(t, tx.setupEnteredAt, setup, tx.terminalEnteredAt, tx.connectionReturnedAt, tx.custodyRetirementStartedAt, tx.custodyRetiredAt, tx.terminalReturnedAt, time.Now())
			if tx.setupReturnedAt != setup || tx.custodyRetiredAt != tx.custodyRetirementReturnedAt {
				t.Fatal("saved boundary changed")
			}
			if commit {
				if result.state != committed || !tx.rollbackStartedAt.IsZero() || !tx.rollbackReturnedAt.IsZero() {
					t.Fatal("invented rollback")
				}
			} else {
				if result.state != rolledBack {
					t.Fatal(result)
				}
				ownerWindowOrder(t, tx.terminalEnteredAt, tx.rollbackStartedAt, tx.rollbackReturnedAt, tx.connectionReturnedAt)
			}
			observation, ok := w.lastOwnerWindow()
			if !ok || observation.ownerWindow != tx.ownerWindow || !observation.setupComplete || !observation.cleanupComplete || !observation.retirementComplete || !observation.policyVerified || observation.settings.Synchronous != 1 {
				t.Fatal(observation)
			}
			saved := observation
			observation.setupReturnedAt = time.Time{}
			again, _ := w.lastOwnerWindow()
			if again != saved {
				t.Fatal("observation exposed mutable owner state")
			}
			if _, err := tx.rollback(nil); !errors.Is(err, ErrClosed) {
				t.Fatal(err)
			}
			again, _ = w.lastOwnerWindow()
			if again != saved {
				t.Fatal("repeated release rewrote owner boundaries")
			}
		})
	}
}

func TestOwnerWindowUnknownCommitIncludesDiscardAndRetirement(t *testing.T) {
	w, path, _ := testExecutor(t)
	fault := panicDriver(t, w, path)
	fault.replyError, fault.afterCommit = true, true
	c := &testCustody{closed: &fault.closed}
	tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	var nativeCloseEntered, nativeCloseReturned time.Time
	fault.beforeClose = func() {
		nativeCloseEntered = time.Now()
		ownerWindowOrder(t, tx.terminalEnteredAt, nativeCloseEntered)
		if !tx.nativeReturnedAt.IsZero() {
			t.Fatal("native return before discard close")
		}
	}
	fault.afterClose = func() { nativeCloseReturned = time.Now() }
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) || !fault.closed.Load() {
		t.Fatal(result, err)
	}
	ownerWindowOrder(t, tx.terminalEnteredAt, nativeCloseEntered, nativeCloseReturned, tx.connectionReturnedAt, tx.custodyRetiredAt, tx.poolCloseStartedAt, tx.poolCloseReturnedAt, tx.terminalReturnedAt)
	if tx.nativeReturnedAt != tx.poolCloseReturnedAt || !tx.rollbackStartedAt.IsZero() {
		t.Fatal("discard/unknown endpoints were inferred or rollback invented")
	}
	o, ok := w.lastOwnerWindow()
	if !ok || o.state != commitUnknown || !o.retirementComplete || !o.connectionReturned {
		t.Fatal(o)
	}
}

func TestOwnerWindowFailedSetupAndPanicRetainPartialFacts(t *testing.T) {
	for _, panicAtBegin := range []bool{false, true} {
		t.Run(map[bool]string{false: "acquire_error", true: "begin_panic"}[panicAtBegin], func(t *testing.T) {
			w, path, _ := testExecutor(t)
			fault := panicDriver(t, w, path)
			var reached time.Time
			if panicAtBegin {
				fault.beforeNative = func(q string, _ context.Context) {
					if q == "BEGIN IMMEDIATE" {
						reached = time.Now()
						panic(errDriverPanic)
					}
				}
			}
			c := &testCustody{}
			if !panicAtBegin {
				c.checkHook = func() { reached = time.Now(); c.checkErr = ErrInvalid }
			}
			var tx *transaction
			var err error
			var primary any
			func() {
				defer func() { primary = recover() }()
				tx, err = w.begin(t.Context(), ProgressAccept, testRecipe(), c)
			}()
			if tx != nil {
				defer tx.rollback(nil)
			}
			if tx != nil || c.returns != 1 {
				t.Fatal("failed setup leaked a frame", tx, c)
			}
			if panicAtBegin {
				if primary != errDriverPanic {
					t.Fatal(primary)
				}
			} else if !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
			o, ok := w.lastOwnerWindow()
			if !ok || o.setupComplete || !o.cleanupComplete || !o.retirementComplete || o.commandID != ProgressAccept {
				t.Fatal(o)
			}
			ownerWindowOrder(t, o.setupEnteredAt, reached, o.terminalEnteredAt, o.custodyRetiredAt, o.terminalReturnedAt)
			if o.setupReturnedAt != o.terminalReturnedAt {
				t.Fatal("failed setup omitted cleanup")
			}
			if !panicAtBegin && (!o.connectionReturnedAt.IsZero() || !o.nativeReturnedAt.IsZero()) {
				t.Fatal("invented native acquisition/return")
			}
			if !o.rollbackStartedAt.IsZero() {
				t.Fatal("invented rollback for incomplete BEGIN")
			}
		})
	}
}

func TestOwnerWindowFailedRetirementDoesNotClaimRelease(t *testing.T) {
	w, _, _ := testExecutor(t)
	c := &testCustody{returnErr: ErrInvalid}
	tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	result, err := tx.rollback(nil)
	if result.state != rolledBack || !errors.Is(err, ErrInvalid) || !tx.custodyRetiredAt.IsZero() {
		t.Fatal(result, err)
	}
	ownerWindowOrder(t, tx.terminalEnteredAt, tx.custodyRetirementStartedAt, tx.custodyRetirementReturnedAt, tx.poolCloseReturnedAt, tx.terminalReturnedAt)
	o, ok := w.lastOwnerWindow()
	if !ok || o.retirementComplete || o.custodyRetirementReturnedAt.IsZero() {
		t.Fatal(o)
	}
}

func TestOwnerWindowBusyHasNoNewObservation(t *testing.T) {
	w, _, _ := testExecutor(t)
	if _, ok := w.lastOwnerWindow(); ok {
		t.Fatal("invented initial turn")
	}
	tx, err := w.begin(t.Context(), ProgressAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	c := &testCustody{}
	if busy, err := w.begin(t.Context(), ProgressAccept, testRecipe(), c); busy != nil || !errors.Is(err, ErrBusy) || c.returns != 0 {
		t.Fatal(busy, err, c)
	}
	if _, ok := w.lastOwnerWindow(); ok {
		t.Fatal("busy call invented an observation")
	}
}
