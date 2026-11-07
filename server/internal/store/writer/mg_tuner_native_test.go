package writer

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func mgTunerFixture(t *testing.T, purpose CommandID) (*executor, *commandTurn, mgTunerRequest) {
	t.Helper()
	w := mgSourceTestExecutor(t)
	for _, q := range []string{
		`CREATE INDEX live_allocations_source_state_channel ON live_allocations(source_id,state,channel_id,id)`,
		`UPDATE live_source_identities SET settings_physical_count=2 WHERE id='source'`,
		`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES('old','channel','p','Channel','1','',0,x'01','','')`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	defs := append(mgSourceDefinitions(), mgTunerDefinitions()...)
	turn := mgSourceTestTurn(t, w, MGConfigureTuner, defs)
	turn.transaction.descriptor.ID = purpose
	now := w.now().UnixMilli()
	return w, turn, mgTunerRequest{mgTunerAllocation{"alloc", "source", "channel", "resource", "live", "local", "account", "profile", "token", 1, "active", now + 10000, now, 0}, "old", now + 20000}
}
func TestMGTunerNativeAcquireRenew(t *testing.T) {
	_, turn, r := mgTunerFixture(t, MGAcquireAllocation)
	got, err := acquireMGTuner(t.Context(), turn, r)
	if err != nil || got != r.allocation {
		t.Fatal(got, err)
	}
	if _, err = acquireMGTuner(t.Context(), turn, r); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate", err)
	}
	turn.transaction.descriptor.ID = MGRenewAllocation
	before := got
	r.allocation.lease += 1000
	if got, err = renewMGTuner(t.Context(), turn, before, r); err != nil || got != r.allocation {
		t.Fatal(got, err)
	}
	if _, err = renewMGTuner(t.Context(), turn, before, r); !errors.Is(err, ErrConflict) {
		t.Fatal("stale", err)
	}
}
func TestMGTunerNativeScopeAndCancellation(t *testing.T) {
	for _, mode := range []string{"scope", "guide", "owner", "cancel", "boundary"} {
		t.Run(mode, func(t *testing.T) {
			_, turn, r := mgTunerFixture(t, MGAcquireAllocation)
			ctx := t.Context()
			switch mode {
			case "scope":
				turn.transaction.descriptor.ID = MGRenewAllocation
			case "guide":
				r.guide = "new"
			case "owner":
				r.allocation.authority = "remote"
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "boundary":
				r.protectedUntil = r.allocation.lease - 1
			}
			if got, err := acquireMGTuner(ctx, turn, r); err == nil || got != (mgTunerAllocation{}) {
				t.Fatal(got, err)
			}
		})
	}
}
func TestMGTunerNativeHeldCapacity(t *testing.T) {
	for _, channels := range [][]string{{"a", "a", "a"}, {"a", "b", "c"}} {
		t.Run(fmt.Sprint(channels), func(t *testing.T) {
			w := mgSourceTestExecutor(t)
			if _, err := w.db.ExecContext(t.Context(), `CREATE INDEX live_allocations_source_state_channel ON live_allocations(source_id,state,channel_id,id)`); err != nil {
				t.Fatal(err)
			}
			for i, ch := range channels {
				state := "active"
				if i%2 == 1 {
					state = "preempting"
				}
				a := mgTunerAllocation{fmt.Sprint(i), "source", ch, fmt.Sprint(i), "live", "local", "account", "profile", "token", 1, state, 1, 0, 0}
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_allocations(`+mgTunerColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.values()...); err != nil {
					t.Fatal(err)
				}
			}
			turn := mgSourceTestTurn(t, w, MGConfigureTuner, mgTunerDefinitions())
			facts, err := readMGTunerCapacity(t.Context(), turn, "source", 2)
			want := int64(1)
			if channels[1] != "a" {
				want = 2
			}
			if err != nil || facts.held != want {
				t.Fatal(facts, err)
			}
		})
	}
}
func TestMGTunerNativeWholeRollback(t *testing.T) {
	w, turn, r := mgTunerFixture(t, MGAcquireAllocation)
	// Force the independent readback to miss after the INSERT has succeeded.
	// The command fails and the parent rolls the entire native transaction back.
	step := turn.steps[mgTunerRead]
	delete(turn.steps, mgTunerRead)
	if got, err := acquireMGTuner(t.Context(), turn, r); err == nil || got != (mgTunerAllocation{}) {
		t.Fatal(got, err)
	}
	turn.steps[mgTunerRead] = step
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_allocations`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestMGTunerNativeFullAndSharing(t *testing.T) {
	for _, mode := range []string{"full", "sharing", "expired-sharing", "preempt-sharing", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			w, turn, r := mgTunerFixture(t, MGAcquireAllocation)
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			physical := 1
			if mode == "unknown" {
				physical = 0
			}
			if _, err := w.db.ExecContext(t.Context(), `UPDATE live_source_identities SET settings_physical_count=?`, physical); err != nil {
				t.Fatal(err)
			}
			// The effective published value, rather than discovery metadata, is
			// the capacity input to the allocation body.
			if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET tuner_count=? WHERE id='source'`, physical); err != nil {
				t.Fatal(err)
			}
			held := r.allocation
			held.id = "held"
			held.resource = "held"
			held.channel = "other"
			if mode == "sharing" || mode == "expired-sharing" || mode == "preempt-sharing" {
				held.channel = r.allocation.channel
			}
			if mode == "expired-sharing" {
				held.lease = 1
			}
			if mode == "preempt-sharing" {
				held.state = "preempting"
			}
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_allocations(`+mgTunerColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, held.values()...); err != nil {
				t.Fatal(err)
			}
			turn = mgSourceTestTurn(t, w, MGConfigureTuner, append(mgSourceDefinitions(), mgTunerDefinitions()...))
			turn.transaction.descriptor.ID = MGAcquireAllocation
			r.allocation.created = w.now().UnixMilli()
			r.allocation.lease = r.allocation.created + 10000
			r.protectedUntil = r.allocation.created + 20000
			got, err := acquireMGTuner(t.Context(), turn, r)
			success := mode == "sharing" || mode == "unknown"
			if success {
				if err != nil || got != r.allocation {
					t.Fatal(got, err)
				}
			} else if !errors.Is(err, ErrConflict) {
				t.Fatal(got, err)
			}
		})
	}
}
