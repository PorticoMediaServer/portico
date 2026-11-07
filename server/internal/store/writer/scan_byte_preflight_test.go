package writer

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestScanActualByteForecastArithmeticAndNullEcho(t *testing.T) {
	a := scanByteVector{exists: true, count: 3, total: 4}
	a.cells[0] = sql.NullInt64{Int64: 4, Valid: true}
	a.cells[1] = sql.NullInt64{Int64: 0, Valid: true}
	if !a.matches("😀", "", nil) || a.matches("😀", nil, nil) || a.matches("😀", "", "") || a.matches("x", "", nil) {
		t.Fatal("native byte counts or NULL/empty distinction lost")
	}
	if n, err := scanActualByteForecast(a, scanByteVector{exists: true, total: 7}); err != nil || n != scanByteFixedWorkspace+32*11 {
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
	w, requests, registry := inventoryLookupFixture(t)
	var sequence int64
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	var target string
	for _, d := range inventoryLookupDefinitions() {
		if d.ID == scanReceiptLookup {
			target = d.SQL
		}
	}
	if target == "" {
		t.Fatal("missing captured receipt projection")
	}
	// Preserve the exact SQL and complete native Capture receipt. Inject drift
	// only when the driver returns the saved source revision to the reader.
	copies := 0
	w.openPool(connector, func(conn driver.Conn) driver.Conn {
		return &scanByteProjectionConn{writerDriverConn: conn.(writerDriverConn), target: target, copies: &copies}
	})
	turn, _ := inventoryLookupTurn(t, w, registry)
	// An echo bypass must fail here with ErrQuantum, before a later result
	// digest mismatch could accidentally make the control pass.
	step := turn.steps[scanReceiptResults]
	step.work.Rows = math.MaxUint64
	turn.steps[scanReceiptResults] = step
	got, err := lookupInventoryAccept(t.Context(), turn, requests["hit"])
	if copies != 1 || !errors.Is(err, ErrResolveRequired) || errors.Is(err, ErrQuantum) || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
		t.Fatal("same-snapshot byte echo drift escaped", got, err)
	}
}

type scanByteProjectionConn struct {
	writerDriverConn
	target string
	copies *int
}

func (c *scanByteProjectionConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, query, args)
	if err == nil && query == c.target {
		for i, column := range rows.Columns() {
			if column == "source_revision" {
				return &scanByteProjectionRows{Rows: rows, column: i, copies: c.copies}, nil
			}
		}
		return nil, errors.Join(errors.New("missing source_revision projection"), rows.Close())
	}
	return rows, err
}

type scanByteProjectionRows struct {
	driver.Rows
	column int
	copies *int
}

func (r *scanByteProjectionRows) Next(dest []driver.Value) error {
	if err := r.Rows.Next(dest); err != nil {
		return err
	}
	value, ok := dest[r.column].(string)
	if !ok {
		return errors.New("source_revision fixture must be TEXT")
	}
	dest[r.column] = value + "x"
	*r.copies++
	return nil
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
