package writer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// uoFlagsLateDrainDump formats every row of an independent oracle query so a
// rollback can be compared exactly. It reads plain SQL and never reuses the
// writer's own encode/decode.
func uoFlagsLateDrainDump(t *testing.T, w *executor, query string) string {
	t.Helper()
	rows, err := w.db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			if i > 0 {
				b.WriteByte('|')
			}
			fmt.Fprintf(&b, "%v", v)
		}
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

const (
	uoFlagsLateDrainItemsQuery = `SELECT profile_id,hex(item_public_id),item_id,library_id,revision,watchlisted,favorite,not_interested,rating,watched,play_count,played_ms FROM personal_items ORDER BY profile_id,item_public_id`
	uoFlagsLateDrainHeadsQuery = `SELECT profile_id,library_id,revision,observation_revision,title_path,selection_revision FROM viewer_revisions ORDER BY profile_id,library_id`
)

// TestUOFlagsLateDrainDuringPersistRefused is the UO-02b blocker control: a
// Fresh 600 that passes the active lookup check but whose root becomes draining
// while its own receipt insert or owner advance is running must still end
// ErrUORootGone. Both late writes happen after the lookup's active check, so
// only an active-only release at the persistence failure point can stop them.
// The complete prefix - personal flags row, viewer head, receipt and owner -
// must roll back byte-identically.
func TestUOFlagsLateDrainDuringPersistRefused(t *testing.T) {
	drains := map[string]string{
		// Same-turn technique as TestUORootReclaimDrainingTransitionFinishes:
		// the command's own write drains the root mid-effect.
		"receipt insert": `CREATE TRIGGER uo_flags_late_drain_receipt AFTER INSERT ON user_mutation_command_receipts BEGIN UPDATE user_mutation_roots SET state='draining' WHERE id=NEW.root_id; END`,
		"owner advance":  `CREATE TRIGGER uo_flags_late_drain_owner AFTER UPDATE ON user_mutation_command_owners WHEN NEW.revision>1 BEGIN UPDATE user_mutation_roots SET state='draining' WHERE id=NEW.root_id; END`,
	}
	for name, trigger := range drains {
		t.Run(name, func(t *testing.T) {
			w, _, root, identity, _, q := uoFlagsDispatchFixture(t)
			// A pre-existing sparse row and head make every rollback observable.
			wc008NativeExec(t, w, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,revision,watchlisted,favorite) VALUES(?,?,2111,7,1,0,0)`, q.profileKey, q.publicID[:])
			wc008NativeExec(t, w, `INSERT INTO viewer_revisions(profile_id,library_id,revision,observation_revision,selection_revision) VALUES(?,?,5,0,3)`, q.profileKey, q.libraryKey)
			q.expectedRevision = 1
			q.patch = uoPersonalFlagsPatch{watchlisted: uoFlagValue{set: true, value: true}}
			itemsBefore := uoFlagsLateDrainDump(t, w, uoFlagsLateDrainItemsQuery)
			headsBefore := uoFlagsLateDrainDump(t, w, uoFlagsLateDrainHeadsQuery)
			wc008NativeExec(t, w, trigger)
			ref := uoFlagsRef(t, root, 1, 9, q)
			turn := uoFlagsDispatchTurn(t, w)
			out, floor, err := runUOFlagsDispatch(t.Context(), turn, uoFlagsProof(t, turn, identity), uoRootFlagsInvocation{reference: ref, public: q.publicID, input: q, nowMS: 300})
			if !errors.Is(err, ErrUORootGone) || out != (uoRootFlagsResult{}) || floor != 0 {
				t.Fatalf("late drain published a body: out=%+v floor=%d err=%v", out, floor, err)
			}
			if got := uoFlagsLateDrainDump(t, w, uoFlagsLateDrainItemsQuery); got != itemsBefore {
				t.Fatalf("flags rows not byte-identical after rollback:\n got %q\nwant %q", got, itemsBefore)
			}
			if got := uoFlagsLateDrainDump(t, w, uoFlagsLateDrainHeadsQuery); got != headsBefore {
				t.Fatalf("viewer heads not byte-identical after rollback:\n got %q\nwant %q", got, headsBefore)
			}
			if last, ack, revision, rows, bytes := uoFlagsOwnerOracle(t, w, root); last != 0 || ack != 0 || revision != 1 || rows != 0 || bytes != 0 {
				t.Fatalf("owner not rolled back: last=%d ack=%d revision=%d rows=%d bytes=%d", last, ack, revision, rows, bytes)
			}
			if got := uoFlagsScalar(t, w, `SELECT (SELECT count(*) FROM user_mutation_command_receipts)+(SELECT count(*) FROM api_events)`); got != 0 {
				t.Fatalf("receipt/event prefix retained: %d", got)
			}
			if got := uoFlagsScalar(t, w, `SELECT state='active' FROM user_mutation_roots WHERE id=?`, root); got != 1 {
				t.Fatal("rolled-back drain changed the root state")
			}
		})
	}
}

// TestUOFlagsReleaseStaysActiveOnly is the mutation guard for the shared
// helper: uoRootReclaimRelease must keep UO-01's active-only Fresh rule, while
// the 654-only maintenance release (and only that helper, on only that
// descriptor) admits a draining root. A Fresh 600 reaching the shared helper
// after a late drain must still fail ErrUORootGone.
func TestUOFlagsReleaseStaysActiveOnly(t *testing.T) {
	t.Run("shared release refuses fresh on a draining 600", func(t *testing.T) {
		w, _, root, identity, _, _ := uoFlagsDispatchFixture(t)
		turn := uoFlagsDispatchTurn(t, w)
		proof := uoFlagsProof(t, turn, identity)
		drainTurnRoot(t, turn, root)
		if err := uoRootReclaimRelease(t.Context(), turn, proof, root, receiptFresh); !errors.Is(err, ErrUORootGone) {
			t.Fatalf("shared release admitted draining Fresh: %v", err)
		}
	})

	t.Run("flags gate refuses fresh on a draining 600", func(t *testing.T) {
		w, _, root, identity, _, _ := uoFlagsDispatchFixture(t)
		turn := uoFlagsDispatchTurn(t, w)
		proof := uoFlagsProof(t, turn, identity)
		drainTurnRoot(t, turn, root)
		if err := uoFlagsRelease(t.Context(), turn, proof, root, receiptFresh); !errors.Is(err, ErrUORootGone) {
			t.Fatalf("flags release admitted draining Fresh: %v", err)
		}
	})

	t.Run("replay may still end draining", func(t *testing.T) {
		w, _, root, identity, _, _ := uoFlagsDispatchFixture(t)
		turn := uoFlagsDispatchTurn(t, w)
		proof := uoFlagsProof(t, turn, identity)
		drainTurnRoot(t, turn, root)
		if err := uoRootReclaimRelease(t.Context(), turn, proof, root, receiptReplay); err != nil {
			t.Fatalf("shared release refused a draining replay: %v", err)
		}
	})

	t.Run("maintenance release refuses a non-654 turn", func(t *testing.T) {
		w, _, root, identity, _, _ := uoFlagsDispatchFixture(t)
		turn := uoFlagsDispatchTurn(t, w)
		proof := uoFlagsProof(t, turn, identity)
		if err := uoRootReclaimMaintenanceRelease(t.Context(), turn, proof, root, receiptFresh); !errors.Is(err, ErrInvalid) {
			t.Fatalf("maintenance release reachable from 600: %v", err)
		}
	})

	t.Run("maintenance release admits fresh on a draining 654", func(t *testing.T) {
		w, _, root, identity, _ := uoRootReclaimFixture(t)
		turn := uoRootReclaimTurn(t, w)
		proof := uoRootReclaimProof(t, turn, identity)
		drainTurnRoot(t, turn, root)
		if err := uoRootReclaimMaintenanceRelease(t.Context(), turn, proof, root, receiptFresh); err != nil {
			t.Fatalf("654 maintenance release refused draining Fresh: %v", err)
		}
		if err := uoRootReclaimRelease(t.Context(), turn, proof, root, receiptFresh); !errors.Is(err, ErrUORootGone) {
			t.Fatalf("shared release admitted draining Fresh on 654: %v", err)
		}
	})
}

// drainTurnRoot flips the root inside the open turn's own connection; the
// fixture pool is single-connection, so a pool-level write would deadlock.
func drainTurnRoot(t *testing.T, turn *commandTurn, root string) {
	t.Helper()
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET state='draining' WHERE id=?`, root); err != nil {
		t.Fatal(err)
	}
}
