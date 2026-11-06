package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestWCCaptureLegacyStartThresholds(t *testing.T) {
	cases := []struct {
		position, duration float64
		percent, kind      int64
		completed, want    bool
	}{
		{4.999, 100, 5, 1, false, false}, {5, 100, 5, 1, false, true},
		{29.999, 0, 5, 1, false, false}, {30, 0, 5, 1, false, true},
		{0.999, 100, 50, 9, false, false}, {1, 100, 50, 9, false, true}, {0, 0, 50, 1, true, true},
	}
	for _, c := range cases {
		q := wc008CaptureReport{positionSeconds: c.position, durationSeconds: c.duration, startedPercent: c.percent, completed: c.completed}
		q.original.values[9] = c.kind
		got, err := q.qualifiedStart()
		if err != nil || got != c.want {
			t.Fatal(c, got, err)
		}
	}
	q := wc008CaptureReport{positionSeconds: math.NaN()}
	q.original.values[9] = int64(1)
	if _, err := q.qualifiedStart(); err == nil {
		t.Fatal("nonfinite report accepted")
	}
}

func wcCaptureFixture(t *testing.T, w *executor) wc008CaptureReport {
	t.Helper()
	p := wc008NativeProgress(t, w, 1000)
	wc008NativeExec(t, w, `UPDATE play_history SET item_kind=1,duration_ms=100000,position=1000`)
	turn := wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
	snapshot, err := readWC008Start(t.Context(), turn, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE playback_personal_claims SET pending_time_from_ms=NULL,pending_time_until_ms=NULL,pending_time_cursor_ms=NULL,pending_time_activity_generation=NULL,pending_time_remember_generation=NULL,pending_time_clock_incarnation=NULL,pending_time_start_event_id=NULL,pending_time_occurrence_generation=NULL,occurrence_start_event_id=NULL,last_accepted_sequence=-1`)
	wc008NativeExec(t, w, `DELETE FROM play_history`)
	raw, _ := json.Marshal(map[string]any{"revision": int64(19), "watched": false, "favorite": true, "watchlisted": false, "progressSeconds": 0, "lastPlayedAt": "", "status": "current", "conflicts": []any{}, "rating": nil})
	wc008NativeExec(t, w, `UPDATE personal_items SET favorite=1`)
	wc008NativeExec(t, w, `INSERT INTO personal_snapshots(profile_id,item_id,revision,state,created_at) VALUES(?,?,19,?,'1970-01-01T00:00:00Z')`, p.profileKey, p.subjectID, string(raw))
	p.startEventID, p.lastAcceptedSequence = 0, -1
	if err := w.db.QueryRowContext(t.Context(), `SELECT control_variable_bytes FROM playback_personal_claims`).Scan(&p.oldControlBytes); err != nil {
		t.Fatal(err)
	}
	return wc008CaptureReport{source: p, original: snapshot, positionSeconds: 1, durationSeconds: 100, startedPercent: 1, state: "playing"}
}

func TestWCCaptureStartPendingReceiptThenDrain(t *testing.T) {
	w := wc008NativeExecutor(t)
	q := wcCaptureFixture(t, w)
	turn := wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
	e, err := applyWC008Capture(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	if !e.createdStart || e.startEventID <= 0 || e.personalRevision != 20 || e.viewerRevision != 24 || e.observationRevision != 42 || e.historyRevision != 32 {
		t.Fatal(e)
	}
	n, err := storeWC008CaptureResult(t.Context(), turn, q, e, RequestDigest{4}, ResultDigest{5})
	if err != nil || n <= 0 {
		t.Fatal(n, err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var starts, played, position, report, rows, bytes int64
	var kind string
	if err := w.db.QueryRowContext(t.Context(), `SELECT started_count,played_ms,resume_position FROM personal_items`).Scan(&starts, &played, &position); err != nil || starts != 1 || played != 0 || position != 1000 {
		t.Fatal(starts, played, position, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT command_kind,result_report_sequence FROM playback_claim_command_receipts`).Scan(&kind, &report); err != nil || kind != "capture_report" || report != 13 {
		t.Fatal(kind, report, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT receipt_rows,receipt_variable_bytes FROM playback_personal_claims`).Scan(&rows, &bytes); err != nil || rows != 1 || bytes != n {
		t.Fatal(rows, bytes, n, err)
	}
	var raw string
	if err := w.db.QueryRowContext(t.Context(), `SELECT state FROM personal_snapshots WHERE revision=20`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if json.Unmarshal([]byte(raw), &state) != nil || state["favorite"] != true {
		t.Fatal("manual snapshot state overwritten", raw)
	}
	// A source ACK is synthetic setup here; production ACK remains W-owned.
	wc008NativeExec(t, w, `UPDATE playback_personal_claims SET command_acknowledged_sequence=command_last_sequence`)
	p := q.source
	p.startEventID = e.startEventID
	p.lastAcceptedSequence = 13
	p.last, p.acknowledged = 1, 1
	p.identity.Sequence = 2
	p.identity.OperationID[0]++
	p.receiptRows, p.receiptVariableBytes = 1, n
	if err := w.db.QueryRowContext(t.Context(), `SELECT control_variable_bytes FROM playback_personal_claims`).Scan(&p.oldControlBytes); err != nil {
		t.Fatal(err)
	}
	turn = wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
	d, err := applyWC008ProgressMetrics(t.Context(), turn, p)
	if err != nil {
		t.Fatal(err)
	}
	if d.personalRevision != 20 || d.viewerRevision != 24 || d.observationRevision != 43 || d.historyRevision != 33 {
		t.Fatal("drain changed semantic revisions", d)
	}
	if _, err := storeWC008ProgressResult(t.Context(), turn, p, d, RequestDigest{6}, ResultDigest{7}); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT started_count,played_ms FROM personal_items`).Scan(&starts, &played); err != nil || starts != 1 || played != 1000 {
		t.Fatal(starts, played, err)
	}

	// A later report reuses the immutable START and changes only observation data.
	wc008NativeExec(t, w, `UPDATE playback_personal_claims SET command_acknowledged_sequence=command_last_sequence`)
	q.source = p
	q.source.last, q.source.acknowledged, q.source.identity.Sequence = 2, 2, 3
	q.source.identity.OperationID[0]++
	q.source.proof.reportSequence = 14
	q.source.fromMS, q.source.cursorMS, q.source.untilMS = 1000, 1000, 2000
	q.source.appliedMS, q.source.appliedAt = 2000, "1970-01-01T00:00:02Z"
	q.positionSeconds = 2
	if err := w.db.QueryRowContext(t.Context(), `SELECT control_variable_bytes,receipt_rows,receipt_variable_bytes FROM playback_personal_claims`).Scan(&q.source.oldControlBytes, &q.source.receiptRows, &q.source.receiptVariableBytes); err != nil {
		t.Fatal(err)
	}
	turn = wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
	again, err := applyWC008Capture(t.Context(), turn, q)
	if err != nil || again.createdStart || again.startEventID != e.startEventID || again.personalRevision != 20 || again.viewerRevision != 24 {
		t.Fatal("repeated report counted START or changed semantic revision", again, err)
	}
	if _, err := storeWC008CaptureResult(t.Context(), turn, q, again, RequestDigest{8}, ResultDigest{9}); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var lastPlayed string
	var startEvents int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT started_count,last_played_at,resume_position,(SELECT count(*) FROM play_history WHERE event_kind='started') FROM personal_items`).Scan(&starts, &lastPlayed, &position, &startEvents); err != nil || starts != 1 || startEvents != 1 || lastPlayed != "1970-01-01T00:00:01Z" || position != 2000 {
		t.Fatal(starts, startEvents, lastPlayed, position, err)
	}
	// An unresolved pending interval cannot be replaced by another report.
	q.source.last, q.source.acknowledged, q.source.identity.Sequence = 3, 3, 4
	q.source.proof.reportSequence = 15
	wc008NativeExec(t, w, `UPDATE playback_personal_claims SET command_acknowledged_sequence=command_last_sequence`)
	if err := w.db.QueryRowContext(t.Context(), `SELECT control_variable_bytes FROM playback_personal_claims`).Scan(&q.source.oldControlBytes); err != nil {
		t.Fatal(err)
	}
	turn = wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
	if bad, err := applyWC008Capture(t.Context(), turn, q); err == nil || bad != (wc008CaptureEffect{}) {
		t.Fatal("pending interval replaced", bad, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func TestWCCaptureUnqualifiedAndPendingRefuseIntact(t *testing.T) {
	w := wc008NativeExecutor(t)
	q := wcCaptureFixture(t, w)
	q.positionSeconds = 0.999
	turn := wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
	if e, err := applyWC008Capture(t.Context(), turn, q); err == nil || e != (wc008CaptureEffect{}) {
		t.Fatal("pre-start prefix accepted", e, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var last int64
	var start sql.NullInt64
	if err := w.db.QueryRowContext(t.Context(), `SELECT last_accepted_sequence,occurrence_start_event_id FROM playback_personal_claims`).Scan(&last, &start); err != nil || last != -1 || start.Valid {
		t.Fatal(last, start, err)
	}
	if _, err := applyWC008Capture(context.Background(), nil, q); err == nil {
		t.Fatal("nil turn accepted")
	}
}

func TestWCProgressClosedGrammar(t *testing.T) {
	for _, d := range wcProgressDefinitions() {
		t.Run(string(d.ID), func(t *testing.T) {
			if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
				t.Fatal(issues)
			}
		})
	}
}

func TestWCCaptureCompletionFirstHasEndedActivityAndNoTimeCredit(t *testing.T) {
	w := wc008NativeExecutor(t)
	q := wcCaptureFixture(t, w)
	q.positionSeconds, q.completed = 0, true
	turn := wc008NativeTurn(t, w, ProgressAccept, wcProgressDefinitions())
	e, err := applyWC008Capture(t.Context(), turn, q)
	if err != nil || !e.createdStart {
		t.Fatal(e, err)
	}
	if _, err := storeWC008CaptureResult(t.Context(), turn, q, e, RequestDigest{10}, ResultDigest{11}); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var state string
	var watched, starts, plays, played, position, startRows, completeRows, timeRows int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT activity_state,watched,started_count,play_count,played_ms,resume_position,(SELECT count(*) FROM play_history WHERE event_kind='started'),(SELECT count(*) FROM play_history WHERE event_kind='completed' AND completed=1 AND position=0),(SELECT count(*) FROM play_history WHERE event_kind='time') FROM personal_items`).Scan(&state, &watched, &starts, &plays, &played, &position, &startRows, &completeRows, &timeRows); err != nil || state != "ended" || watched != 1 || starts != 1 || plays != 1 || played != 0 || position != 0 || startRows != 1 || completeRows != 1 || timeRows != 0 {
		t.Fatal(state, watched, starts, plays, played, position, startRows, completeRows, timeRows, err)
	}
}
