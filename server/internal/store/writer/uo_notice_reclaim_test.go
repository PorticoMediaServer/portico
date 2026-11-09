package writer

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func uoNoticeReclaimFixture(t *testing.T) *executor {
	t.Helper()
	return wc008NativeExecutor(t)
}

func uoNoticeReclaimTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	return wc008NativeTurn(t, w, ReclaimNotices, uoNoticeReclaimDefinitions())
}

func uoNoticeReclaimOne(t *testing.T, w *executor, nowMS int64) uoNoticeReclaimEffect {
	t.Helper()
	turn := uoNoticeReclaimTurn(t, w)
	out, err := reclaimUONotices(t.Context(), turn, nowMS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Independent plain-SQL facts, never the reclaim effect's own arithmetic.
func uoNoticeReclaimFacts(t *testing.T, w *executor) (records, acks, operations, revision, events int64) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), `SELECT
	(SELECT count(*) FROM notification_records),
	(SELECT count(*) FROM notification_acknowledgments),
	(SELECT count(*) FROM notice_broadcast_work),
	COALESCE((SELECT value FROM notification_revision WHERE singleton=1),0),
	(SELECT count(*) FROM api_events)`).Scan(&records, &acks, &operations, &revision, &events); err != nil {
		t.Fatal(err)
	}
	return
}

func uoNoticeReclaimScalar(t *testing.T, w *executor, q string, args ...any) (n int64) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUONoticeReclaimBoundedQuantaAndActualBytes(t *testing.T) {
	w := uoNoticeReclaimFixture(t)
	q := uoNoticeDraft("a")
	q.expiresMS = 100
	q.operation = uoNoticeOperation{id: "broadcast-one", digest: [32]byte{1}, expiresMS: 100}
	a := uoNoticePublishOne(t, w, q)
	for i := range 35 {
		wc008NativeExec(t, w, `INSERT INTO notification_acknowledgments(principal_key,notice_sequence,revision) VALUES(?,?,?)`, fmt.Sprintf("p%03d", i), a.sequence, a.revision+1)
	}
	_, _, _, revision, events := uoNoticeReclaimFacts(t, w)
	operationBytes := uoNoticeReclaimScalar(t, w, `SELECT length(scope)+length(operation_id)+length(digest)+length(upper_account)+length(upper_profile)+length(last_account)+length(last_profile)+length(ids) FROM notice_broadcast_work`)
	wantAckBytes := func() int64 {
		return uoNoticeReclaimScalar(t, w, `SELECT COALESCE(SUM(length(principal_key)),0) FROM (SELECT principal_key FROM notification_acknowledgments WHERE notice_sequence=? ORDER BY principal_key LIMIT 16)`, a.sequence)
	}
	firstExpected, secondExpected := wantAckBytes(), wantAckBytes()
	first := uoNoticeReclaimOne(t, w, 100)
	if first.ackRows != 16 || first.noticeRows != 0 || !first.noticePending || first.operationRows != 1 || first.operationPending || first.ackBytes != firstExpected || first.operationBytes != operationBytes || first.refundedBytes() != firstExpected+operationBytes {
		t.Fatal("first bounded quantum", first, firstExpected, operationBytes)
	}
	second := uoNoticeReclaimOne(t, w, 100)
	if second.ackRows != 16 || second.noticeRows != 0 || !second.noticePending || second.operationRows != 0 || second.ackBytes != secondExpected {
		t.Fatal("second bounded quantum", second, secondExpected)
	}
	recordBytes := uoNoticeReclaimScalar(t, w, `SELECT length(id)+length(scope)+length(audience)+length(severity)+length(source)+length(category)+length(title)+length(body)+length(arguments)+length(actions)+length(dedupe_key) FROM notification_records WHERE sequence=?`, a.sequence)
	thirdExpected := wantAckBytes()
	third := uoNoticeReclaimOne(t, w, 100)
	if third.ackRows != 3 || third.noticeRows != 1 || third.noticePending || third.operationRows != 0 || third.ackBytes != thirdExpected || third.noticeBytes != recordBytes || third.refundedBytes() != thirdExpected+recordBytes {
		t.Fatal("final bounded quantum", third, thirdExpected, recordBytes)
	}
	if records, acks, operations, revisionAfter, eventsAfter := uoNoticeReclaimFacts(t, w); records != 0 || acks != 0 || operations != 0 || revisionAfter != revision || eventsAfter != events {
		t.Fatal("reclaim touched shared notice state", records, acks, operations, revisionAfter, eventsAfter)
	}
}

func TestUONoticeReclaimCapPlusOneSentinel(t *testing.T) {
	w := uoNoticeReclaimFixture(t)
	q := uoNoticeDraft("a")
	q.expiresMS = 100
	a := uoNoticePublishOne(t, w, q)
	for i := range uoNoticeReclaimMaxAckRows + 1 {
		wc008NativeExec(t, w, `INSERT INTO notification_acknowledgments(principal_key,notice_sequence,revision) VALUES(?,?,?)`, fmt.Sprintf("p%03d", i), a.sequence, a.revision+1)
	}
	first := uoNoticeReclaimOne(t, w, 100)
	if first.ackRows != uoNoticeReclaimMaxAckRows || first.noticeRows != 0 || !first.noticePending {
		t.Fatal("seventeenth ACK did not bound the quantum", first)
	}
	second := uoNoticeReclaimOne(t, w, 100)
	if second.ackRows != 1 || second.noticeRows != 1 || second.noticePending {
		t.Fatal("retained tombstone not reclaimed after its last ACK", second)
	}
}

func TestUONoticeReclaimLeavesLiveAndUnexpiredWork(t *testing.T) {
	w := uoNoticeReclaimFixture(t)
	live := uoNoticePublishOne(t, w, uoNoticeDraft("live"))
	expired := uoNoticeDraft("expired")
	expired.expiresMS = 100
	uoNoticePublishOne(t, w, expired)
	operation := uoNoticeDraft("operation")
	operation.expiresMS = 100
	operation.operation = uoNoticeOperation{id: "live-operation", digest: [32]byte{2}, expiresMS: 10000}
	uoNoticePublishOne(t, w, operation)
	out := uoNoticeReclaimOne(t, w, 100)
	if out.noticeRows != 1 || out.operationRows != 0 || out.operationPending || out.noticeBytes <= 0 {
		t.Fatal("live work reclaimed", out)
	}
	var liveRecords, operations int
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM notification_records WHERE sequence=?), (SELECT count(*) FROM notice_broadcast_work)`, live.sequence).Scan(&liveRecords, &operations); err != nil || liveRecords != 1 || operations != 1 {
		t.Fatal("live notice or unexpired operation deleted", liveRecords, operations, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM notification_records WHERE sequence=?`, live.sequence).Scan(&liveRecords); err != nil || liveRecords != 1 {
		t.Fatal("live notice deleted", liveRecords, err)
	}
}

func TestUONoticeReclaimKeepsLiveNoticeAcrossQuanta(t *testing.T) {
	w := uoNoticeReclaimFixture(t)
	// The live notice outlives the quantum clock and carries ACKs. It sorts last
	// in the expiry index, so a query that loses `expires_ms<=?` reaches it once
	// the two expired records are gone and would delete it in a later quantum.
	live := uoNoticeDraft("live")
	live.expiresMS = 101
	liveRecord := uoNoticePublishOne(t, w, live)
	for i := range 3 {
		wc008NativeExec(t, w, `INSERT INTO notification_acknowledgments(principal_key,notice_sequence,revision) VALUES(?,?,?)`, fmt.Sprintf("live%02d", i), liveRecord.sequence, liveRecord.revision+1)
	}
	for _, id := range []string{"expired-one", "expired-two"} {
		q := uoNoticeDraft(id)
		q.expiresMS = 100
		uoNoticePublishOne(t, w, q)
	}
	first := uoNoticeReclaimOne(t, w, 100)
	if first.noticeRows != 1 || first.ackRows != 0 || !first.noticePending || first.operationRows != 0 || first.operationPending {
		t.Fatal("first expired quantum", first)
	}
	second := uoNoticeReclaimOne(t, w, 100)
	if second.noticeRows != 1 || second.ackRows != 0 || second.noticePending || second.operationRows != 0 || second.operationPending {
		t.Fatal("second expired quantum", second)
	}
	// Nothing expires at this clock now. Any non-zero effect means the live
	// notice or one of its acknowledgements was reclaimed.
	third := uoNoticeReclaimOne(t, w, 100)
	if third != (uoNoticeReclaimEffect{}) {
		t.Fatal("live notice reclaimed", third)
	}
	var records, acks int
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM notification_records WHERE sequence=?), (SELECT count(*) FROM notification_acknowledgments WHERE notice_sequence=?)`, liveRecord.sequence, liveRecord.sequence).Scan(&records, &acks); err != nil || records != 1 || acks != 3 {
		t.Fatal("live notice or ACKs did not survive", records, acks, err)
	}
}

func TestUONoticeReclaimRefundsLastProfile(t *testing.T) {
	w := uoNoticeReclaimFixture(t)
	q := uoNoticeDraft("operation")
	q.expiresMS = 100
	q.operation = uoNoticeOperation{id: "broadcast-last-profile", digest: [32]byte{3}, expiresMS: 100}
	uoNoticePublishOne(t, w, q)
	wc008NativeExec(t, w, `UPDATE notice_broadcast_work SET last_profile='profile-extra'`)
	// Independent refund oracle: the sum of the actual deleted text columns,
	// read one at a time. `last_profile` is non-empty, so leaving it out of the
	// returned columns under-refunds and fails here.
	var want int64
	for _, column := range []string{"scope", "operation_id", "digest", "upper_account", "upper_profile", "last_account", "last_profile", "ids"} {
		want += uoNoticeReclaimScalar(t, w, `SELECT length(`+column+`) FROM notice_broadcast_work`)
	}
	out := uoNoticeReclaimOne(t, w, 100)
	if out.operationRows != 1 || out.operationBytes != want || out.noticeRows != 1 || out.noticeBytes <= 0 {
		t.Fatal("operation refund omitted last_profile", out, want)
	}
}

func TestUONoticeReclaimRollbackOnFailure(t *testing.T) {
	for _, failure := range []string{"cancel", "missing-turn"} {
		t.Run(failure, func(t *testing.T) {
			w := uoNoticeReclaimFixture(t)
			expired := uoNoticeDraft("expired")
			expired.expiresMS = 100
			a := uoNoticePublishOne(t, w, expired)
			wc008NativeExec(t, w, `INSERT INTO notification_acknowledgments(principal_key,notice_sequence,revision) VALUES('p000',?,?)`, a.sequence, a.revision+1)
			records, acks, operations, revision, events := uoNoticeReclaimFacts(t, w)
			turn := uoNoticeReclaimTurn(t, w)
			ctx := t.Context()
			if failure == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			target := turn
			if failure == "missing-turn" {
				target = nil
			}
			out, err := reclaimUONotices(ctx, target, 100)
			if err == nil || out != (uoNoticeReclaimEffect{}) {
				t.Fatal("failure leaked a reclaim result", out, err)
			}
			if turn != nil {
				if _, err = turn.transaction.rollback(nil); err != nil {
					t.Fatal(err)
				}
			}
			if gotRecords, gotAcks, gotOperations, gotRevision, gotEvents := uoNoticeReclaimFacts(t, w); gotRecords != records || gotAcks != acks || gotOperations != operations || gotRevision != revision || gotEvents != events {
				t.Fatal("failed reclaim left a prefix", gotRecords, gotAcks, gotOperations, gotRevision, gotEvents)
			}
		})
	}
}

func TestUONoticeReclaimRootSeamRefuses(t *testing.T) {
	w := uoNoticeReclaimFixture(t)
	expired := uoNoticeDraft("expired")
	expired.expiresMS = 100
	uoNoticePublishOne(t, w, expired)
	out := uoNoticeReclaimOne(t, w, 100)
	binding, err := bindUONoticeReclaimRoot(out)
	if !errors.Is(err, ErrNoticeRootUnavailable) || binding != (uoNoticeRootBinding{}) {
		t.Fatal("root seam must refuse until UO-01/UO-02 land", binding, err)
	}
	var records int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM notification_records`).Scan(&records); err != nil || records != 0 {
		t.Fatal("root refusal wrote native state", records, err)
	}
}

func TestUONoticeReclaimDefinitionsPrepare(t *testing.T) {
	w := uoNoticeReclaimFixture(t)
	wc008NativePrepare(t, w, uoNoticeReclaimDefinitions())
}

func TestUONoticeReclaimInputBounds(t *testing.T) {
	w := uoNoticeReclaimFixture(t)
	turn := uoNoticeReclaimTurn(t, w)
	for _, now := range []int64{0, -1} {
		if out, err := reclaimUONotices(t.Context(), turn, now); !errors.Is(err, ErrInvalid) || out != (uoNoticeReclaimEffect{}) {
			t.Fatal("accepted invalid maintenance clock", out, err)
		}
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	// sql.ErrNoRows on both candidate probes is the ordinary empty case.
	empty := uoNoticeReclaimOne(t, w, 100)
	if empty != (uoNoticeReclaimEffect{}) {
		t.Fatal("empty reclaim was not a no-op", empty)
	}
}
