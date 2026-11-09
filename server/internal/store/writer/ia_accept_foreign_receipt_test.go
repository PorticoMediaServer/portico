package writer

import (
	"errors"
	"testing"
)

// A receipt planted at the exact IA original key may belong to any other
// command purpose. Those purposes never populate the IA media projection, so the
// header decode must leave it undecoded: the kind mismatch is a typed conflict,
// never a raw NULL scan error from database/sql.
func TestIAAcceptForeignReceiptIsTypedConflict(t *testing.T) {
	plant := func(t *testing.T, w *executor, runID string, ref ReceiptReference) {
		t.Helper()
		wc008NativeExec(t, w, `INSERT INTO scan_command_receipts(run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,source_id,source_incarnation,source_revision,next_cursor,result_count,result_digest) VALUES(?,?,?,?,?,'inventory_accept',1,1,'foreign-source','foreign-incarnation','foreign-revision','',0,?)`,
			runID, ref.Identity.Incarnation[:], ref.Identity.Sequence, ref.Identity.OperationID[:], ref.RequestDigest[:], make([]byte, 32))
	}
	noEffects := func(t *testing.T, w *executor, runID string, count func(*testing.T, *executor, string, ...any) int64) {
		t.Helper()
		if got := count(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 1 {
			t.Fatal("foreign receipt changed", got)
		}
		if got := count(t, w, `SELECT count(*) FROM scan_media_result_members`); got != 0 {
			t.Fatal("refused request wrote media members", got)
		}
		if got := count(t, w, `SELECT count(*) FROM search_publish_jobs`); got != 0 {
			t.Fatal("refused request wrote search work", got)
		}
		if got := count(t, w, `SELECT last_command_sequence FROM inventory_runs WHERE job_id=?`, runID); got != 1 {
			t.Fatal("refused request advanced the run", got)
		}
	}
	t.Run("audiobook", func(t *testing.T) {
		w, reg, p := iaBookAcceptFixture(t)
		q := iaBookAcceptRequest(w)
		ref := iaBookAcceptReference(t, q, 2, 3)
		plant(t, w, q.scope.runID, ref)
		turn, out, err := iaBookAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
			t.Fatal("foreign receipt", out, err)
		}
		turn.transaction.rollback(nil)
		noEffects(t, w, q.scope.runID, iaBookCount)
	})
	t.Run("reading", func(t *testing.T) {
		w, reg, p := iaReadingAcceptFixture(t, 20)
		q := iaReadingAcceptRequest(w, 20, "epub")
		ref := iaReadingAcceptReference(t, q, 2, 3)
		plant(t, w, q.scope.runID, ref)
		turn, out, err := iaReadingAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
			t.Fatal("foreign receipt", out, err)
		}
		turn.transaction.rollback(nil)
		noEffects(t, w, q.scope.runID, iaReadingCount)
	})
	t.Run("extra", func(t *testing.T) {
		w, reg, p := iaExtraAcceptFixture(t)
		q := newIAExtraAcceptRequest(w)
		ref := iaExtraAcceptReference(t, q, 2, 3)
		plant(t, w, q.scope.runID, ref)
		turn, out, err := iaExtraAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
			t.Fatal("foreign receipt", out, err)
		}
		turn.transaction.rollback(nil)
		noEffects(t, w, q.scope.runID, iaEpisodeCount)
	})
	t.Run("membership", func(t *testing.T) {
		w, reg, p := iaMembershipAcceptFixture(t)
		q := newIAMembershipAcceptRequest(w)
		ref := iaMembershipAcceptReference(t, q, 2, 3)
		plant(t, w, q.scope.runID, ref)
		turn, out, err := iaMembershipAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
			t.Fatal("foreign receipt", out, err)
		}
		turn.transaction.rollback(nil)
		noEffects(t, w, q.scope.runID, iaEpisodeCount)
	})
}
