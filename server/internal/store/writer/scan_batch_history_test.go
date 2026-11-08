package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Saved rows are supplied independently of the fresh writer. Replay must be
// able to recover every supported width even with an N=1 accepting profile.
func batchHistoryFixture(t *testing.T, width int) (*executor, ScanReceiptRequest, inventoryAcceptResult) {
	t.Helper()
	w, requests, _ := inventoryLookupFixture(t)
	q := requests["hit"]
	v := inventoryAcceptResult{RunID: q.RunID, AcceptedRunRevision: 2, AppliedMS: 100,
		SourceID: "scan-source-hit", SourceIncarnation: "source-incarnation", SourceRevision: "opaque:007", NextCursor: "native-next", ResultCount: int64(width)}
	for i := 0; i < width; i++ {
		object := strings.Repeat("o", 10+i)
		v.Members = append(v.Members, inventoryResultMember{int64(i), 0, 0, object, 7, 31, 41, 1})
		if i == 0 {
			if _, err := w.db.ExecContext(t.Context(), `UPDATE scan_command_results SET object_id=? WHERE run_id=?`, object, q.RunID); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO scan_command_results SELECT run_id,command_incarnation,command_sequence,?,child_kind,child_ordinal,?,library_id,subject_id,asset_id,accepted_asset_revision FROM scan_command_results WHERE run_id=? AND input_ordinal=0`, i, object, q.RunID); err != nil {
				t.Fatal(err)
			}
		}
	}
	digest, err := inventoryAcceptResultDigest(q.Receipt.Identity, q.Receipt.RequestDigest, v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE scan_command_receipts SET result_count=?,result_digest=? WHERE run_id=?`, width, digest[:], q.RunID); err != nil {
		t.Fatal(err)
	}
	return w, q, v
}

func TestInventoryBatchHistoryRejectsExtraRowAtMaximumWidth(t *testing.T) {
	w, q, _ := batchHistoryFixture(t, maxInventoryBatchFiles)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO scan_command_results SELECT run_id,command_incarnation,command_sequence,8,child_kind,child_ordinal,'extra-object',library_id,subject_id,asset_id,accepted_asset_revision FROM scan_command_results WHERE run_id=? AND input_ordinal=0`, q.RunID); err != nil {
		t.Fatal(err)
	}
	registry := cachedNativeInventoryFixtureRegistry(t, w, inventoryLookupDefinitions(), "inventory_resolution_test.go", "scan_receipts_test.go")
	turn, tx := inventoryLookupTurn(t, w, registry)
	got, err := lookupInventoryAccept(t.Context(), turn, q)
	if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
		t.Fatal("extra maximum-width row escaped numeric key preflight", got, err)
	}
	tx.rollback(err)
}

type batchMemberLengthEchoConn struct {
	writerDriverConn
	ordinal int64
}
type batchMemberLengthEchoRows struct{ driver.Rows }

func (c *batchMemberLengthEchoConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, query, args)
	if err == nil && strings.Contains(query, "octet_length(object_id)") && len(args) == 6 && args[3].Value == c.ordinal {
		return &batchMemberLengthEchoRows{rows}, nil
	}
	return rows, err
}
func (r *batchMemberLengthEchoRows) Next(values []driver.Value) error {
	err := r.Rows.Next(values)
	if err == nil {
		values[0] = values[0].(int64) + 1
	}
	return err
}

// The payload and its saved digest remain correct. Only the native octet
// preflight lies, so the exact per-member echo (not a digest mismatch) must
// reject a payload copied under a different byte observation.
func TestInventoryBatchHistoryChecksEachMemberOctetEcho(t *testing.T) {
	for _, ordinal := range []int64{0, maxInventoryBatchFiles - 1} {
		t.Run(strings.Repeat("N", int(ordinal)+1), func(t *testing.T) {
			w, q, _ := batchHistoryFixture(t, maxInventoryBatchFiles)
			registry := cachedNativeInventoryFixtureRegistry(t, w, inventoryLookupDefinitions(), "inventory_resolution_test.go", "scan_receipts_test.go")
			var sequence int64
			var name, path string
			if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
				t.Fatal(err)
			}
			if err := w.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config)
			if err != nil {
				t.Fatal(err)
			}
			w.openPool(connector, func(conn driver.Conn) driver.Conn {
				return &batchMemberLengthEchoConn{conn.(writerDriverConn), ordinal}
			})
			turn, tx := inventoryLookupTurn(t, w, registry)
			got, err := lookupInventoryAccept(t.Context(), turn, q)
			if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
				t.Fatal("member payload escaped exact octet echo", got, err)
			}
			tx.rollback(err)
		})
	}
}

func TestInventoryBatchHistory(t *testing.T) {
	for _, width := range []int{1, 2, maxInventoryBatchFiles} {
		t.Run(strings.Repeat("N", width), func(t *testing.T) {
			w, q, want := batchHistoryFixture(t, width)
			registry := cachedNativeInventoryFixtureRegistry(t, w, inventoryLookupDefinitions(), "inventory_resolution_test.go", "scan_receipts_test.go")
			turn, tx := inventoryLookupTurn(t, w, registry)
			var before, after int64
			if err := tx.conn.QueryRowContext(tx.ctx, `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			got, err := lookupInventoryAccept(t.Context(), turn, q)
			if err != nil || got.decision != receiptReplay || !reflect.DeepEqual(got.result, want) {
				t.Fatal("saved cohort", got, want, err)
			}
			if err := tx.conn.QueryRowContext(tx.ctx, `SELECT total_changes()`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatal("replay wrote effects")
			}
			tx.rollback(nil)
		})
	}
}

func TestInventoryBatchHistoryRefusesMalformedCohortsBeforeText(t *testing.T) {
	for _, mutation := range []string{
		`DELETE FROM scan_command_results WHERE run_id='scan-hit' AND input_ordinal=1`,
		`UPDATE scan_command_results SET input_ordinal=8 WHERE run_id='scan-hit' AND input_ordinal=1`,
		`UPDATE scan_command_results SET child_kind=1,child_ordinal=1 WHERE run_id='scan-hit' AND input_ordinal=1`,
		`UPDATE scan_command_results SET object_id=printf('%.*c',2097152,'x') WHERE run_id='scan-hit' AND input_ordinal=1`,
	} {
		t.Run(mutation, func(t *testing.T) {
			w, q, _ := batchHistoryFixture(t, 2)
			if _, err := w.db.ExecContext(t.Context(), mutation); err != nil {
				t.Fatal(err)
			}
			registry := cachedNativeInventoryFixtureRegistry(t, w, inventoryLookupDefinitions(), "inventory_resolution_test.go", "scan_receipts_test.go")
			turn, tx := inventoryLookupTurn(t, w, registry)
			step := turn.steps[scanReceiptResults]
			step.work.Rows = math.MaxUint64
			turn.steps[scanReceiptResults] = step
			got, err := lookupInventoryAccept(t.Context(), turn, q)
			if err == nil || errors.Is(err, ErrQuantum) || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
				t.Fatal("copied unpreflighted cohort", got, err)
			}
			tx.rollback(err)
		})
	}
}
