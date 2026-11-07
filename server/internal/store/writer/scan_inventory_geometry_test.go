package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestN1UsablePageFloorDoesNotAssumeZeroReservedBytes(t *testing.T) {
	for _, size := range []int32{512, 4096, 65536} {
		got, err := inventoryUsablePageFloor(size)
		if err != nil || got != uint64(size)-255 {
			t.Fatal(size, got, err)
		}
	}
	for _, size := range []int32{0, 511, 1000, 65537} {
		got, err := inventoryUsablePageFloor(size)
		var refusal *Refusal
		if got != 0 || !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified {
			t.Fatal(size, got, err)
		}
	}
}

func TestN1GeometryCannotQualifyAnotherPurposeOrSubset(t *testing.T) {
	for _, purpose := range []CommandID{EffectiveStage, SearchImageStage, InventoryCreate, InventoryClaim, ProgressAccept, SecurityAccept, ProgressReceiptReclaim, InventoryAccept} {
		plan, err := freezeCommandPlan(&executor{}, effectivePlan{inventoryPageSize: 4096}, nil, purpose)
		if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(plan, effectivePlan{}) {
			t.Fatal("N1 geometry escaped complete-purpose composition", purpose, plan, err)
		}
	}
}

func TestN1FreshRejectsUnobservedConnectionGeometry(t *testing.T) {
	d, preparation, command, _ := n1ComposedDriverPrepared(t)
	if d.writer.effective.inventoryDriverPlan.inventoryPageSize == 0 {
		t.Fatal("no frozen geometry")
	}
	// A positive earlier profile/config sample cannot substitute for the
	// evidence Assert actually returned on this command's acquired connection.
	d.opening.executor.config.WriterPageSize = 0
	got, err := preparation.Accept(t.Context(), command)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(got, InventoryResult{}) {
		t.Fatal("profile accepted an unobserved default", got, err)
	}
	var assets int
	if err := d.opening.executor.db.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_assets").Scan(&assets); err != nil || assets != 0 {
		t.Fatal("unobserved geometry reached effects", assets, err)
	}
}

type pageBeginCounter struct {
	writerDriverConn
	begins *int
}

func (c *pageBeginCounter) ExecContext(ctx context.Context, sql string, args []driver.NamedValue) (driver.Result, error) {
	if sql == "BEGIN IMMEDIATE" {
		*c.begins++
	}
	return c.writerDriverConn.ExecContext(ctx, sql, args)
}

func TestWriterPageMismatchRefusesBeforeBegin(t *testing.T) {
	begins := 0
	w, _, _ := testExecutor(t, func(c driver.Conn) driver.Conn { return &pageBeginCounter{c.(writerDriverConn), &begins} })
	var actual int32
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA main.page_size").Scan(&actual); err != nil {
		t.Fatal(err)
	}
	w.config.WriterPageSize = actual / 2
	if actual == 512 {
		w.config.WriterPageSize = 1024
	}
	g, a, wal, request := governedFixture(t, w.now)
	decision := g.Try(request)
	if decision.Grant == nil {
		t.Fatal(decision)
	}
	custody, err := bindGovernedTurn(InventoryAccept, testRecipe().limit, g, a, wal, decision.Grant, request)
	if err != nil {
		t.Fatal(err)
	}
	before := begins
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), custody)
	if tx != nil || !errors.Is(err, sqliteconn.ErrPolicy) || begins != before || custody.state.Load() != 2 {
		t.Fatal("page mismatch began native work or lost retirement", tx, err, begins-before, custody.state.Load())
	}
}
