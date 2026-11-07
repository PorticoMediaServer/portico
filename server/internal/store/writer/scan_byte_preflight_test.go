package writer

import (
	"database/sql"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func TestScanActualByteForecastArithmeticAndNullEcho(t *testing.T) {
	a := scanByteVector{exists: true, count: 3, total: 6}
	a.cells[0] = sql.NullInt64{Int64: 4, Valid: true}
	a.cells[1] = sql.NullInt64{Int64: 0, Valid: true}
	if !a.matches("😀", "", nil) || a.matches("😀", nil, nil) || a.matches("😀", "", "") || a.matches("x", "", nil) {
		t.Fatal("native byte counts or NULL/empty distinction lost")
	}
	if n, err := scanActualByteForecast(a, scanByteVector{exists: true, total: 7}); err != nil || n != scanByteFixedWorkspace+32*13 {
		t.Fatal(n, err)
	}
	for _, vectors := range [][]scanByteVector{
		{{}},
		{{exists: true, total: math.MaxUint64}, {exists: true, total: 1}},
		{{exists: true, total: (math.MaxUint64-scanByteFixedWorkspace)/32 + 1}},
	} {
		if n, err := scanActualByteForecast(vectors...); n != 0 || err == nil {
			t.Fatal("invalid byte proof returned a usable prefix", n, err)
		}
	}
}

func TestInventoryHistoryByteCapacityBeforePayloadCopy(t *testing.T) {
	w, requests, registry := inventoryLookupFixture(t)
	q := requests["hit"]
	// Legal-size saved TEXT, larger than this component's existing 4MiB copy
	// partition once all retained/encoding copies are accounted for. No cap is
	// reduced and no command partition is enlarged to make the case pass.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE scan_command_receipts SET source_revision=? WHERE run_id=?`, strings.Repeat("x", 128<<10), q.RunID); err != nil {
		t.Fatal(err)
	}
	turn, _ := inventoryLookupTurn(t, w, registry)
	// A payload copy would now fail with ErrQuantum instead of Capacity.
	step := turn.steps[scanReceiptLookup]
	step.work.Rows = math.MaxUint64
	turn.steps[scanReceiptLookup] = step
	got, err := lookupInventoryAccept(t.Context(), turn, q)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
		t.Fatal("unfunded payload was copied or released", got, err)
	}
}

func TestInventoryHistoryFloorDoesNotCopyOrPreflightRetiredPayload(t *testing.T) {
	w, requests, registry := inventoryLookupFixture(t)
	q := requests["floor"]
	turn, _ := inventoryLookupTurn(t, w, registry)
	for _, id := range []stmt.ID{scanReceiptByteLengths, scanMemberKeys, scanMemberByteLengths, scanReceiptLookup, scanReceiptResults} {
		step := turn.steps[id]
		step.work.Rows = math.MaxUint64
		turn.steps[id] = step
	}
	got, err := lookupInventoryAccept(t.Context(), turn, q)
	if err != nil || got.decision != receiptRetired || !reflect.DeepEqual(got.result, inventoryAcceptResult{}) {
		t.Fatal("floor depended on drained payload", got, err)
	}
}

func TestInventoryHistoryByteEchoRejectsChangedProjection(t *testing.T) {
	w, requests, _ := inventoryLookupFixture(t)
	// Deliberately altered projection only: the ordinary native fixture and
	// cancellation controls retain real complete-set Capture receipts.
	defs := inventoryLookupDefinitions()
	for i := range defs {
		if defs[i].ID == scanReceiptLookup {
			defs[i].SQL = strings.Replace(defs[i].SQL, "source_revision", "source_revision||'x'", 1)
		}
	}
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	turn, _ := inventoryLookupTurn(t, w, registry)
	got, err := lookupInventoryAccept(t.Context(), turn, requests["hit"])
	if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
		t.Fatal("same-snapshot byte echo drift escaped", got, err)
	}
}

func TestInventoryHistoryExtraMemberRefusesBeforeObjectCopy(t *testing.T) {
	w, requests, registry := inventoryLookupFixture(t)
	q := requests["hit"]
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO scan_command_results(run_id,command_incarnation,command_sequence,input_ordinal,child_kind,child_ordinal,object_id,library_id,subject_id,asset_id,accepted_asset_revision) SELECT run_id,command_incarnation,command_sequence,1,child_kind,child_ordinal,object_id,library_id,subject_id,asset_id,accepted_asset_revision FROM scan_command_results WHERE run_id=?`, q.RunID); err != nil {
		t.Fatal(err)
	}
	turn, _ := inventoryLookupTurn(t, w, registry)
	step := turn.steps[scanReceiptResults]
	step.work.Rows = math.MaxUint64
	turn.steps[scanReceiptResults] = step
	got, err := lookupInventoryAccept(t.Context(), turn, q)
	if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
		t.Fatal("extra ordinal copied an un-preflighted object", got, err)
	}
}

func TestScanSnapshotByteCapacityBeforeNativeCopy(t *testing.T) {
	w, registry, input := scanLifecycleExecutionFixture(t)
	budget := commandMemoryBudget{4 << 20, 4 << 20}
	turn := scanLifecycleExecutionTurn(t, w, registry, InventoryCreate, budget)
	saved, err := applyInventoryCreate(t.Context(), turn, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), `UPDATE jobs SET error=? WHERE id=?`, strings.Repeat("e", 128<<10), saved.Original.RunID); err != nil {
		t.Fatal(err)
	}
	turn = scanLifecycleExecutionTurn(t, w, registry, InventoryClaim, budget)
	for _, id := range []stmt.ID{scanLifecycleRun, scanLifecycleJob, scanLifecycleRoot} {
		step := turn.steps[id]
		step.work.Rows = math.MaxUint64
		turn.steps[id] = step
	}
	got, err := readCheckedScanRunSnapshot(t.Context(), turn, saved.Original.RunID)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(got, scanRunSnapshot{}) {
		t.Fatal("native snapshot copied before actual-byte admission", got, err)
	}
}
