package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Stop's final position is a served-state write: it takes the writer gate at
// the established-playback class, in the same transaction as the progress
// evidence and the end (SRV031). These tests drive the real public
// Service.Stop on a session the real start path created.
const stopPositionStatement = `UPDATE playback_v1_sessions SET position_ms=?,last_seq=? WHERE id=? AND ended_ms=0`

// stopWriterAdmissions is the number of ClassEstablishedPlayback admissions one
// video Stop takes with the final position admitted: one transaction for the
// evidence, the position and the end, then the grant fence. The three
// separate writes it replaced took four.
const stopWriterAdmissions = 2

type stoppedRow struct {
	position   int64
	lastSeq    int64
	ended      int64
	revision   int64
	generation int64
	endReason  string
	media      string
}

func readStoppedRow(t *testing.T, db *sql.DB, id string) stoppedRow {
	t.Helper()
	var r stoppedRow
	if err := db.QueryRow(`SELECT position_ms,last_seq,ended_ms,revision,generation,end_reason,media_session_id FROM playback_v1_sessions WHERE id=?`, id).Scan(&r.position, &r.lastSeq, &r.ended, &r.revision, &r.generation, &r.endReason, &r.media); err != nil {
		t.Fatal(err)
	}
	return r
}

func establishedPlaybackAdmissions() uint64 {
	return dbwork.WriteGate().Stats().ByClass[dbwork.ClassEstablishedPlayback.String()].Acquired
}

func assertGrantFenced(t *testing.T, db *sql.DB, media string) {
	t.Helper()
	if media == "" {
		t.Fatal("fixture session has no media session to fence")
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM playback_sessions WHERE id=?`, media).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "stopped" {
		t.Fatalf("media grant is %q after Stop, want stopped", state)
	}
}

// TestStopAdmitsFinalPositionThroughTheWriterGate is the finding's regression:
// the real Stop stores the exact final position and sequence, still ends the
// session and fences its grant, all in one writer transaction.
func TestStopAdmitsFinalPositionThroughTheWriterGate(t *testing.T) {
	f := newCarryFixture(t, false)
	q := f.queue()
	session := *q.Session
	before := readStoppedRow(t, f.catalog.DB, session.ID)

	traceCtx, statements := dbwork.TraceQueryPlans(context.Background())
	admissionsBefore := establishedPlaybackAdmissions()
	stopAt := int64(42_000)
	if err := f.service.Stop(traceCtx, f.caller, session.ID, &stopAt); err != nil {
		t.Fatal(err)
	}
	admissions := establishedPlaybackAdmissions() - admissionsBefore
	after := readStoppedRow(t, f.catalog.DB, session.ID)

	if admissions != stopWriterAdmissions {
		t.Fatalf("Stop took %d established-playback writer admissions, want %d: the final-position write did not go through the gate", admissions, stopWriterAdmissions)
	}
	if after.position != stopAt {
		t.Fatalf("final position is %d, want %d", after.position, stopAt)
	}
	if after.lastSeq != before.lastSeq+1 {
		t.Fatalf("last_seq is %d, want exactly one past %d", after.lastSeq, before.lastSeq)
	}
	if after.ended == 0 || after.endReason != "stopped" {
		t.Fatalf("session did not end as stopped: %+v", after)
	}
	if after.revision != before.revision+1 {
		t.Fatalf("revision is %d, want one past %d: only End may bump it", after.revision, before.revision)
	}
	if after.generation != before.generation {
		t.Fatalf("generation moved from %d to %d", before.generation, after.generation)
	}
	assertGrantFenced(t, f.catalog.DB, before.media)

	// The statement itself, once, after the legacy progress write and before
	// the End update.
	lines := statements()
	progressAt, positionAt, endAt := -1, -1, -1
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, `UPDATE playback_sessions SET sequence=?`):
			progressAt = i
		case line == stopPositionStatement:
			if positionAt >= 0 {
				t.Fatalf("final-position statement issued more than once: %d and %d", positionAt, i)
			}
			positionAt = i
		case strings.HasPrefix(line, `UPDATE playback_v1_sessions SET ended_ms=?`):
			if endAt < 0 {
				endAt = i
			}
		}
	}
	if progressAt < 0 || positionAt < 0 || endAt < 0 {
		t.Fatalf("traced statements missing (progress %d, position %d, end %d):\n%s", progressAt, positionAt, endAt, strings.Join(lines, "\n"))
	}
	if !(progressAt < positionAt && positionAt < endAt) {
		t.Fatalf("final-position statement is out of order: progress %d, position %d, end %d", progressAt, positionAt, endAt)
	}
}

// Storage failures roll back final evidence and the end, so the exact Stop can
// be retried. A failed final position must never look like a successful Stop.
func TestStopFinalPositionWriteErrorRollsBack(t *testing.T) {
	f := newCarryFixture(t, false)
	session := *f.queue().Session
	before := readStoppedRow(t, f.catalog.DB, session.ID)
	f.catalog.Exec(`CREATE TRIGGER test_stop_position_write_failure BEFORE UPDATE OF position_ms ON playback_v1_sessions BEGIN SELECT RAISE(ABORT,'injected final-position write failure'); END`)
	stopAt := int64(43_500)
	if err := f.service.Stop(context.Background(), f.caller, session.ID, &stopAt); err == nil {
		t.Fatal("storage error reported success")
	}
	if after := readStoppedRow(t, f.catalog.DB, session.ID); after != before {
		t.Fatalf("failed stop changed row: %+v before %+v", after, before)
	}
	var state string
	if err := f.catalog.DB.QueryRow(`SELECT state FROM playback_sessions WHERE id=?`, before.media).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == "stopped" {
		t.Fatal("failed stop fenced grant")
	}
	f.catalog.Exec(`DROP TRIGGER test_stop_position_write_failure`)
	if err := f.service.Stop(context.Background(), f.caller, session.ID, &stopAt); err != nil {
		t.Fatal(err)
	}
	if after := readStoppedRow(t, f.catalog.DB, session.ID); after.position != stopAt || after.ended == 0 {
		t.Fatalf("retry: %+v", after)
	}
}

// TestEndedSessionStopLeavesFinalPosition records nothing on a second Stop: the
// idempotent path returns before any writer admission.
func TestEndedSessionStopLeavesFinalPosition(t *testing.T) {
	f := newCarryFixture(t, false)
	q := f.queue()
	session := *q.Session
	first := int64(1_000)
	if err := f.service.Stop(context.Background(), f.caller, session.ID, &first); err != nil {
		t.Fatal(err)
	}
	before := readStoppedRow(t, f.catalog.DB, session.ID)

	admissionsBefore := establishedPlaybackAdmissions()
	late := int64(9_000)
	if err := f.service.Stop(context.Background(), f.caller, session.ID, &late); err != nil {
		t.Fatal(err)
	}
	admissions := establishedPlaybackAdmissions() - admissionsBefore
	after := readStoppedRow(t, f.catalog.DB, session.ID)

	if admissions != 0 {
		t.Fatalf("stop of an ended session took %d writer admissions, want 0", admissions)
	}
	if after != before {
		t.Fatalf("ended session changed: %+v, before %+v", after, before)
	}
}

// TestRefusedStopWithFinalPositionWritesNothing keeps the ownership refusal
// ahead of the position write.
func TestRefusedStopWithFinalPositionWritesNothing(t *testing.T) {
	f := newCarryFixture(t, false)
	q := f.queue()
	session := *q.Session
	before := readStoppedRow(t, f.catalog.DB, session.ID)

	c := f.caller
	c.Principal.AccountID = "intruder"
	admissionsBefore := establishedPlaybackAdmissions()
	stopAt := int64(12_345)
	if err := f.service.Stop(context.Background(), c, session.ID, &stopAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign Stop returned %v, want ErrNotFound", err)
	}
	admissions := establishedPlaybackAdmissions() - admissionsBefore
	after := readStoppedRow(t, f.catalog.DB, session.ID)

	if admissions != 0 {
		t.Fatalf("refused stop took %d writer admissions, want 0", admissions)
	}
	if after != before {
		t.Fatalf("refused stop changed the row: %+v, before %+v", after, before)
	}
	var state string
	if err := f.catalog.DB.QueryRow(`SELECT state FROM playback_sessions WHERE id=?`, before.media).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == "stopped" {
		t.Fatal("refused stop fenced the grant")
	}
}
