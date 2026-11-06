package writer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func inventoryDisclosureFixture(t *testing.T) *executor {
	t.Helper()
	w, _, _ := testExecutor(t)
	seedScanReceiptCohorts(t, w) // unchanged immutable SC source/receipt seed
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_libraries(id,library_id,root) VALUES(8,'other','/other')`); err != nil {
		t.Fatal(err)
	}
	return w
}

func inventoryDisclosureTurn(t *testing.T, w *executor, definitions []stmt.Definition) (*commandTurn, *transaction) {
	t.Helper()
	r, err := stmt.New(definitions)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.rollback(nil) })
	var steps []statementStep
	for _, d := range definitions {
		steps = append(steps, statementStep{d.ID, Work{2, 100, 1, 0, time.Nanosecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn, tx
}

func TestInventoryNativeDisclosureScope(t *testing.T) {
	w := inventoryDisclosureFixture(t)
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
			turn, tx := inventoryDisclosureTurn(t, w, inventoryDisclosureDefinitions())
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
	turn, _ := inventoryDisclosureTurn(t, w, inventoryDisclosureDefinitions())
	if err := checkInventoryNativeScope(t.Context(), turn, base); err != nil {
		t.Fatal("fresh eligibility hid historical scope", err)
	}
}

func TestInventoryNativeDisclosureRejectsFaultedRowsAndCancel(t *testing.T) {
	w := inventoryDisclosureFixture(t)
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
			turn, tx := inventoryDisclosureTurn(t, w, definitions)
			err := checkInventoryNativeScope(t.Context(), turn, r)
			if !errors.Is(err, c.want) {
				t.Fatal(err)
			}
			tx.rollback(err)
		})
	}
	turn, tx := inventoryDisclosureTurn(t, w, inventoryDisclosureDefinitions())
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
	r := ScanReceiptRequest{RunID: "scan-hit", LibraryID: 7, LibraryKey: "scan-library"}
	for _, id := range []stmt.ID{inventoryDisclosureJob, inventoryDisclosureLibrary} {
		for _, missing := range []bool{false, true} {
			turn, tx := inventoryDisclosureTurn(t, w, inventoryDisclosureDefinitions())
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

func TestInventoryNativeDisclosureStatementPlans(t *testing.T) {
	w := inventoryDisclosureFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, w.config); err != nil {
		t.Fatal(err)
	}
	for _, d := range inventoryDisclosureDefinitions() {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 || !d.Plan.Hot || len(d.Plan.Waivers) != 0 {
			t.Fatal(d.ID, issues)
		}
		for _, c := range d.Cohorts {
			rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, c.Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			eqp := plan.Rows{Columns: []string{"id", "parent", "notused", "detail"}}
			for rows.Next() {
				var id, parent, aux int64
				var detail string
				if err := rows.Scan(&id, &parent, &aux, &detail); err != nil {
					t.Fatal(err)
				}
				t.Logf("%s/%s: %s", d.ID, c.Name, detail)
				eqp.Values = append(eqp.Values, []any{id, parent, aux, detail})
				count++
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil || count != 1 {
				t.Fatal(d.ID, c.Name, err, count)
			}
			if err := plan.Check(d, eqp); err != nil {
				t.Fatal(err)
			}
		}
	}
}
