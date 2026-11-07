package writer

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestMGLCDraftReceiptReopenAckGCAndBirthFloor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {

		w, b := mgLCFixture(t)
		turn := mgLCTurn(t, w)
		c := mgLCNext(b, mgLCProgress{})
		p, err := beginMGLC(t.Context(), turn, b, c)
		if err != nil {
			t.Fatal(err)
		}
		saved := mgLCTestSave(t, turn, b, c, mgLCBeginKind, p)
		mgLCCommit(t, turn)
		var seq int
		var database, path string
		if err := w.db.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&seq, &database, &path); err != nil {
			t.Fatal(err)
		}
		if err := w.close(); err != nil {
			t.Fatal(err)
		}
		connector, err := sqliteconn.Connector(path, w.config)
		if err != nil {
			t.Fatal(err)
		}
		reopened := &executor{config: w.config, now: time.Now}
		reopened.openPool(connector, nil)
		t.Cleanup(func() {
			if err := reopened.close(); err != nil {
				t.Error(err)
			}
		})
		// Connection open/schema initialization belongs outside the admitted
		// command hold, just as in the ordinary canonical executor fixture.
		var warm int64
		if err := reopened.db.QueryRowContext(t.Context(), `SELECT command_sequence FROM lc_generation_command_receipts WHERE generation_id=? LIMIT 1`, b.generation).Scan(&warm); err != nil || warm != 1 {
			t.Fatal(warm, err)
		}
		turn = mgLCTurn(t, reopened)
		var before, after int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		decision, replay, err := lookupMGLCReceipt(t.Context(), turn, b, c, mgLCBeginKind)
		if err != nil || decision != receiptReplay || replay != saved {
			t.Fatal(decision, replay, err)
		}
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
			t.Fatal("replay had effects", before, after, err)
		}
		_, _ = turn.transaction.rollback(nil)
		p = mgLCTestAck(t, reopened, b, saved, p)
		if _, err := reopened.db.ExecContext(t.Context(), `DELETE FROM lc_generation_command_receipts WHERE generation_id=?`, b.generation); err != nil {
			t.Fatal(err)
		}
		turn = mgLCTurn(t, reopened)
		decision, _, err = lookupMGLCReceipt(t.Context(), turn, b, c, mgLCBeginKind)
		if err != nil || decision != receiptRetired {
			t.Fatal("ACKed GC resurrected", decision, err)
		}
		_, _ = turn.transaction.rollback(nil)
		// GC must not let a second incarnation classify this LIVE generation as
		// fresh, even though that incarnation has no matching receipt namespace.
		otherLive := b
		otherLive.incarnation[0] ^= 1
		turn = mgLCTurn(t, reopened)
		if _, _, err := lookupMGLCReceipt(t.Context(), turn, otherLive, mgLCNext(otherLive, mgLCProgress{}), mgLCBeginKind); !errors.Is(err, ErrConflict) {
			t.Fatal("live generation identity recycled after ACK GC", err)
		}
		_, _ = turn.transaction.rollback(nil)
		// Fixture-only retained floor stands in for the parent's bounded retirement
		// command. It is written before deleting the owner, while the channel lives.
		if _, err := reopened.db.ExecContext(t.Context(), `INSERT INTO lc_generation_retirements(generation_id,command_incarnation,create_operation_id,birth_request_digest,channel_id,final_sequence,final_result_digest,retired_ms) SELECT id,command_incarnation,create_operation_id,birth_request_digest,channel_id,last_command_sequence,?,1 FROM lc_generations WHERE id=? AND last_command_sequence=acknowledged_command_sequence`, saved.digest[:], b.generation); err != nil {
			t.Fatal(err)
		}
		if _, err := reopened.db.ExecContext(t.Context(), `DELETE FROM lc_generations WHERE id=?`, b.generation); err != nil {
			t.Fatal(err)
		}
		turn = mgLCTurn(t, reopened)
		decision, _, err = lookupMGLCReceipt(t.Context(), turn, b, c, mgLCBeginKind)
		if err != nil || decision != receiptRetired {
			t.Fatal("owner absence ignored floor", decision, err)
		}
		if got, err := beginMGLC(t.Context(), turn, b, c); !errors.Is(err, ErrConflict) || got != (mgLCProgress{}) {
			t.Fatal("retired birth resurrected", got, err)
		}
		other := b
		other.incarnation[0] ^= 1
		if _, _, err := lookupMGLCReceipt(t.Context(), turn, other, mgLCNext(other, mgLCProgress{}), mgLCBeginKind); !errors.Is(err, ErrConflict) {
			t.Fatal("owner incarnation recycled", err)
		}

	})
}
func TestMGLCDraftCandidateDuplicateRollsBackActualCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {

		w, b := mgLCFixture(t)
		p := mgLCBeginFixture(t, w, b)
		turn := mgLCTurn(t, w)
		c := mgLCNext(b, p)
		first := mgLCCandidateFixture()
		duplicate := first
		duplicate.ordinal = 1
		duplicate.title = "changed body"
		if got, err := stageMGLCCandidates(t.Context(), turn, b, c, p, []mgLCCandidate{first, duplicate}, "id:101", 2, 0); err == nil || got != (mgLCProgress{}) {
			t.Fatal(got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		var count, entries, bytes, rows int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT candidate_count,entry_count,staged_bytes,(SELECT count(*) FROM lc_candidates WHERE generation_id='lc-new') FROM lc_generations WHERE id='lc-new'`).Scan(&count, &entries, &bytes, &rows); err != nil || count != 0 || entries != 0 || bytes != 0 || rows != 0 {
			t.Fatal(count, entries, bytes, rows, err)
		}
		turn = mgLCTurn(t, w)
		first.ordinal = 2
		if _, err := stageMGLCCandidates(t.Context(), turn, b, c, p, []mgLCCandidate{first}, "id:101", 1, 0); !errors.Is(err, ErrConflict) {
			t.Fatal("ordinal gap", err)
		}

	})
}

func TestMGLCReceiptNativeDigestRejectsTampering(t *testing.T) {
	for _, field := range []string{"result_cursor_ms", "result_lease_token", "catalog_fence"} {
		t.Run(field, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {

				w, b := mgLCFixture(t)
				turn := mgLCTurn(t, w)
				c := mgLCNext(b, mgLCProgress{})
				p, err := beginMGLC(t.Context(), turn, b, c)
				if err != nil {
					t.Fatal(err)
				}
				saved := mgLCTestSave(t, turn, b, c, mgLCBeginKind, p)
				mgLCCommit(t, turn)
				value := any("tampered")
				if field == "result_cursor_ms" {
					value = int64(1234)
				}
				if _, err := w.db.ExecContext(t.Context(), "UPDATE lc_generation_command_receipts SET "+field+"=? WHERE generation_id=?", value, b.generation); err != nil {
					t.Fatal(err)
				}
				turn = mgLCTurn(t, w)
				decision, out, err := lookupMGLCReceipt(t.Context(), turn, b, c, mgLCBeginKind)
				if !errors.Is(err, ErrConflict) || decision != 0 || out != (mgLCSaved{}) {
					t.Fatal("tampered native receipt accepted", decision, out, err)
				}
				if ack, err := acknowledgeMGLC(t.Context(), turn, b, c, mgLCBeginKind, saved.digest); !errors.Is(err, ErrConflict) || ack != 0 {
					t.Fatal("tampered receipt ACK", ack, err)
				}

			})
		})
	}
}
