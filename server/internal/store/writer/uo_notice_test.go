package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"testing"
)

func uoNoticeFixture(t *testing.T) *executor {
	t.Helper()
	w := wc008NativeExecutor(t)
	// DRAFT UO-001 only: never alter embedded/canonical schema or templates.
	wc008NativeExec(t, w, `ALTER TABLE notification_ack_heads ADD COLUMN read_through_ms INTEGER NOT NULL DEFAULT 0 CHECK(read_through_ms>=0)`)
	return w
}
func uoNoticeTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	defs := append(uoNoticeDefinitions(), wcAPIEventDefinitions()...)
	// Synthetic component custody ONLY. No UO dispatch/authority/receipt or
	// prospective forecasts/qualification are provided by this test fixture.
	return wc008NativeTurn(t, w, ProgressAccept, defs)
}
func uoNoticeCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}
func uoNoticeDraft(id string) uoNoticePublish {
	return uoNoticePublish{view: uoNoticeView{"shared:admins", "server-admin"}, id: id, severity: "warning", source: "storage", category: "storage.low", title: "Storage low", body: "Free space is low", dedupe: id, nowMS: 10, expiresMS: 10000, authorityRevision: 1}
}
func uoNoticePublishOne(t *testing.T, w *executor, q uoNoticePublish) uoNoticeEffect {
	t.Helper()
	turn := uoNoticeTurn(t, w)
	out, err := applyUONoticePublish(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	uoNoticeCommit(t, turn)
	return out
}
func uoNoticeActionOne(t *testing.T, w *executor, now int64, ops ...uoNoticeAckOperation) uoNoticeAckEffect {
	t.Helper()
	turn := uoNoticeTurn(t, w)
	out, err := applyUONoticeAcks(t.Context(), turn, uoNoticeAckInput{view: uoNoticeDraft("a").view, principal: "principal:one:shared-admins", nowMS: now, operations: ops})
	if err != nil {
		t.Fatal(err)
	}
	uoNoticeCommit(t, turn)
	return out
}
func uoNoticeOp(action uoNoticeAction, notices ...uoNoticeEffect) uoNoticeAckOperation {
	op := uoNoticeAckOperation{action: action}
	for _, n := range notices {
		op.targets = append(op.targets, uoNoticeAckTarget{n.id, n.revision, n.authorityRevision})
	}
	return op
}
func uoNoticeReadState(t *testing.T, w *executor, n uoNoticeEffect) (sql.NullInt64, sql.NullInt64) {
	t.Helper()
	var a uoNoticeAckState
	err := w.db.QueryRowContext(t.Context(), `SELECT revision,read_ms,archived_ms FROM notification_acknowledgments WHERE principal_key=? AND notice_sequence=?`, "principal:one:shared-admins", n.sequence).Scan(&a.revision, &a.readMS, &a.archivedMS)
	if err == nil {
		a.exists = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	var b uoNoticeBulkState
	err = w.db.QueryRowContext(t.Context(), `SELECT read_through_sequence,revision,read_through_ms FROM notification_ack_heads WHERE principal_key=?`, "principal:one:shared-admins").Scan(&b.sequence, &b.revision, &b.readMS)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	read, archive, err := resolveUONativeAck(n.sequence, n.revision, a, b)
	if err != nil {
		t.Fatal(err)
	}
	return read, archive
}
func TestUONoticeNativeTwoBulkAndRestatement(t *testing.T) {
	w := uoNoticeFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	c := uoNoticePublishOne(t, w, uoNoticeDraft("c"))
	uoNoticeActionOne(t, w, 50, uoNoticeOp(uoNoticeRead, c))
	uoNoticeActionOne(t, w, 100, uoNoticeOp(uoNoticeReadAll))
	b := uoNoticePublishOne(t, w, uoNoticeDraft("b"))
	uoNoticeActionOne(t, w, 200, uoNoticeOp(uoNoticeReadAll))
	for _, test := range []struct {
		n  uoNoticeEffect
		ms int64
	}{{a, 200}, {b, 200}, {c, 50}} {
		read, _ := uoNoticeReadState(t, w, test.n)
		if !read.Valid || read.Int64 != test.ms {
			t.Fatal("covering bulk/individual time", test.n.id, read, test.ms)
		}
	}
	uoNoticeActionOne(t, w, 250, uoNoticeOp(uoNoticeUnread, a))
	read, _ := uoNoticeReadState(t, w, a)
	if read.Valid {
		t.Fatal("unread lost", read)
	}
	uoNoticeActionOne(t, w, 260, uoNoticeOp(uoNoticeRead, b)) // no-op; must not freeze bulk time
	uoNoticeActionOne(t, w, 300, uoNoticeOp(uoNoticeReadAll))
	read, _ = uoNoticeReadState(t, w, b)
	if read.Int64 != 300 {
		t.Fatal("no-op read froze bulk-only timestamp", read)
	}
	q := uoNoticeDraft("c")
	q.expectedRevision = c.revision
	q.nowMS = 400
	q.title = "Storage recovered"
	restated := uoNoticePublishOne(t, w, q)
	if restated.created || restated.sequence != c.sequence || restated.revision <= c.revision {
		t.Fatal("not a restatement", restated)
	}
	read, archive := uoNoticeReadState(t, w, restated)
	if read.Valid || archive.Valid {
		t.Fatal("restatement retained stale ACK", read, archive)
	}
	var records, acks, heads, events int
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM notification_records),(SELECT count(*) FROM notification_acknowledgments),(SELECT count(*) FROM notification_ack_heads),(SELECT count(*) FROM api_events)`).Scan(&records, &acks, &heads, &events); err != nil || records != 3 || acks != 2 || heads != 1 || events != 9 {
		t.Fatal("shared records/sparse ACK/event count", records, acks, heads, events, err)
	}
}
func TestUONoticeNativeArchiveUnreadReadAll(t *testing.T) {
	w := uoNoticeFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	uoNoticeActionOne(t, w, 100, uoNoticeOp(uoNoticeArchive, a))
	uoNoticeActionOne(t, w, 200, uoNoticeOp(uoNoticeUnread, a))
	uoNoticeActionOne(t, w, 300, uoNoticeOp(uoNoticeReadAll))
	read, archive := uoNoticeReadState(t, w, a)
	if read.Valid || !archive.Valid || archive.Int64 != 100 {
		t.Fatal("read-all reached archived unread", read, archive)
	}
	uoNoticeActionOne(t, w, 400, uoNoticeOp(uoNoticeUnarchive, a))
	read, archive = uoNoticeReadState(t, w, a)
	if read.Valid || archive.Valid {
		t.Fatal("unarchive resurrected excluded bulk", read, archive)
	}
	uoNoticeActionOne(t, w, 500, uoNoticeOp(uoNoticeReadAll))
	read, _ = uoNoticeReadState(t, w, a)
	if !read.Valid || read.Int64 != 500 {
		t.Fatal("subsequent bulk should cover unarchived unread", read)
	}
}

func TestUONoticeNativeArchivePreservesEarlierReadTime(t *testing.T) {
	w := uoNoticeFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	uoNoticeActionOne(t, w, 100, uoNoticeOp(uoNoticeReadAll))
	uoNoticeActionOne(t, w, 200, uoNoticeOp(uoNoticeArchive, a))
	uoNoticeActionOne(t, w, 300, uoNoticeOp(uoNoticeReadAll))
	read, archive := uoNoticeReadState(t, w, a)
	if !read.Valid || read.Int64 != 100 || !archive.Valid || archive.Int64 != 200 {
		t.Fatal("archive replaced earlier covering read", read, archive)
	}
	uoNoticeActionOne(t, w, 400, uoNoticeOp(uoNoticeUnread, a))
	uoNoticeActionOne(t, w, 500, uoNoticeOp(uoNoticeArchive, a))
	read, archive = uoNoticeReadState(t, w, a)
	if !read.Valid || read.Int64 != 500 || !archive.Valid || archive.Int64 != 200 {
		t.Fatal("re-archive failed to mark read", read, archive)
	}
}
func TestUONoticeNativeOrderedEightOperationRange(t *testing.T) {
	w := uoNoticeFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	// Operations must retain public order; a single batch ordinal would lose
	// read-all -> unread and the reverse when comparing sparse/barrier state.
	ops := []uoNoticeAckOperation{uoNoticeOp(uoNoticeReadAll), uoNoticeOp(uoNoticeUnread, a), uoNoticeOp(uoNoticeReadAll), uoNoticeOp(uoNoticeUnread, a), uoNoticeOp(uoNoticeArchive, a), uoNoticeOp(uoNoticeUnread, a), uoNoticeOp(uoNoticeReadAll), uoNoticeOp(uoNoticeUnarchive, a)}
	out := uoNoticeActionOne(t, w, 100, ops...)
	if out.revision != a.revision+8 || len(out.outcomes) != 8 {
		t.Fatal("not one bounded ordered range", out)
	}
	for i, result := range out.outcomes {
		if result.revision != a.revision+int64(i)+1 {
			t.Fatal("operation order collapsed", out)
		}
	}
	read, archive := uoNoticeReadState(t, w, a)
	if read.Valid || archive.Valid {
		t.Fatal("archive/unread/read-all/unarchive order", read, archive)
	}
	uoNoticeActionOne(t, w, 200, uoNoticeOp(uoNoticeUnread, a), uoNoticeOp(uoNoticeReadAll))
	read, _ = uoNoticeReadState(t, w, a)
	if !read.Valid || read.Int64 != 200 {
		t.Fatal("later read-all lost", read)
	}
	// Number of revision allocations is observed with a test trigger solely on
	// this disposable fixture, not installed in canonical/native application DDL.
	wc008NativeExec(t, w, `CREATE TABLE uo_test_allocations(n INTEGER)`)
	wc008NativeExec(t, w, `CREATE TRIGGER uo_test_allocations AFTER UPDATE ON notification_revision BEGIN INSERT INTO uo_test_allocations VALUES(new.value-old.value); END`)
	uoNoticeActionOne(t, w, 300, ops...)
	var count, width int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*),max(n) FROM uo_test_allocations`).Scan(&count, &width); err != nil || count != 1 || width != 8 {
		t.Fatal("range not reserved once", count, width, err)
	}
}
func TestUONoticeNativeWithdrawAndScopeIsolation(t *testing.T) {
	w := uoNoticeFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	turn := uoNoticeTurn(t, w)
	out, err := applyUONoticeWithdraw(t.Context(), turn, uoNoticeWithdraw{view: uoNoticeDraft("a").view, id: a.id, expectedRevision: a.revision, authorityRevision: 1, nowMS: 100})
	if err != nil || !out.changed {
		t.Fatal(out, err)
	}
	uoNoticeCommit(t, turn)
	ack := uoNoticeActionOne(t, w, 200, uoNoticeOp(uoNoticeRead, out))
	if len(ack.outcomes) != 1 || !ack.outcomes[0].notFound || ack.changed != 0 {
		t.Fatal("withdrawn notice acknowledged", ack)
	}
	turn = uoNoticeTurn(t, w)
	q := uoNoticeAckInput{view: uoNoticeView{"wrong:scope", "server-admin"}, principal: "principal:other", nowMS: 300, operations: []uoNoticeAckOperation{uoNoticeOp(uoNoticeRead, a)}}
	result, err := applyUONoticeAcks(t.Context(), turn, q)
	if err != nil || !result.outcomes[0].notFound {
		t.Fatal(result, err)
	}
	uoNoticeCommit(t, turn)
	qpub := uoNoticeDraft("a")
	qpub.expectedRevision = out.revision
	qpub.nowMS = 400
	restated := uoNoticePublishOne(t, w, qpub)
	read, archive := uoNoticeReadState(t, w, restated)
	if read.Valid || archive.Valid {
		t.Fatal("reinstated notice inherited stale state")
	}
}
func TestUONoticeNativeEmptyAndUninitializedBulkHeads(t *testing.T) {
	w := uoNoticeFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	wc008NativeExec(t, w, `INSERT INTO notification_ack_heads(principal_key) VALUES('principal:one:shared-admins')`)
	uoNoticeActionOne(t, w, 100, uoNoticeOp(uoNoticeRead, a))
	wc008NativeExec(t, w, `UPDATE notification_ack_heads SET read_through_sequence=?,read_through_ms=0`, a.sequence)
	turn := uoNoticeTurn(t, w)
	out, err := applyUONoticeAcks(t.Context(), turn, uoNoticeAckInput{view: uoNoticeDraft("a").view, principal: "principal:one:shared-admins", nowMS: 200, operations: []uoNoticeAckOperation{uoNoticeOp(uoNoticeReadAll)}})
	if !errors.Is(err, ErrResolveRequired) || out.revision != 0 || out.outcomes != nil {
		t.Fatal("invented epoch read time", out, err)
	}
}
func TestUONoticeNativeFailureRollsBackWholeTurn(t *testing.T) {
	for _, failure := range []string{"event", "authority", "revision-overflow", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			w := uoNoticeFixture(t)
			q := uoNoticeDraft("a")
			var turn *commandTurn
			if failure == "event" {
				turn = wc008NativeTurn(t, w, ProgressAccept, uoNoticeDefinitions())
			} else {
				turn = uoNoticeTurn(t, w)
			}
			if failure == "revision-overflow" { // Native setup inside this disposable transaction.
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `INSERT INTO notification_revision VALUES(1,?)`, int64(math.MaxInt64)); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "authority" {
				q.expectedRevision = 1
			}
			ctx := t.Context()
			if failure == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			out, err := applyUONoticePublish(ctx, turn, q)
			if err == nil || out != (uoNoticeEffect{}) {
				t.Fatal("error leaked prefix", out, err)
			}
			if _, err = turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM notification_records)+(SELECT count(*) FROM notification_inbox)+(SELECT count(*) FROM notification_revision)+(SELECT count(*) FROM api_events)`).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed turn left prefix", count, err)
			}
		})
	}
}
func TestUONoticeNativeBoundedReclaim(t *testing.T) {
	w := uoNoticeFixture(t)
	q := uoNoticeDraft("a")
	q.expiresMS = 100
	q.operation = uoNoticeOperation{id: "broadcast-one", digest: [32]byte{1}, expiresMS: 100}
	a := uoNoticePublishOne(t, w, q)
	for i := range 35 {
		wc008NativeExec(t, w, `INSERT INTO notification_acknowledgments(principal_key,notice_sequence,revision) VALUES(?,?,?)`, fmt.Sprintf("p%03d", i), a.sequence, a.revision+1)
	}
	for i, want := range []struct {
		acks, notices int
		pending       bool
	}{{16, 0, true}, {16, 0, true}, {3, 1, false}} {
		turn := uoNoticeTurn(t, w)
		out, err := reclaimUONotices(t.Context(), turn, 100)
		if err != nil || out.ackRows != want.acks || out.noticeRows != want.notices || out.noticePending != want.pending || out.operationRows != map[bool]int{true: 1, false: 0}[i == 0] {
			t.Fatal("bounded reclaim", i, out, err)
		}
		uoNoticeCommit(t, turn)
	}
	var count int
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM notification_records)+(SELECT count(*) FROM notification_acknowledgments)+(SELECT count(*) FROM notice_broadcast_work)`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestUONoticeNativeDefinitionsPrepare(t *testing.T) {
	w := uoNoticeFixture(t)
	wc008NativePrepare(t, w, uoNoticeDefinitions())
}
func TestUONoticeNativeInputBounds(t *testing.T) {
	q := uoNoticeAckInput{view: uoNoticeDraft("a").view, principal: "p", nowMS: 1, operations: []uoNoticeAckOperation{uoNoticeOp(uoNoticeReadAll)}}
	for _, edit := range []func(*uoNoticeAckInput){func(q *uoNoticeAckInput) { q.nowMS = 0 }, func(q *uoNoticeAckInput) { q.operations = make([]uoNoticeAckOperation, 9) }, func(q *uoNoticeAckInput) { q.operations[0].targets = []uoNoticeAckTarget{{"a", 1, 1}} }, func(q *uoNoticeAckInput) { q.principal = "" }} {
		copy := q
		copy.operations = append([]uoNoticeAckOperation(nil), q.operations...)
		edit(&copy)
		if !errors.Is(copy.validate(), ErrInvalid) {
			t.Fatal("accepted invalid input", copy)
		}
	}
	out, err := applyUONoticePublish(t.Context(), nil, uoNoticeDraft("a"))
	if err == nil || out != (uoNoticeEffect{}) {
		t.Fatal(out, err)
	}
}

func TestUONoticeNativeAckPrefixConflictRollsBack(t *testing.T) {
	w := uoNoticeFixture(t)
	a := uoNoticePublishOne(t, w, uoNoticeDraft("a"))
	b := uoNoticePublishOne(t, w, uoNoticeDraft("b"))
	turn := uoNoticeTurn(t, w)
	stale := b
	stale.authorityRevision++
	q := uoNoticeAckInput{view: uoNoticeDraft("a").view, principal: "principal:one:shared-admins", nowMS: 100, operations: []uoNoticeAckOperation{uoNoticeOp(uoNoticeRead, a), uoNoticeOp(uoNoticeArchive, stale)}}
	out, err := applyUONoticeAcks(t.Context(), turn, q)
	if !errors.Is(err, ErrConflict) || out.outcomes != nil || out.revision != 0 || out.changed != 0 {
		t.Fatal("stale authority leaked accepted prefix", out, err)
	}
	if _, err = turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var acks, events int
	var revision int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM notification_acknowledgments),(SELECT count(*) FROM api_events),(SELECT value FROM notification_revision WHERE singleton=1)`).Scan(&acks, &events, &revision); err != nil || acks != 0 || events != 2 || revision != b.revision {
		t.Fatal("conflict committed prefix", acks, events, revision, err)
	}
}
