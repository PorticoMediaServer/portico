package writer

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func progressiveProtocolFixture(t *testing.T, count int) *inventoryProgressiveFixture {
	t.Helper()
	clock := time.Now().Add(time.Hour)
	return newInventoryProgressiveFixture(t, count, func() time.Time { return clock })
}

func commitProgressiveOriginal(t *testing.T, f *inventoryProgressiveFixture) InventoryResult {
	t.Helper()
	turn, err := f.Begin(t)
	if err != nil {
		t.Fatal(err)
	}
	result, err := applyInventoryAccept(t.Context(), turn, f.current)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != (ReceiptOutcome{}) {
		t.Fatal("component invented public outcome", result.Outcome)
	}
	if err := f.finish(turn); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestInventoryProgressiveFreshOriginalsRetainOwnersAndConnection(t *testing.T) {
	f := progressiveProtocolFixture(t, 3)
	owner := f.owner
	registry, authority, g, wal := owner.registry, owner.authority, owner.governor, owner.wal
	if len(registry.Manifest()) != len(inventoryCommandDefinitions()) {
		t.Fatal("incomplete captured registry")
	}
	// Existing test-only observer: one setup reopen begins observation; no
	// measured/control turn reopens or resets the database thereafter.
	if err := owner.executor.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(owner.path, owner.executor.config)
	if err != nil {
		t.Fatal(err)
	}
	trace := newN1Trace(time.Now(), false)
	owner.executor.openPool(connector, trace.decorate)
	// Materialize the reopened connection/WAL with an actual read before the
	// owner's pre-admission WAL sample; creation is not a reset event.
	if _, err := f.currentSnapshot(t); err != nil {
		t.Fatal(err)
	}
	var previous CommandIdentity
	var references []ReceiptReference
	for i := 0; i < 3; i++ {
		before := f.snapshot
		command, err := f.NextOriginal()
		if err != nil {
			t.Fatal(err)
		}
		if command.identity.Sequence != before.run.lastSequence+1 || command.request.run != before.run ||
			command.request.directory.expectedCursor != before.directory.expectedCursor || command.request.directory.pages != before.directory.pages {
			t.Fatal("original did not use current native snapshot", command)
		}
		if command.identity.OperationID == previous.OperationID {
			t.Fatal("replayed operation identity")
		}
		previous = command.identity
		references = append(references, ReceiptReference{command.identity, command.digest})
		out := commitProgressiveOriginal(t, f)
		if out.AcceptedRunRevision != before.run.revision+1 || len(out.Members) != 1 {
			t.Fatal(out)
		}
		if _, err := f.NextOriginal(); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("advanced before Resolve", err)
		}
		if err := f.AckAndSnapshot(t); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("ACK before Resolve", err)
		}
		if err := f.Resolve(t); err != nil {
			t.Fatal(err)
		}
		if _, err := f.NextOriginal(); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("advanced before ACK+snapshot", err)
		}
		if f.resolved.digest != out.Digest || f.resolved.result.AcceptedRunRevision != out.AcceptedRunRevision {
			t.Fatal("Resolve changed original result")
		}
		if err := f.AckAndSnapshot(t); err != nil {
			t.Fatal(err)
		}
		if f.snapshot.run.lastSequence != command.identity.Sequence || f.snapshot.run.acknowledgedSequence != command.identity.Sequence ||
			f.snapshot.discovered != before.discovered+1 || f.snapshot.processed != before.processed+1 {
			t.Fatal("current state did not progress natively", f.snapshot)
		}
		if owner != f.owner || registry != owner.registry || authority != owner.authority || g != owner.governor || wal != owner.wal {
			t.Fatal("owner composition replaced")
		}
		if trace.connections != 1 || !f.turn.transaction.policyVerified || authority.Usage() != ([4]governor.Amounts{}) || owner.executor.db.Stats().InUse != 0 {
			t.Fatal("connection/policy/custody continuity lost", trace.connections, authority.Usage())
		}
	}
	if _, err := f.NextOriginal(); !errors.Is(err, ErrCap) {
		t.Fatal("unbounded originals", err)
	}
	var objects, receipts, results, distinctOperations int
	if err := owner.executor.db.QueryRowContext(t.Context(), `SELECT
(SELECT count(*) FROM inventory_objects),(SELECT count(*) FROM scan_command_receipts),
(SELECT count(*) FROM scan_command_results),(SELECT count(DISTINCT operation_id) FROM scan_command_receipts)`).Scan(&objects, &receipts, &results, &distinctOperations); err != nil {
		t.Fatal(err)
	}
	if objects != 3 || receipts != 3 || results != 3 || distinctOperations != 3 {
		t.Fatal("reset/replay lost retained state", objects, receipts, results, distinctOperations)
	}
	// ACKed references follow W's retired floor even though this fixture keeps
	// all native receipt/result rows; they must not become replay throughput.
	for _, ref := range references {
		turn, err := owner.begin(t, f.current)
		if err != nil {
			t.Fatal(err)
		}
		q, _ := f.current.receiptRequest()
		q.Receipt = ref
		lookup, err := lookupInventoryAccept(t.Context(), turn, q)
		if err != nil || lookup.decision != receiptRetired || !reflect.DeepEqual(lookup.result, inventoryAcceptResult{}) {
			t.Fatal("ACKed original escaped retired floor", lookup, err)
		}
		if err := f.finish(turn); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInventoryProgressiveStopsAfterRollbackFenceUnknownOrUnjoined(t *testing.T) {
	for _, fault := range []string{"rollback", "fence", "unknown", "unjoined"} {
		t.Run(fault, func(t *testing.T) {
			f := progressiveProtocolFixture(t, 2)
			if _, err := f.NextOriginal(); err != nil {
				t.Fatal(err)
			}
			if fault == "rollback" {
				turn, err := f.Begin(t)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := turn.transaction.rollback(nil); err != nil {
					t.Fatal(err)
				}
			} else {
				commitProgressiveOriginal(t, f)
				switch fault {
				case "fence":
					f.owner.executor.fenced = true
				case "unknown":
					f.turn.transaction.result.state = commitUnknown
				case "unjoined":
					f.turn.transaction.retirementComplete = false
				}
			}
			if err := f.Resolve(t); err == nil {
				t.Fatal("unsafe attempt resolved")
			}
			stopped := f.stopped
			if stopped == nil {
				t.Fatal("failed attempt not sticky")
			}
			if _, err := f.NextOriginal(); !errors.Is(err, stopped) {
				t.Fatal("offered after failure", err)
			}
			if err := f.AckAndSnapshot(t); !errors.Is(err, stopped) {
				t.Fatal("ACK after failure", err)
			}
			if f.offered != 1 || f.owner.authority.Usage() != ([4]governor.Amounts{}) {
				t.Fatal("failed attempt changed census/custody")
			}
		})
	}
}

func TestInventoryProgressiveSnapshotFailureDoesNotAdvance(t *testing.T) {
	f := progressiveProtocolFixture(t, 2)
	if _, err := f.NextOriginal(); err != nil {
		t.Fatal(err)
	}
	commitProgressiveOriginal(t, f)
	if err := f.Resolve(t); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot
	// Current-source drift after Resolve: ACK remains historical, but the
	// fixture may not rebase a new ORIGINAL onto the changed source.
	if _, err := f.owner.executor.db.ExecContext(t.Context(), `UPDATE library_sources SET root='/replacement' WHERE id='fresh-source'`); err != nil {
		t.Fatal(err)
	}
	if err := f.AckAndSnapshot(t); err == nil {
		t.Fatal("changed source accepted as current snapshot")
	}
	if !reflect.DeepEqual(before, f.snapshot) || f.ready || f.stopped == nil {
		t.Fatal("partial snapshot published")
	}
	if _, err := f.NextOriginal(); err == nil {
		t.Fatal("next original escaped snapshot gap")
	}
	var ack int64
	if err := f.owner.executor.db.QueryRowContext(t.Context(), `SELECT acknowledged_command_sequence FROM inventory_runs WHERE job_id='fresh-run'`).Scan(&ack); err != nil || ack != f.current.identity.Sequence {
		t.Fatal("test failed before actual historical ACK", ack, err)
	}
}

func TestInventoryMemoryFactoredBeginBusyReturnsOnlyUnconsumedGrant(t *testing.T) {
	f := progressiveProtocolFixture(t, 1)
	input, err := f.NextOriginal()
	if err != nil {
		t.Fatal(err)
	}
	f.owner.executor.mu.Lock()
	_, err = f.owner.begin(t, input)
	f.owner.executor.mu.Unlock()
	if !errors.Is(err, ErrBusy) || f.owner.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("Busy leaked unconsumed grant", err, f.owner.authority.Usage())
	}
}
