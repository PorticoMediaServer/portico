package writer

import (
	"database/sql"
	"testing"
)

// Prepare a second native report after a committed capture and drain. ACK here
// is fixture setup; actual authority, ACK and scheduling remain W-owned.
func wcBoundaryNextReport(t *testing.T, w *executor, q wc008CaptureReport) wc008CaptureReport {
	t.Helper()
	turn := wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
	e, err := applyWC008Capture(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	n, err := storeWC008CaptureResult(t.Context(), turn, q, e, RequestDigest{20}, ResultDigest{21})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE playback_personal_claims SET command_acknowledged_sequence=command_last_sequence`)
	p := q.source
	p.startEventID = e.startEventID
	p.lastAcceptedSequence = p.proof.reportSequence
	p.last, p.acknowledged = 1, 1
	p.identity.Sequence = 2
	p.identity.OperationID[0]++
	p.receiptRows, p.receiptVariableBytes = 1, n
	if err := w.db.QueryRowContext(t.Context(), `SELECT control_variable_bytes FROM playback_personal_claims`).Scan(&p.oldControlBytes); err != nil {
		t.Fatal(err)
	}
	turn = wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
	drain, err := applyWC008ProgressMetrics(t.Context(), turn, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storeWC008ProgressResult(t.Context(), turn, p, drain, RequestDigest{22}, ResultDigest{23}); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE playback_personal_claims SET command_acknowledged_sequence=command_last_sequence`)
	p.last, p.acknowledged, p.identity.Sequence = 2, 2, 3
	p.identity.OperationID[0]++
	p.proof.reportSequence++
	p.fromMS, p.cursorMS, p.untilMS = 1000, 1000, 2000
	p.appliedMS, p.appliedAt = 2000, "1970-01-01T00:00:02Z"
	if err := w.db.QueryRowContext(t.Context(), `SELECT control_variable_bytes,receipt_rows,receipt_variable_bytes FROM playback_personal_claims`).Scan(&p.oldControlBytes, &p.receiptRows, &p.receiptVariableBytes); err != nil {
		t.Fatal(err)
	}
	q.source = p
	return q
}

func TestWCProgressReportObservationBoundaries(t *testing.T) {
	for _, name := range []string{"early_ended", "repeated_completion"} {
		t.Run(name, func(t *testing.T) {
			w := wc008NativeExecutor(t)
			q := wcCaptureFixture(t, w)
			if name == "repeated_completion" {
				q.positionSeconds, q.completed = 95, true
				q.original.values[21] = int64(95000)
			}
			q = wcBoundaryNextReport(t, w, q)
			if name == "early_ended" {
				q.state = "ended"
			}
			turn := wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
			e, err := applyWC008Capture(t.Context(), turn, q)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := storeWC008CaptureResult(t.Context(), turn, q, e, RequestDigest{24}, ResultDigest{25}); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var lastPlayed string
			var watchedAt sql.NullString
			var revision, plays, starts int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT last_played_at,watched_at,revision,play_count,started_count FROM personal_items`).Scan(&lastPlayed, &watchedAt, &revision, &plays, &starts); err != nil {
				t.Fatal(err)
			}
			if lastPlayed != "1970-01-01T00:00:01Z" || revision != 20 || starts != 1 {
				t.Fatalf("observation changed legacy boundary: last=%q revision=%d starts=%d", lastPlayed, revision, starts)
			}
			if name == "repeated_completion" && (!watchedAt.Valid || watchedAt.String != "1970-01-01T00:00:01Z" || plays != 1) {
				t.Fatalf("repeated completion changed watched evidence: at=%+v plays=%d", watchedAt, plays)
			}
			if name == "early_ended" && (watchedAt.Valid || plays != 0) {
				t.Fatal("early end completed occurrence", watchedAt, plays)
			}
		})
	}
}
