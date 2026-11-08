package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type inventoryCancelReadConn struct {
	writerDriverConn
	target, point string
	cancel        func()
}

func (c *inventoryCancelReadConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err == nil && strings.Contains(q, c.target) {
		return &inventoryCancelReadRows{Rows: rows, point: c.point, cancel: c.cancel}, nil
	}
	return rows, err
}

type inventoryCancelReadRows struct {
	driver.Rows
	point  string
	cancel func()
	once   sync.Once
}

func (r *inventoryCancelReadRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if r.point == "next" || r.point == "eof" && errors.Is(err, io.EOF) {
		r.once.Do(r.cancel)
	}
	return err
}

func (r *inventoryCancelReadRows) Close() error {
	err := r.Rows.Close()
	if r.point == "close" {
		r.once.Do(r.cancel)
	}
	return err
}

func TestInventoryLookupLateCancellationReturnsZero(t *testing.T) {
	w, requests, registry := inventoryLookupFixture(t)
	var sequence int64
	var name, path string
	if err := w.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	for _, population := range []string{"missing", "floor", "hit"} {
		for _, point := range []string{"next", "eof", "close"} {
			// A present header's single-row Scan closes without requesting
			// native EOF. Test only cancellation boundaries actually reached.
			if population == "floor" && point == "eof" {
				continue
			}
			for _, lifetime := range []string{"caller", "turn", "same"} {
				t.Run(population+"/"+point+"/"+lifetime, func(t *testing.T) {
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
					target := "FROM scan_command_receipts"
					if population == "hit" {
						target = "FROM scan_command_results"
					}
					w.openPool(connector, func(conn driver.Conn) driver.Conn {
						return &inventoryCancelReadConn{writerDriverConn: conn.(writerDriverConn), target: target, point: point, cancel: func() { cancelRead() }}
					})
					turn, tx := inventoryLookupTurn(t, w, registry)
					var callCtx context.Context = caller
					if lifetime == "turn" || lifetime == "same" {
						cancelRead = tx.cancel
					}
					if lifetime == "same" {
						callCtx = tx.ctx
					}
					got, err := lookupInventoryAccept(callCtx, turn, requests[population])
					if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
						t.Fatal("late cancellation returned fresh/history/floor proof", got, err)
					}
					tx.rollback(err)
					if w.db.Stats().InUse != 0 {
						t.Fatal("cancelled lookup stranded a connection")
					}
				})
			}
		}
	}
}

func inventoryLookupFixture(t *testing.T) (*executor, map[string]ScanReceiptRequest, *stmt.Registry) {
	t.Helper()
	w, _, _ := testExecutor(t)
	populations := seedScanReceiptCohorts(t, w)
	requests := make(map[string]ScanReceiptRequest)
	for _, p := range populations {
		r := ScanReceiptRequest{RunID: "scan-" + p.name, LibraryID: 7, LibraryKey: "scan-library"}
		r.Receipt.Identity.Sequence = 1
		copy(r.Receipt.Identity.OperationID[:], p.operation)
		requests[p.name] = r
		if p.last == 0 || p.rows != p.count || p.first != 0 {
			continue
		}
		// Replace SQL-plan synthetic result digests with the closed codec's
		// complete native fixture projection. These are receipt-reader fixtures,
		// not accepted fresh input or proof of source/lease/public authority.
		v := inventoryAcceptResult{RunID: r.RunID, AcceptedRunRevision: 2, AppliedMS: 100,
			SourceID: "scan-source-" + p.name, SourceIncarnation: "source-incarnation", SourceRevision: "opaque:007", NextCursor: "native-next",
			ResultCount: 1, Members: []inventoryResultMember{{0, 0, 0, "object-" + p.name, 7, 31, 41, 1}}}
		var q RequestDigest
		copy(q[:], p.digest)
		d, err := inventoryAcceptResultDigest(r.Receipt.Identity, q, v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.db.ExecContext(t.Context(), `UPDATE scan_command_receipts SET result_digest=? WHERE run_id=?`, d[:], r.RunID); err != nil {
			t.Fatal(err)
		}
	}
	registry := cachedNativeInventoryFixtureRegistry(t, w, inventoryLookupDefinitions(), "inventory_resolution_test.go", "scan_receipts_test.go")
	return w, requests, registry
}

func inventoryLookupDefinitions() []stmt.Definition {
	var definitions []stmt.Definition
	for _, d := range scanReceiptDefinitions() {
		if d.ID == scanReceiptOwner || d.ID == scanReceiptLookup || d.ID == scanReceiptResults {
			definitions = append(definitions, d)
		}
	}
	return append(definitions, inventoryHistoryByteDefinitions()...)
}

func inventoryLookupTurn(t *testing.T, w *executor, registry *stmt.Registry) (*commandTurn, *transaction) {
	t.Helper()
	request, identity := inventoryRequestFixture()
	request.scope = inventoryScope{runID: "scan-hit", libraryID: 7, libraryKey: "scan-library"}
	input, err := newInventoryAcceptCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	turn, _ := inventoryMemoryFixtureTurn(t, w, registry, input, inventoryMemoryBudget{4 << 20, 4 << 20})
	return turn, turn.transaction
}

func TestInventoryLookupOriginalHistoryAndFloors(t *testing.T) {
	w, requests, registry := inventoryLookupFixture(t)
	for name, want := range map[string]receiptDecision{"hit": receiptReplay, "replay": receiptReplay, "stale": receiptReplay,
		"missing": receiptFresh, "new": receiptFresh, "floor": receiptRetired, "drained": receiptRetired, "remaining": receiptRetired} {
		t.Run(name, func(t *testing.T) {
			turn, tx := inventoryLookupTurn(t, w, registry)
			if want != receiptReplay {
				// Any attempt to decode the cohort would exceed this frozen step.
				step := turn.steps[scanReceiptResults]
				step.work.Rows = math.MaxUint64
				turn.steps[scanReceiptResults] = step
			}
			got, err := lookupInventoryAccept(t.Context(), turn, requests[name])
			if err != nil || got.decision != want {
				t.Fatal(name, got.decision, want, err)
			}
			if want == receiptReplay {
				if got.result.AcceptedRunRevision != 2 || got.result.AppliedMS != 100 || got.result.SourceRevision != "opaque:007" ||
					got.result.SourceIncarnation != "source-incarnation" || got.result.NextCursor != "native-next" || len(got.result.Members) != 1 ||
					got.result.Members[0] != (inventoryResultMember{0, 0, 0, "object-" + name, 7, 31, 41, 1}) || got.digest == (ResultDigest{}) {
					t.Fatal("saved native echoes changed", got)
				}
			} else if !reflect.DeepEqual(got.result, inventoryAcceptResult{}) || got.digest != (ResultDigest{}) {
				t.Fatal("fresh/retired returned a cohort")
			}
			if _, err := tx.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Prove lookup has no fresh-source/lease/epoch dependency or reconstruction.
	for _, sql := range []string{
		`UPDATE inventory_runs SET job_revision=9223372036854775807,job_lease_expires_ms=0 WHERE job_id='scan-hit'`,
		`UPDATE library_sources SET generation=77,incarnation='different-now' WHERE id='scan-source-hit'`,
		`UPDATE catalog_assets SET info_epoch=88 WHERE id=41`,
	} {
		if _, err := w.db.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	turn, _ := inventoryLookupTurn(t, w, registry)
	got, err := lookupInventoryAccept(t.Context(), turn, requests["hit"])
	if err != nil || got.result.AcceptedRunRevision != 2 || got.result.SourceIncarnation != "source-incarnation" || got.result.Members[0].AcceptedAssetRevision != 1 {
		t.Fatal("historical result refetched mutable facts", got, err)
	}
}

func TestInventoryLookupRejectsConflictAndIncompleteProof(t *testing.T) {
	w, requests, registry := inventoryLookupFixture(t)
	for _, c := range []struct {
		name string
		sql  string
		edit func(*ScanReceiptRequest)
		want error
	}{
		{"wrong_digest", "", func(r *ScanReceiptRequest) { r.Receipt.RequestDigest[0] = 1 }, ErrConflict},
		{"wrong_operation", "", func(r *ScanReceiptRequest) { r.Receipt.Identity.OperationID[0] ^= 1 }, ErrConflict},
		{"wrong_incarnation", "", func(r *ScanReceiptRequest) { r.Receipt.Identity.Incarnation[0] = 1 }, ErrOwnerUnavailable},
		{"missing_owner", "", func(r *ScanReceiptRequest) { r.RunID = "absent" }, ErrOwnerUnavailable},
		{"future_gap", "", func(r *ScanReceiptRequest) { r.Receipt.Identity.Sequence = 3 }, ErrConflict},
		{"unacked_predecessor", "", func(r *ScanReceiptRequest) { r.Receipt.Identity.Sequence = 2 }, ErrResolveRequired},
		{"saved_digest", `UPDATE scan_command_receipts SET result_digest=zeroblob(32) WHERE run_id='scan-hit'`, nil, ErrResolveRequired},
		{"wrong_kind", `UPDATE scan_command_receipts SET command_kind='basic_stage' WHERE run_id='scan-hit'`, nil, ErrConflict},
		{"missing_child", `DELETE FROM scan_command_results WHERE run_id='scan-hit'`, nil, ErrResolveRequired},
		{"extra_child", `INSERT INTO scan_command_results SELECT run_id,command_incarnation,command_sequence,1,child_kind,child_ordinal,'extra',library_id,subject_id,asset_id,accepted_asset_revision FROM scan_command_results WHERE run_id='scan-hit'`, nil, ErrResolveRequired},
		{"ordinal_gap", `UPDATE scan_command_results SET input_ordinal=1 WHERE run_id='scan-hit'`, nil, ErrResolveRequired},
		{"child_variant", `UPDATE scan_command_results SET child_kind=1,child_ordinal=1 WHERE run_id='scan-hit'`, nil, ErrResolveRequired},
		{"saved_member_changed", `UPDATE scan_command_results SET accepted_asset_revision=7 WHERE run_id='scan-hit'`, nil, ErrResolveRequired},
		{"source_echo_changed", `UPDATE scan_command_receipts SET source_revision='changed' WHERE run_id='scan-hit'`, nil, ErrResolveRequired},
		{"wrong_member_scope", "", func(r *ScanReceiptRequest) { r.LibraryID = 8 }, ErrResolveRequired},
	} {
		t.Run(c.name, func(t *testing.T) {
			turn, tx := inventoryLookupTurn(t, w, registry)
			if c.sql != "" {
				// Test-only fault stays in this transaction and is rolled back.
				if _, err := tx.conn.ExecContext(tx.ctx, c.sql); err != nil {
					t.Fatal(err)
				}
			}
			r := requests["hit"]
			if c.edit != nil {
				c.edit(&r)
			}
			got, err := lookupInventoryAccept(t.Context(), turn, r)
			if !errors.Is(err, c.want) || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
				t.Fatal("error returned partial proof", c.name, got, err)
			}
			tx.rollback(err)
		})
	}
}

func TestInventoryLookupMissingAboveFloorAndN1Boundary(t *testing.T) {
	w, requests, registry := inventoryLookupFixture(t)
	for _, count := range []int64{0, inventoryReceiptMaxMembers + 1, math.MaxInt64} {
		t.Run(strconv.FormatInt(count, 10), func(t *testing.T) {
			turn, tx := inventoryLookupTurn(t, w, registry)
			if _, err := tx.conn.ExecContext(tx.ctx, `UPDATE scan_command_receipts SET result_count=? WHERE run_id='scan-hit'`, count); err != nil {
				t.Fatal(err)
			}
			step := turn.steps[scanReceiptResults]
			step.work.Rows = math.MaxUint64
			turn.steps[scanReceiptResults] = step
			got, err := lookupInventoryAccept(t.Context(), turn, requests["hit"])
			if !reflect.DeepEqual(got, inventoryReceiptLookup{}) || errors.Is(err, ErrQuantum) {
				t.Fatal("unqualified count read a cohort", got, err)
			}
			if count == 0 {
				if !errors.Is(err, ErrResolveRequired) {
					t.Fatal(err)
				}
			} else {
				var refusal *Refusal
				if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified {
					t.Fatal(err)
				}
			}
			tx.rollback(err)
		})
	}
	turn, tx := inventoryLookupTurn(t, w, registry)
	for _, sql := range []string{`DELETE FROM scan_command_results WHERE run_id='scan-hit'`, `DELETE FROM scan_command_receipts WHERE run_id='scan-hit'`} {
		if _, err := tx.conn.ExecContext(tx.ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	got, err := lookupInventoryAccept(t.Context(), turn, requests["hit"])
	if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
		t.Fatal("missing unacked proof became fresh", got, err)
	}
	tx.rollback(err)
	turn, tx = inventoryLookupTurn(t, w, registry)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	got, err = lookupInventoryAccept(cancelled, turn, requests["hit"])
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
		t.Fatal("cancelled read returned proof", got, err)
	}
}
