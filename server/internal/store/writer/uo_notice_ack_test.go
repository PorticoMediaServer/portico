package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func uoNoticeAckFixture(t *testing.T) *executor {
	t.Helper()
	return wc008NativeExecutor(t)
}

func uoNoticeAckCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func uoNoticeAckDefs() []stmt.Definition {
	return append(uoNoticeAckDefinitions(), wcAPIEventDefinitions()...)
}

func uoNoticeAckPrincipal() string { return "principal:one:shared-admins" }

func uoNoticeAckRequest(principal string, nowMS int64, ops ...uoNoticeAckOperation) uoNoticeAckInput {
	return uoNoticeAckInput{view: uoNoticeDraft("a").view, principal: principal, nowMS: nowMS, operations: ops}
}

func uoNoticeAckOp(action uoNoticeAction, notices ...uoNoticeEffect) uoNoticeAckOperation {
	op := uoNoticeAckOperation{action: action}
	for _, n := range notices {
		op.targets = append(op.targets, uoNoticeAckTarget{n.id, n.revision, n.authorityRevision})
	}
	return op
}

func uoNoticeAckOne(t *testing.T, w *executor, q uoNoticeAckInput) uoNoticeAckEffect {
	t.Helper()
	turn := wc008NativeTurn(t, w, AcknowledgeNotices, uoNoticeAckDefs())
	out, err := applyUONoticeAcks(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	uoNoticeAckCommit(t, turn)
	return out
}

func uoNoticeAckIsZero(out uoNoticeAckEffect) bool {
	return out.revision == 0 && out.changed == 0 && out.outcomes == nil && out.event == (wcAPIEventEffect{})
}

// Stored facts, read with plain SQL. Tests compare these against literal
// expectations rather than calling the writer's resolver.
func uoNoticeAckStored(t *testing.T, w *executor, principal string, sequence int64) (revision int64, read, archive sql.NullInt64) {
	t.Helper()
	err := w.db.QueryRowContext(t.Context(), `SELECT revision,read_ms,archived_ms FROM notification_acknowledgments WHERE principal_key=? AND notice_sequence=?`, principal, sequence).Scan(&revision, &read, &archive)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, sql.NullInt64{}, sql.NullInt64{}
	}
	if err != nil {
		t.Fatal(err)
	}
	return revision, read, archive
}

func uoNoticeAckHeadStored(t *testing.T, w *executor, principal string) (sequence, revision, readMS, rows int64) {
	t.Helper()
	err := w.db.QueryRowContext(t.Context(), `SELECT COALESCE(MAX(read_through_sequence),0),COALESCE(MAX(revision),0),COALESCE(MAX(read_through_ms),0),count(*) FROM notification_ack_heads WHERE principal_key=?`, principal).Scan(&sequence, &revision, &readMS, &rows)
	if err != nil {
		t.Fatal(err)
	}
	return sequence, revision, readMS, rows
}

// uoNoticeAckEffective is the independent oracle: one plain-SQL statement over
// the two stored tables that applies the published read-time rule without
// calling the writer's native resolver. It is parameterized by principal, so it
// also proves that another principal's rows are never consulted.
func uoNoticeAckEffective(t *testing.T, w *executor, principal string, notice uoNoticeEffect) (read, archive sql.NullInt64) {
	t.Helper()
	const oracle = `WITH n(seq,rev) AS (SELECT ?,?)
SELECT
 CASE
  WHEN a.revision >= n.rev AND a.read_ms IS NOT NULL THEN a.read_ms
  WHEN a.revision >= n.rev AND a.archived_ms IS NOT NULL THEN NULL
  WHEN h.read_through_sequence >= n.seq AND h.revision >= n.rev AND (a.principal_key IS NULL OR a.revision <= h.revision) THEN h.read_through_ms
  ELSE NULL
 END,
 CASE WHEN a.revision >= n.rev THEN a.archived_ms ELSE NULL END
FROM n
LEFT JOIN notification_acknowledgments a ON a.principal_key=? AND a.notice_sequence=n.seq
LEFT JOIN notification_ack_heads h ON h.principal_key=?`
	if err := w.db.QueryRowContext(t.Context(), oracle, notice.sequence, notice.revision, principal, principal).Scan(&read, &archive); err != nil {
		t.Fatal(err)
	}
	return read, archive
}

func uoNoticeAckNoticeFacts(t *testing.T, w *executor) (records, revisions, inboxRevision int64) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM notification_records),(SELECT COALESCE(SUM(revision),0) FROM notification_records),(SELECT COALESCE(MAX(revision),0) FROM notification_inbox)`).Scan(&records, &revisions, &inboxRevision); err != nil {
		t.Fatal(err)
	}
	return records, revisions, inboxRevision
}

func uoNoticeAckEventCount(t *testing.T, w *executor) (events int64) {
	t.Helper()
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM api_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestUONoticeAckReadThroughMovesCoveredTime(t *testing.T) {
	w := uoNoticeAckFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	c := uoNoticePublishOne(t, w, uoNoticeDraft("c"))
	readC := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 50, uoNoticeAckOp(uoNoticeRead, c)))
	if len(readC.outcomes) != 1 || readC.changed != 1 || !readC.outcomes[0].changed ||
		readC.outcomes[0].action != uoNoticeRead || readC.outcomes[0].id != "c" ||
		readC.outcomes[0].readMS != (sql.NullInt64{Int64: 50, Valid: true}) || readC.outcomes[0].archivedMS.Valid ||
		readC.outcomes[0].revision != c.revision+1 {
		t.Fatal("individual read outcome", readC)
	}
	first := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 100, uoNoticeAckOp(uoNoticeReadThrough)))
	if len(first.outcomes) != 1 || first.outcomes[0].through != c.sequence || !first.outcomes[0].changed || first.changed != 1 {
		t.Fatal("first read-through", first)
	}
	// A notice published after the first read-all is not covered yet.
	b := uoNoticePublishOne(t, w, uoNoticeDraft("b"))
	records, revisions, inboxRevision := uoNoticeAckNoticeFacts(t, w)
	second := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 200, uoNoticeAckOp(uoNoticeReadThrough)))
	if len(second.outcomes) != 1 || second.outcomes[0].through != b.sequence || !second.outcomes[0].changed {
		t.Fatal("second read-through", second)
	}
	sequence, revision, readMS, rows := uoNoticeAckHeadStored(t, w, uoNoticeAckPrincipal())
	if sequence != b.sequence || readMS != 200 || revision != second.revision || rows != 1 {
		t.Fatal("read-through head", sequence, revision, readMS, rows)
	}
	// Only C has a sparse individual row; a bulk read never materializes one.
	var acks int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM notification_acknowledgments`).Scan(&acks); err != nil || acks != 1 {
		t.Fatal("sparse acknowledgement rows", acks, err)
	}
	if revisionC, read, archive := uoNoticeAckStored(t, w, uoNoticeAckPrincipal(), c.sequence); revisionC != readC.revision || read != (sql.NullInt64{Int64: 50, Valid: true}) || archive.Valid {
		t.Fatal("individual earlier read was replaced by the bulk time", revisionC, read, archive)
	}
	// Independent oracle: A and B are covered at 200, C keeps its exact 50.
	for _, test := range []struct {
		n  uoNoticeEffect
		ms int64
	}{{a, 200}, {b, 200}, {c, 50}} {
		read, archive := uoNoticeAckEffective(t, w, uoNoticeAckPrincipal(), test.n)
		if read != (sql.NullInt64{Int64: test.ms, Valid: true}) || archive.Valid {
			t.Fatal("effective read time", test.n.id, read, archive, test.ms)
		}
	}
	// Acknowledging never rewrites the shared record, the publication revision
	// or the durable inbox head: there is no recipient update.
	if gotRecords, gotRevisions, gotInbox := uoNoticeAckNoticeFacts(t, w); gotRecords != records || gotRevisions != revisions || gotInbox != inboxRevision {
		t.Fatal("acknowledgement touched shared notice state", gotRecords, gotRevisions, gotInbox)
	}
	if events := uoNoticeAckEventCount(t, w); events != 6 {
		t.Fatal("one hint per changed command", events)
	}
}

func TestUONoticeAckOrderedEightOperations(t *testing.T) {
	w := uoNoticeAckFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	// Order is public: read-through then unread is not the same as unread then
	// read-through, so the batch keeps its ordered ordinal range.
	ops := []uoNoticeAckOperation{
		uoNoticeAckOp(uoNoticeReadThrough), uoNoticeAckOp(uoNoticeUnread, a), uoNoticeAckOp(uoNoticeReadThrough), uoNoticeAckOp(uoNoticeUnread, a),
		uoNoticeAckOp(uoNoticeArchive, a), uoNoticeAckOp(uoNoticeUnread, a), uoNoticeAckOp(uoNoticeReadThrough), uoNoticeAckOp(uoNoticeUnarchive, a),
	}
	out := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 100, ops...))
	if len(out.outcomes) != 8 || out.revision != a.revision+8 {
		t.Fatal("not one bounded ordered range", out)
	}
	for i, result := range out.outcomes {
		if result.action != ops[i].action || result.revision != a.revision+int64(i)+1 {
			t.Fatal("operation order collapsed", i, result)
		}
	}
	revision, read, archive := uoNoticeAckStored(t, w, uoNoticeAckPrincipal(), a.sequence)
	if read.Valid || archive.Valid || revision != out.revision {
		t.Fatal("archive/unread/read-through/unarchive order", revision, read, archive)
	}
	if read, archive := uoNoticeAckEffective(t, w, uoNoticeAckPrincipal(), a); read.Valid || archive.Valid {
		t.Fatal("oracle disagrees with the ordered batch", read, archive)
	}
	readAgain := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 200, uoNoticeAckOp(uoNoticeUnread, a), uoNoticeAckOp(uoNoticeReadThrough)))
	if len(readAgain.outcomes) != 2 || !readAgain.outcomes[1].changed {
		t.Fatal("later read-through", readAgain)
	}
	if read, _ := uoNoticeAckEffective(t, w, uoNoticeAckPrincipal(), a); read != (sql.NullInt64{Int64: 200, Valid: true}) {
		t.Fatal("later read-through did not cover the unread", read)
	}
	// The whole batch reserves one contiguous range in a single allocation.
	wc008NativeExec(t, w, `CREATE TABLE uo_test_ack_allocations(n INTEGER)`)
	wc008NativeExec(t, w, `CREATE TRIGGER uo_test_ack_allocations AFTER UPDATE ON notification_revision BEGIN INSERT INTO uo_test_ack_allocations VALUES(new.value-old.value); END`)
	uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 300, ops...))
	var allocations, width int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*),max(n) FROM uo_test_ack_allocations`).Scan(&allocations, &width); err != nil || allocations != 1 || width != 8 {
		t.Fatal("range not reserved once", allocations, width, err)
	}
}

func TestUONoticeAckArchiveUnreadExclusion(t *testing.T) {
	w := uoNoticeAckFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 100, uoNoticeAckOp(uoNoticeArchive, a)))
	uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 200, uoNoticeAckOp(uoNoticeUnread, a)))
	uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 300, uoNoticeAckOp(uoNoticeReadThrough)))
	read, archive := uoNoticeAckEffective(t, w, uoNoticeAckPrincipal(), a)
	if read.Valid || archive != (sql.NullInt64{Int64: 100, Valid: true}) {
		t.Fatal("read-through reached an archived unread notice", read, archive)
	}
	uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 400, uoNoticeAckOp(uoNoticeUnarchive, a)))
	if read, archive := uoNoticeAckEffective(t, w, uoNoticeAckPrincipal(), a); read.Valid || archive.Valid {
		t.Fatal("unarchive resurrected excluded bulk state", read, archive)
	}
	uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 500, uoNoticeAckOp(uoNoticeReadThrough)))
	if read, _ := uoNoticeAckEffective(t, w, uoNoticeAckPrincipal(), a); read != (sql.NullInt64{Int64: 500, Valid: true}) {
		t.Fatal("later read-through should cover an unarchived unread notice", read)
	}
}

func TestUONoticeAckArchivePreservesEarlierRead(t *testing.T) {
	w := uoNoticeAckFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 100, uoNoticeAckOp(uoNoticeReadThrough)))
	out := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 200, uoNoticeAckOp(uoNoticeArchive, a)))
	if !out.outcomes[0].changed || out.outcomes[0].readMS != (sql.NullInt64{Int64: 100, Valid: true}) || out.outcomes[0].archivedMS != (sql.NullInt64{Int64: 200, Valid: true}) {
		t.Fatal("archive replaced an earlier covering read", out)
	}
	uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 300, uoNoticeAckOp(uoNoticeReadThrough)))
	read, archive := uoNoticeAckEffective(t, w, uoNoticeAckPrincipal(), a)
	if read != (sql.NullInt64{Int64: 100, Valid: true}) || archive != (sql.NullInt64{Int64: 200, Valid: true}) {
		t.Fatal("archive replaced earlier covering read", read, archive)
	}
	// Re-archive after an explicit unread marks it read now and keeps the first
	// archived time.
	uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 400, uoNoticeAckOp(uoNoticeUnread, a)))
	out = uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 500, uoNoticeAckOp(uoNoticeArchive, a)))
	if out.outcomes[0].readMS != (sql.NullInt64{Int64: 500, Valid: true}) || out.outcomes[0].archivedMS != (sql.NullInt64{Int64: 200, Valid: true}) {
		t.Fatal("re-archive failed to mark read", out)
	}
}

func TestUONoticeAckIdempotentAndMonotonic(t *testing.T) {
	w := uoNoticeAckFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	first := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 100, uoNoticeAckOp(uoNoticeRead, a)))
	if !first.outcomes[0].changed {
		t.Fatal("first read", first)
	}
	events := uoNoticeAckEventCount(t, w)
	// Repeating a read on a read notice writes nothing and emits no hint.
	again := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 150, uoNoticeAckOp(uoNoticeRead, a)))
	if again.changed != 0 || again.outcomes[0].changed || again.outcomes[0].readMS != (sql.NullInt64{Int64: 100, Valid: true}) || again.event != (wcAPIEventEffect{}) {
		t.Fatal("repeated read was not a no-op", again)
	}
	if eventsAfter := uoNoticeAckEventCount(t, w); eventsAfter != events {
		t.Fatal("no-op read emitted a hint", eventsAfter, events)
	}
	if revision, read, _ := uoNoticeAckStored(t, w, uoNoticeAckPrincipal(), a.sequence); revision != first.outcomes[0].revision || read != (sql.NullInt64{Int64: 100, Valid: true}) {
		t.Fatal("repeated read rewrote the row", revision, read)
	}
	archive := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 200, uoNoticeAckOp(uoNoticeArchive, a)))
	if !archive.outcomes[0].changed {
		t.Fatal("archive", archive)
	}
	rearchive := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 250, uoNoticeAckOp(uoNoticeArchive, a)))
	if rearchive.changed != 0 || rearchive.outcomes[0].archivedMS != (sql.NullInt64{Int64: 200, Valid: true}) {
		t.Fatal("repeated archive moved its own time", rearchive)
	}
	// A read-through never regresses its boundary or its accepted time, even
	// when a later command presents an earlier clock and a newer record.
	b := uoNoticePublishOne(t, w, uoNoticeDraft("b"))
	uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 400, uoNoticeAckOp(uoNoticeReadThrough)))
	sequence, _, readMS, rows := uoNoticeAckHeadStored(t, w, uoNoticeAckPrincipal())
	if sequence != b.sequence || readMS != 400 || rows != 1 {
		t.Fatal("read-through head before regression check", sequence, readMS, rows)
	}
	c := uoNoticePublishOne(t, w, uoNoticeDraft("c"))
	uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 300, uoNoticeAckOp(uoNoticeReadThrough)))
	sequence, _, readMS, rows = uoNoticeAckHeadStored(t, w, uoNoticeAckPrincipal())
	if sequence != c.sequence || readMS != 400 || rows != 1 {
		t.Fatal("read-through regressed", sequence, readMS, rows)
	}
}

func TestUONoticeAckPrincipalIsolation(t *testing.T) {
	w := uoNoticeAckFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	other := uoNoticeDraft("b")
	other.view = uoNoticeView{scope: `["authority","account-one","profile-two"]`, audience: uoNoticeAudienceProfile}
	foreign := uoNoticePublishOne(t, w, other)
	const second = "principal:two:shared-admins"
	if out := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 100, uoNoticeAckOp(uoNoticeRead, a))); !out.outcomes[0].changed {
		t.Fatal("first principal read", out)
	}
	// The first principal's rows are invisible to the second: no individual
	// row and no head exist for it, and the oracle reports unread.
	if revision, _, _ := uoNoticeAckStored(t, w, second, a.sequence); revision != 0 {
		t.Fatal("another principal's acknowledgement was visible", revision)
	}
	if _, _, _, rows := uoNoticeAckHeadStored(t, w, second); rows != 0 {
		t.Fatal("another principal's head was visible", rows)
	}
	if read, archive := uoNoticeAckEffective(t, w, second, a); read.Valid || archive.Valid {
		t.Fatal("another principal read state leaked", read, archive)
	}
	firstRevision, firstRead, _ := uoNoticeAckStored(t, w, uoNoticeAckPrincipal(), a.sequence)
	// The second principal's own read creates only its own row.
	out := uoNoticeAckOne(t, w, uoNoticeAckRequest(second, 200, uoNoticeAckOp(uoNoticeRead, a)))
	if !out.outcomes[0].changed || out.outcomes[0].readMS != (sql.NullInt64{Int64: 200, Valid: true}) {
		t.Fatal("second principal read", out)
	}
	if revision, read, _ := uoNoticeAckStored(t, w, uoNoticeAckPrincipal(), a.sequence); revision != firstRevision || read != firstRead {
		t.Fatal("second principal rewrote the first principal's row", revision, read)
	}
	// A read-through by the second principal does not cover the first.
	uoNoticeAckOne(t, w, uoNoticeAckRequest(second, 300, uoNoticeAckOp(uoNoticeReadThrough)))
	if _, _, _, rows := uoNoticeAckHeadStored(t, w, uoNoticeAckPrincipal()); rows != 0 {
		t.Fatal("another principal's read-through created a head", rows)
	}
	// A notice in another inbox is not an actionable target, even by identifier.
	events := uoNoticeAckEventCount(t, w)
	missing := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 400, uoNoticeAckOp(uoNoticeRead, foreign)))
	if !missing.outcomes[0].notFound || missing.changed != 0 || missing.event != (wcAPIEventEffect{}) {
		t.Fatal("foreign inbox notice acknowledged", missing)
	}
	if eventsAfter := uoNoticeAckEventCount(t, w); eventsAfter != events {
		t.Fatal("not-found acknowledgement emitted a hint", eventsAfter, events)
	}
	// Withdrawn and expired notices are equally absent.
	if out := uoNoticeWithdrawOne(t, w, uoNoticeWithdraw{view: uoNoticeDraft("a").view, id: a.id, expectedRevision: a.revision, authorityRevision: 1, nowMS: 500}); !out.changed {
		t.Fatal("withdraw", out)
	}
	expired := uoNoticeDraft("d")
	expired.expiresMS = 100
	expiredNotice := uoNoticePublishOne(t, w, expired)
	for _, notice := range []uoNoticeEffect{a, expiredNotice} {
		if out := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 600, uoNoticeAckOp(uoNoticeRead, notice))); !out.outcomes[0].notFound || out.changed != 0 {
			t.Fatal("withdrawn/expired notice acknowledged", notice.id, out)
		}
	}
}

func TestUONoticeAckRestatementResetsReadState(t *testing.T) {
	w := uoNoticeAckFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	read := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 50, uoNoticeAckOp(uoNoticeRead, a)))
	restate := uoNoticeDraft("a")
	restate.expectedRevision, restate.expectedAuthorityRevision = a.revision, a.authorityRevision
	restate.title = "Storage recovered"
	restate.nowMS = 100
	restated := uoNoticePublishOne(t, w, restate)
	if restated.revision <= a.revision || restated.sequence != a.sequence {
		t.Fatal("not a restatement", restated)
	}
	// The old witness is refused rather than silently applied to the new record.
	turn := wc008NativeTurn(t, w, AcknowledgeNotices, uoNoticeAckDefs())
	if out, err := applyUONoticeAcks(t.Context(), turn, uoNoticeAckRequest(uoNoticeAckPrincipal(), 150, uoNoticeAckOp(uoNoticeRead, a))); !errors.Is(err, ErrConflict) || !uoNoticeAckIsZero(out) {
		t.Fatal("stale witness acknowledged a restatement", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	// The stale row is still stored but no longer current, so the oracle reads
	// unread until an acknowledgement names the new revision.
	if revision, _, _ := uoNoticeAckStored(t, w, uoNoticeAckPrincipal(), a.sequence); revision != read.outcomes[0].revision || revision >= restated.revision {
		t.Fatal("test witness", revision, restated.revision)
	}
	if readMS, archive := uoNoticeAckEffective(t, w, uoNoticeAckPrincipal(), restated); readMS.Valid || archive.Valid {
		t.Fatal("restatement retained stale acknowledgement state", readMS, archive)
	}
	updated := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 200, uoNoticeAckOp(uoNoticeRead, restated)))
	if !updated.outcomes[0].changed || updated.outcomes[0].readMS != (sql.NullInt64{Int64: 200, Valid: true}) {
		t.Fatal("restated notice read", updated)
	}
	if readMS, _ := uoNoticeAckEffective(t, w, uoNoticeAckPrincipal(), restated); readMS != (sql.NullInt64{Int64: 200, Valid: true}) {
		t.Fatal("fresh acknowledgement not effective", readMS)
	}
}

func TestUONoticeAckZeroHeadResync(t *testing.T) {
	w := uoNoticeAckFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	// A raw zero-sequence head with a zero time is a normalized empty head.
	wc008NativeExec(t, w, `INSERT INTO notification_ack_heads(principal_key) VALUES(?)`, uoNoticeAckPrincipal())
	if out := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 100, uoNoticeAckOp(uoNoticeRead, a))); !out.outcomes[0].changed {
		t.Fatal("empty head blocked a normal read", out)
	}
	// A positive boundary with a zero accepted time cannot invent an epoch
	// read time: both the scalar and an individual action require a resync.
	wc008NativeExec(t, w, `UPDATE notification_ack_heads SET read_through_sequence=?,read_through_ms=0 WHERE principal_key=?`, a.sequence, uoNoticeAckPrincipal())
	for _, op := range []uoNoticeAckOperation{uoNoticeAckOp(uoNoticeReadThrough), uoNoticeAckOp(uoNoticeRead, a)} {
		turn := wc008NativeTurn(t, w, AcknowledgeNotices, uoNoticeAckDefs())
		out, err := applyUONoticeAcks(t.Context(), turn, uoNoticeAckRequest(uoNoticeAckPrincipal(), 200, op))
		if !errors.Is(err, ErrResolveRequired) || !uoNoticeAckIsZero(out) {
			t.Fatal("invented an epoch read time", out, err)
		}
		if _, err = turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
	// A zero boundary carrying a positive time is coverage with no boundary.
	wc008NativeExec(t, w, `UPDATE notification_ack_heads SET read_through_sequence=0,read_through_ms=50 WHERE principal_key=?`, uoNoticeAckPrincipal())
	turn := wc008NativeTurn(t, w, AcknowledgeNotices, uoNoticeAckDefs())
	if out, err := applyUONoticeAcks(t.Context(), turn, uoNoticeAckRequest(uoNoticeAckPrincipal(), 200, uoNoticeAckOp(uoNoticeReadThrough))); !errors.Is(err, ErrResolveRequired) || !uoNoticeAckIsZero(out) {
		t.Fatal("boundary-less coverage accepted", out, err)
	}
}

func TestUONoticeAckBatchBounds(t *testing.T) {
	base := uoNoticeAckRequest(uoNoticeAckPrincipal(), 1, uoNoticeAckOp(uoNoticeReadThrough))
	nine := make([]uoNoticeAckOperation, 9)
	for i := range nine {
		nine[i] = uoNoticeAckOp(uoNoticeRead, uoNoticeEffect{id: fmt.Sprintf("n%02d", i), revision: 1, authorityRevision: 1})
	}
	wide := uoNoticeAckOperation{action: uoNoticeRead}
	for i := 0; i <= uoNoticeMaxAckTargets; i++ {
		wide.targets = append(wide.targets, uoNoticeAckTarget{fmt.Sprintf("n%03d", i), 1, 1})
	}
	for _, edit := range []func(*uoNoticeAckInput){
		func(q *uoNoticeAckInput) { q.view.audience = "everyone" },
		func(q *uoNoticeAckInput) { q.view.scope = "" },
		func(q *uoNoticeAckInput) { q.principal = "" },
		func(q *uoNoticeAckInput) { q.principal = strings.Repeat("p", uoNoticeMaxPrincipalBytes+1) },
		func(q *uoNoticeAckInput) { q.nowMS = 0 },
		func(q *uoNoticeAckInput) { q.operations = nil },
		func(q *uoNoticeAckInput) { q.operations = nine },
		func(q *uoNoticeAckInput) { q.operations = []uoNoticeAckOperation{wide} },
		func(q *uoNoticeAckInput) { q.operations[0] = uoNoticeAckOperation{action: uoNoticeRead} },
		func(q *uoNoticeAckInput) {
			q.operations[0] = uoNoticeAckOperation{action: uoNoticeReadThrough, through: -1}
		},
		func(q *uoNoticeAckInput) {
			q.operations[0] = uoNoticeAckOperation{action: uoNoticeReadThrough, targets: []uoNoticeAckTarget{{"a", 1, 1}}}
		},
		func(q *uoNoticeAckInput) {
			q.operations[0] = uoNoticeAckOperation{action: uoNoticeRead, through: 1, targets: []uoNoticeAckTarget{{"a", 1, 1}}}
		},
		func(q *uoNoticeAckInput) {
			q.operations[0] = uoNoticeAckOperation{action: uoNoticeRead, targets: []uoNoticeAckTarget{{"bad id", 1, 1}}}
		},
		func(q *uoNoticeAckInput) {
			q.operations[0] = uoNoticeAckOperation{action: uoNoticeRead, targets: []uoNoticeAckTarget{{"a", 0, 1}}}
		},
		func(q *uoNoticeAckInput) {
			q.operations[0] = uoNoticeAckOperation{action: uoNoticeRead, targets: []uoNoticeAckTarget{{"a", 1, 0}}}
		},
		func(q *uoNoticeAckInput) { q.operations[0] = uoNoticeAckOperation{action: uoNoticeAction(9)} },
	} {
		input := base
		input.operations = append([]uoNoticeAckOperation(nil), base.operations...)
		edit(&input)
		if err := input.validate(); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted invalid acknowledgement input", input)
		}
	}
	// The component refuses a nil turn rather than writing without custody.
	if out, err := applyUONoticeAcks(t.Context(), nil, base); err == nil || !uoNoticeAckIsZero(out) {
		t.Fatal("missing turn acknowledged", out, err)
	}
	w := uoNoticeAckFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	// A boundary beyond the newest record would cover a notice that does not
	// exist yet.
	turn := wc008NativeTurn(t, w, AcknowledgeNotices, uoNoticeAckDefs())
	if out, err := applyUONoticeAcks(t.Context(), turn, uoNoticeAckRequest(uoNoticeAckPrincipal(), 100, uoNoticeAckOperation{action: uoNoticeReadThrough, through: a.sequence + 1})); !errors.Is(err, ErrConflict) || !uoNoticeAckIsZero(out) {
		t.Fatal("future boundary accepted", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	// A bounded scalar covers only the named prefix.
	second := uoNoticePublishOne(t, w, uoNoticeDraft("b"))
	if out := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 200, uoNoticeAckOperation{action: uoNoticeReadThrough, through: a.sequence})); out.outcomes[0].through != a.sequence || !out.outcomes[0].changed {
		t.Fatal("bounded read-through", out)
	}
	readA, _ := uoNoticeAckEffective(t, w, uoNoticeAckPrincipal(), a)
	readB, _ := uoNoticeAckEffective(t, w, uoNoticeAckPrincipal(), second)
	if readA != (sql.NullInt64{Int64: 200, Valid: true}) || readB.Valid {
		t.Fatal("bounded read-through covered past its scalar", readA, readB)
	}
}

func TestUONoticeAckRollbackAfterPrefix(t *testing.T) {
	for _, failure := range []string{"conflict", "event", "revision-overflow", "cancel", "missing-turn"} {
		t.Run(failure, func(t *testing.T) {
			w := uoNoticeAckFixture(t)
			a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
			b := uoNoticePublishOne(t, w, uoNoticeDraft("b"))
			records, revisions, inboxRevision := uoNoticeAckNoticeFacts(t, w)
			events := uoNoticeAckEventCount(t, w)
			defs := uoNoticeAckDefs()
			if failure == "event" {
				defs = uoNoticeAckDefinitions()
			}
			turn := wc008NativeTurn(t, w, AcknowledgeNotices, defs)
			if failure == "revision-overflow" {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO notification_revision(singleton,value) VALUES(1,?)`, int64(math.MaxInt64)); err != nil {
					t.Fatal(err)
				}
			}
			stale := b
			if failure == "conflict" {
				stale.authorityRevision++
			}
			operations := []uoNoticeAckOperation{uoNoticeAckOp(uoNoticeRead, a), uoNoticeAckOp(uoNoticeArchive, stale)}
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
			out, err := applyUONoticeAcks(ctx, target, uoNoticeAckRequest(uoNoticeAckPrincipal(), 100, operations...))
			if err == nil || !uoNoticeAckIsZero(out) {
				t.Fatal("failure leaked a result", out, err)
			}
			if turn != nil {
				if _, err = turn.transaction.rollback(nil); err != nil {
					t.Fatal(err)
				}
			}
			// The accepted first action of the prefix left nothing behind.
			var acks, heads, revisionRows int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM notification_acknowledgments),(SELECT count(*) FROM notification_ack_heads),(SELECT COALESCE(MAX(value),0) FROM notification_revision)`).Scan(&acks, &heads, &revisionRows); err != nil || acks != 0 || heads != 0 || revisionRows != b.revision {
				t.Fatal("failed turn left a prefix", acks, heads, revisionRows, err)
			}
			if gotRecords, gotRevisions, gotInbox := uoNoticeAckNoticeFacts(t, w); gotRecords != records || gotRevisions != revisions || gotInbox != inboxRevision {
				t.Fatal("failed turn touched shared notice state", gotRecords, gotRevisions, gotInbox)
			}
			if gotEvents := uoNoticeAckEventCount(t, w); gotEvents != events {
				t.Fatal("failed turn appended a hint", gotEvents, events)
			}
		})
	}
}

func TestUONoticeAckRootSeamRefuses(t *testing.T) {
	w := uoNoticeAckFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	out := uoNoticeAckOne(t, w, uoNoticeAckRequest(uoNoticeAckPrincipal(), 100, uoNoticeAckOp(uoNoticeRead, a)))
	binding, err := bindUONoticeAckRoot(out)
	if !errors.Is(err, ErrNoticeRootUnavailable) || binding != (uoNoticeRootBinding{}) {
		t.Fatal("root seam must refuse until UO-01/UO-03 land", binding, err)
	}
	var acks int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM notification_acknowledgments`).Scan(&acks); err != nil || acks != 1 {
		t.Fatal("root refusal wrote native state", acks, err)
	}
}

func TestUONoticeAckDefinitionsPrepare(t *testing.T) {
	w := uoNoticeAckFixture(t)
	wc008NativePrepare(t, w, uoNoticeAckDefinitions())
	wc008NativePrepare(t, w, wcAPIEventDefinitions())
}
