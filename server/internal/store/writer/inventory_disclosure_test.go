package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

type inventoryScopeCancelConn struct {
	writerDriverConn
	point  string
	cancel func()
}

func (c *inventoryScopeCancelConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err == nil && strings.Contains(q, "FROM catalog_libraries") {
		if c.point == "query" {
			c.cancel()
		}
		return &inventoryScopeCancelRows{Rows: rows, point: c.point, cancel: c.cancel}, nil
	}
	return rows, err
}

type inventoryScopeCancelRows struct {
	driver.Rows
	point  string
	cancel func()
	once   sync.Once
}

func (r *inventoryScopeCancelRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if r.point == "next" || r.point == "eof" && errors.Is(err, io.EOF) {
		r.once.Do(r.cancel)
	}
	return err
}

func (r *inventoryScopeCancelRows) Close() error {
	err := r.Rows.Close()
	if r.point == "close" {
		r.once.Do(r.cancel)
	}
	return err
}

func TestInventoryNativeDisclosureLateCancellation(t *testing.T) {
	w := inventoryDisclosureFixture(t)
	registry := cachedNativeInventoryFixtureRegistry(t, w, inventoryDisclosureDefinitions(), "inventory_disclosure_test.go", "scan_receipts_test.go")
	var sequence int64
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	for _, point := range []string{"query", "next", "eof", "close"} {
		for _, lifetime := range []string{"caller", "turn", "same"} {
			t.Run(point+"/"+lifetime, func(t *testing.T) {
				if err := w.db.Close(); err != nil {
					t.Fatal(err)
				}
				connector, err := sqliteconn.Connector(path, w.config)
				if err != nil {
					t.Fatal(err)
				}
				caller, cancel := context.WithCancel(t.Context())
				defer cancel()
				cancelRead := cancel
				w.openPool(connector, func(conn driver.Conn) driver.Conn {
					return &inventoryScopeCancelConn{writerDriverConn: conn.(writerDriverConn), point: point, cancel: func() { cancelRead() }}
				})
				turn, tx := inventoryDisclosureTurn(t, w, registry)
				var callCtx context.Context = caller
				if lifetime == "turn" || lifetime == "same" {
					cancelRead = tx.cancel
				}
				if lifetime == "same" {
					callCtx = tx.ctx
				}
				err = checkInventoryNativeScope(callCtx, turn, ScanReceiptRequest{RunID: "scan-hit", LibraryID: 7, LibraryKey: "scan-library"})
				if !errors.Is(err, context.Canceled) {
					t.Fatal("late cancellation became successful scope proof", err)
				}
				tx.rollback(err)
				if w.db.Stats().InUse != 0 {
					t.Fatal("cancelled scope check stranded custody")
				}
			})
		}
	}
}

func TestInventoryNativeDisclosureRetirementDoesNotHideHistory(t *testing.T) {
	w := inventoryDisclosureFixture(t)
	registry := cachedNativeInventoryFixtureRegistry(t, w, inventoryDisclosureDefinitions(), "inventory_disclosure_test.go", "scan_receipts_test.go")
	if _, err := w.db.ExecContext(t.Context(), "UPDATE catalog_libraries SET retired=1 WHERE id=7"); err != nil {
		t.Fatal(err)
	}
	turn, _ := inventoryDisclosureTurn(t, w, registry)
	if err := checkInventoryNativeScope(t.Context(), turn, ScanReceiptRequest{RunID: "scan-hit", LibraryID: 7, LibraryKey: "scan-library"}); err != nil {
		t.Fatal("live retirement hid retained historical scope", err)
	}
	// This relationship proof confers no fresh permission. The composed
	// dispatcher must refuse retired-library fresh work AFTER historical lookup.
}

func inventoryDisclosureFixture(t *testing.T) *executor {
	t.Helper()
	w, _, _ := testExecutor(t)
	seedScanReceiptCohorts(t, w) // unchanged immutable SC source/receipt seed
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_libraries(id,library_id,root) VALUES(8,'other','/other')`); err != nil {
		t.Fatal(err)
	}
	return w
}

func inventoryDisclosureTurn(t *testing.T, w *executor, registry *stmt.Registry) (*commandTurn, *transaction) {
	t.Helper()
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.rollback(nil) })
	var steps []statementStep
	for _, d := range registry.Manifest() {
		steps = append(steps, statementStep{d.ID, Work{2, 100, 1, 0, time.Nanosecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn, tx
}

// Deliberately malformed projection/row-count SQL is fault-only evidence.
func inventoryDisclosureFaultTurn(t *testing.T, w *executor, defs []stmt.Definition) (*commandTurn, *transaction) {
	t.Helper()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return inventoryDisclosureTurn(t, w, registry)
}

func TestInventoryNativeDisclosureScope(t *testing.T) {
	w := inventoryDisclosureFixture(t)
	registry := cachedNativeInventoryFixtureRegistry(t, w, inventoryDisclosureDefinitions(), "inventory_disclosure_test.go", "scan_receipts_test.go")
	base := ScanReceiptRequest{RunID: "scan-hit", LibraryID: 7, LibraryKey: "scan-library"}
	for _, c := range []struct {
		name string
		edit func(*ScanReceiptRequest)
		want error
	}{
		{"current_scope", nil, nil},
		{"stale_source_lease", func(r *ScanReceiptRequest) { r.RunID = "scan-stale" }, nil},
		{"missing_job", func(r *ScanReceiptRequest) { r.RunID = "absent" }, ErrDisclosureDenied},
		{"wrong_job_library", func(r *ScanReceiptRequest) { r.LibraryKey = "other" }, ErrDisclosureDenied},
		{"wrong_catalog_library", func(r *ScanReceiptRequest) { r.LibraryID = 8 }, ErrDisclosureDenied},
		{"missing_catalog_library", func(r *ScanReceiptRequest) { r.LibraryID = 9 }, ErrDisclosureDenied},
		{"empty_run", func(r *ScanReceiptRequest) { r.RunID = "" }, ErrDisclosureDenied},
		{"empty_key", func(r *ScanReceiptRequest) { r.LibraryKey = "" }, ErrDisclosureDenied},
		{"invalid_utf8", func(r *ScanReceiptRequest) { r.LibraryKey = "\xff" }, ErrDisclosureDenied},
	} {
		t.Run(c.name, func(t *testing.T) {
			turn, tx := inventoryDisclosureTurn(t, w, registry)
			r := base
			if c.edit != nil {
				c.edit(&r)
			}
			err := checkInventoryNativeScope(t.Context(), turn, r)
			if !errors.Is(err, c.want) {
				t.Fatal(c.name, err)
			}
			if _, err := tx.rollback(err); !errors.Is(err, c.want) {
				t.Fatal("lost native refusal", err)
			}
		})
	}
	for _, sql := range []string{
		`UPDATE jobs SET status='failed' WHERE id='scan-hit'`,
		`UPDATE inventory_runs SET job_revision=9223372036854775807,job_lease_expires_ms=0 WHERE job_id='scan-hit'`,
		`UPDATE library_sources SET generation=99,incarnation='changed-root' WHERE id='scan-source-hit'`,
	} {
		if _, err := w.db.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	turn, _ := inventoryDisclosureTurn(t, w, registry)
	if err := checkInventoryNativeScope(t.Context(), turn, base); err != nil {
		t.Fatal("fresh eligibility hid historical scope", err)
	}
}

func TestInventoryNativeDisclosureRejectsFaultedRowsAndCancel(t *testing.T) {
	w := inventoryDisclosureFixture(t)
	registry := cachedNativeInventoryFixtureRegistry(t, w, inventoryDisclosureDefinitions(), "inventory_disclosure_test.go", "scan_receipts_test.go")
	r := ScanReceiptRequest{RunID: "scan-hit", LibraryID: 7, LibraryKey: "scan-library"}
	for _, c := range []struct {
		name string
		id   stmt.ID
		sql  string
		want error
	}{
		{"wrong_returned_job", inventoryDisclosureJob, `SELECT 'wrong',library_id FROM jobs WHERE id=? AND library_id=?`, ErrDisclosureDenied},
		{"wrong_returned_key", inventoryDisclosureJob, `SELECT id,'wrong' FROM jobs WHERE id=? AND library_id=?`, ErrDisclosureDenied},
		{"wrong_returned_library", inventoryDisclosureLibrary, `SELECT id+1,library_id FROM catalog_libraries WHERE id=? AND library_id=?`, ErrDisclosureDenied},
		{"extra_row", inventoryDisclosureJob, `SELECT id,library_id FROM jobs WHERE id=? AND library_id=? UNION ALL SELECT 'extra','scan-library'`, ErrResolveRequired},
	} {
		t.Run(c.name, func(t *testing.T) {
			definitions := inventoryDisclosureDefinitions()
			for i := range definitions {
				if definitions[i].ID == c.id {
					// A test-only driver/projection fault, never a composed recipe.
					definitions[i].SQL = c.sql
				}
			}
			turn, tx := inventoryDisclosureFaultTurn(t, w, definitions)
			err := checkInventoryNativeScope(t.Context(), turn, r)
			if !errors.Is(err, c.want) {
				t.Fatal(err)
			}
			tx.rollback(err)
		})
	}
	turn, tx := inventoryDisclosureTurn(t, w, registry)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := checkInventoryNativeScope(ctx, turn, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	tx.rollback(nil)
	if err := checkInventoryNativeScope(nil, turn, r); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestInventoryNativeDisclosureRequiresClosedReadSteps(t *testing.T) {
	w := inventoryDisclosureFixture(t)
	registry := cachedNativeInventoryFixtureRegistry(t, w, inventoryDisclosureDefinitions(), "inventory_disclosure_test.go", "scan_receipts_test.go")
	r := ScanReceiptRequest{RunID: "scan-hit", LibraryID: 7, LibraryKey: "scan-library"}
	for _, id := range []stmt.ID{inventoryDisclosureJob, inventoryDisclosureLibrary} {
		for _, missing := range []bool{false, true} {
			turn, tx := inventoryDisclosureTurn(t, w, registry)
			if missing {
				delete(turn.steps, id)
			} else {
				step := turn.steps[id]
				step.kind = stmt.Write
				turn.steps[id] = step
			}
			if err := checkInventoryNativeScope(t.Context(), turn, r); !errors.Is(err, ErrInvalid) || len(turn.rows) != 0 {
				t.Fatal("unregistered/non-read scope step entered SQL", id, missing, err)
			}
			tx.rollback(nil)
		}
	}
}
