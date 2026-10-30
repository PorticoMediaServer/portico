package downloads

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type bookHarness struct {
	*harness
	book     catalogtest.Item
	parts    []catalogtest.Item
	personal string
}

// newBookHarness adds one audiobook with five parts to the ordinary harness.
func newBookHarness(t *testing.T) *bookHarness {
	t.Helper()
	h := newHarness(t)
	c := catalogtest.New(t, h.db)
	root := filepath.Join(h.root, "books")
	books := c.Library("books", "Books", "audiobook", root)
	b := &bookHarness{harness: h, book: c.Book(books, "Dune", "Frank Herbert"), personal: identity.PersonalKey(h.viewer.Viewer)}
	for part := 1; part <= 5; part++ {
		b.parts = append(b.parts, c.BookFile(b.book, part, filepath.Join(root, "Dune", "0"+string(rune('0'+part))+".m4b")))
	}
	settleDownloadsProjection(t, h.db)
	return b
}

// pointer reads where the server would resume the book: which part, and where
// in it.
func (b *bookHarness) pointer(t *testing.T) (int64, int64) {
	t.Helper()
	var item, position int64
	if e := b.db.QueryRow(`SELECT item_id,position FROM book_resume WHERE profile_id=? AND book_id=?`, b.personal, b.book.ID).Scan(&item, &position); e != nil {
		t.Fatal(e)
	}
	return item, position
}

// listenedOnline records what an online session leaves behind for one part: its
// progress, its activity, and the book's pointer naming it.
func (b *bookHarness) listenedOnline(t *testing.T, part catalogtest.Item, seconds int64, at time.Time) {
	t.Helper()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,?,0,'online') ON CONFLICT(profile_id,item_id) DO UPDATE SET position=excluded.position,playback_id=excluded.playback_id`, []any{b.personal, part.ID, seconds * 1000}},
		{`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES(?,'books',?,?,'paused') ON CONFLICT(profile_id,item_id) DO UPDATE SET updated_at=excluded.updated_at`, []any{b.personal, part.ID, at.UTC().Format(time.RFC3339)}},
		{`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position,unit) VALUES(?,?,?,'online',?,0) ON CONFLICT(profile_id,book_id) DO UPDATE SET item_id=excluded.item_id,playback_id=excluded.playback_id,position=excluded.position`, []any{b.personal, b.book.ID, part.ID, seconds * 1000}},
	} {
		if _, e := b.db.Exec(statement.query, statement.args...); e != nil {
			t.Fatal(e)
		}
	}
}

func (b *bookHarness) sync(t *testing.T, operation string, entries ...ProgressEntry) ProgressReceipt {
	t.Helper()
	// Each entry here is its own play unless the test names one.
	for i := range entries {
		if entries[i].PlayID == "" {
			entries[i].PlayID = operation + "-" + strconv.Itoa(i)
		}
	}
	out, e := b.service.SyncProgress(context.Background(), b.viewer, operation, entries)
	if e != nil {
		t.Fatal(e)
	}
	return out
}

// Listening offline moves through a book exactly as listening online does: the
// book resumes in the part the listener was last in, not in the part they were
// in when they went offline. The order the observations arrive in does not
// matter, only when each was made.
func TestOfflineListeningMovesTheBookToTheLaterPart(t *testing.T) {
	for _, order := range [][]int{{2, 3, 4}, {4, 3, 2}} {
		b := newBookHarness(t)
		b.listenedOnline(t, b.parts[1], 120, b.now.Add(-6*time.Hour))
		var entries []ProgressEntry
		for _, index := range order {
			entries = append(entries, ProgressEntry{ItemID: b.parts[index].Public, PositionSeconds: float64(100 * index), ObservedAt: b.now.Add(time.Duration(index-5) * time.Hour).Format(time.RFC3339)})
		}
		if out := b.sync(t, "flight", entries...); out.Applied != 3 {
			t.Fatalf("order %v: %+v", order, out)
		}
		if item, position := b.pointer(t); item != b.parts[4].ID || position != 400_000 {
			t.Fatalf("order %v: the book resumes in part id %d at %d ms, want part five (%d) at 400000", order, item, position, b.parts[4].ID)
		}
	}
}

// A book nobody has listened to online has no pointer yet; listening offline
// gives it one.
func TestOfflineListeningStartsABook(t *testing.T) {
	b := newBookHarness(t)
	if out := b.sync(t, "first-listen", ProgressEntry{ItemID: b.parts[0].Public, PositionSeconds: 30, ObservedAt: b.now.Add(-time.Hour).Format(time.RFC3339)}); out.Applied != 1 {
		t.Fatalf("%+v", out)
	}
	if item, position := b.pointer(t); item != b.parts[0].ID || position != 30_000 {
		t.Fatalf("the book resumes in part id %d at %d ms", item, position)
	}
}

// The pointer is ordered the way progress is: an offline observation of one
// part does not pull the book back from a part the listener reached online
// afterwards.
func TestOfflineListeningDoesNotPullTheBookBackFromLaterOnlineListening(t *testing.T) {
	b := newBookHarness(t)
	b.listenedOnline(t, b.parts[4], 900, b.now.Add(-time.Hour))
	out := b.sync(t, "late-sync", ProgressEntry{ItemID: b.parts[2].Public, PositionSeconds: 300, ObservedAt: b.now.Add(-3 * time.Hour).Format(time.RFC3339)})
	// The part's own position is still recorded: nothing online superseded it.
	if out.Applied != 1 {
		t.Fatalf("%+v", out)
	}
	if item, position := b.pointer(t); item != b.parts[4].ID || position != 900_000 {
		t.Fatalf("an older offline observation moved the book to part id %d at %d ms", item, position)
	}
	// Listening further in the part the book already names still moves within it.
	b.sync(t, "same-part", ProgressEntry{ItemID: b.parts[4].Public, PositionSeconds: 1200, ObservedAt: b.now.Format(time.RFC3339)})
	if item, position := b.pointer(t); item != b.parts[4].ID || position != 1_200_000 {
		t.Fatalf("the book did not follow within its part: part id %d at %d ms", item, position)
	}
}

// startedOnline records exactly what starting an online session leaves behind
// before its first progress report: the session row (the schema stamps when it
// was created), and the part's progress and the book's pointer both bound to
// that session at the part's stored resume point. It writes no activity, because a
// start does not; the part's activity is whatever an earlier session left, or
// nothing. The creation stamp is then placed on the harness clock, which is
// fixed, where production would have the wall clock.
func (b *bookHarness) startedOnline(t *testing.T, session string, part catalogtest.Item, resumeSeconds int64, at time.Time) {
	t.Helper()
	if _, e := b.db.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES(?,'login','account','profile',?,?,1,'playing',?,?,'2099-01-01T00:00:00Z',?,5400)`, session, part.ID, part.Token, "grant-hash-"+session, "grant-"+session, "request-"+session); e != nil {
		t.Fatal(e)
	}
	stamped, e := b.db.Exec(`UPDATE playback_observations SET created_at=? WHERE session_id=? AND created_at IS NOT NULL`, at.UTC().Format("2006-01-02T15:04:05.000Z"), session)
	if e != nil {
		t.Fatal(e)
	}
	if n, _ := stamped.RowsAffected(); n != 1 {
		t.Fatalf("starting a session stamped %d creation rows", n)
	}
	if _, e = b.db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,?,0,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET playback_id=excluded.playback_id,position=excluded.position,unit=excluded.unit`, b.personal, part.ID, resumeSeconds*1000, session); e != nil {
		t.Fatal(e)
	}
	if _, e = b.db.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position,unit) VALUES(?,?,?,?,?,0) ON CONFLICT(profile_id,book_id) DO UPDATE SET item_id=excluded.item_id,playback_id=excluded.playback_id,position=excluded.position,unit=excluded.unit`, b.personal, b.book.ID, part.ID, session, resumeSeconds*1000); e != nil {
		t.Fatal(e)
	}
}

// partPosition reads one part's own stored position, which is what the next
// online session of that part resumes from.
func (b *bookHarness) partPosition(t *testing.T, part catalogtest.Item) int64 {
	t.Helper()
	var position int64
	if e := b.db.QueryRow(`SELECT position FROM progress WHERE profile_id=? AND item_id=?`, b.personal, part.ID).Scan(&position); e != nil {
		t.Fatal(e)
	}
	return position
}

func (b *bookHarness) writer(t *testing.T) string {
	t.Helper()
	var session string
	if e := b.db.QueryRow(`SELECT playback_id FROM book_resume WHERE profile_id=? AND book_id=?`, b.personal, b.book.ID).Scan(&session); e != nil {
		t.Fatal(e)
	}
	return session
}

// An online session that has just started owns the book's pointer from the
// moment it starts, before it has reported any progress. An offline observation
// of another part made before that start must not take the book back, and must
// not unbind the session, or the session's own reports could never move the
// pointer again. The part's old activity, or its absence, does not date the
// start.
func TestOfflineListeningDoesNotTakeTheBookFromASessionThatJustStarted(t *testing.T) {
	for _, activity := range []string{"none", "from an earlier day"} {
		t.Run(activity, func(t *testing.T) {
			b := newBookHarness(t)
			if activity != "none" {
				b.listenedOnline(t, b.parts[4], 900, b.now.Add(-30*time.Hour))
			}
			b.startedOnline(t, "session-noon", b.parts[4], 900, b.now.Add(-time.Hour))
			out := b.sync(t, "late-sync", ProgressEntry{ItemID: b.parts[2].Public, PositionSeconds: 300, ObservedAt: b.now.Add(-2 * time.Hour).Format(time.RFC3339)})
			// The part's own position is still recorded.
			if out.Applied != 1 {
				t.Fatalf("%+v", out)
			}
			if item, position := b.pointer(t); item != b.parts[4].ID || position != 900_000 {
				t.Fatalf("an observation older than the session's start moved the book to part id %d at %d ms", item, position)
			}
			if session := b.writer(t); session != "session-noon" {
				t.Fatalf("the started session lost the pointer: writer %q", session)
			}
			// An observation made after the start is later evidence, and does move it.
			b.sync(t, "after-start", ProgressEntry{ItemID: b.parts[3].Public, PositionSeconds: 60, ObservedAt: b.now.Add(-30 * time.Minute).Format(time.RFC3339)})
			if item, position := b.pointer(t); item != b.parts[3].ID || position != 60_000 {
				t.Fatalf("a later observation did not move the book: part id %d at %d ms", item, position)
			}
			// The play holds the pointer now, in the same order of intentional
			// plays a session holds it in, so the next session to start takes
			// it from the play exactly as it would from another session.
			if session := b.writer(t); !strings.HasPrefix(session, "offline-") {
				t.Fatalf("the superseded session kept the pointer: writer %q", session)
			}
		})
	}
}

// The session's last report dates it too: a session started long ago that is
// still reporting is newer than an observation made in between.
func TestOfflineListeningDoesNotTakeTheBookFromASessionStillReporting(t *testing.T) {
	b := newBookHarness(t)
	b.startedOnline(t, "session-long", b.parts[4], 900, b.now.Add(-5*time.Hour))
	if _, e := b.db.Exec(`UPDATE playback_observations SET reported_at=?,position=1500 WHERE session_id='session-long'`, b.now.Add(-time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")); e != nil {
		t.Fatal(e)
	}
	b.sync(t, "between", ProgressEntry{ItemID: b.parts[2].Public, PositionSeconds: 300, ObservedAt: b.now.Add(-3 * time.Hour).Format(time.RFC3339)})
	if item, _ := b.pointer(t); item != b.parts[4].ID || b.writer(t) != "session-long" {
		t.Fatalf("an observation older than the session's last report moved the book to part id %d (writer %q)", item, b.writer(t))
	}
}

// The same holds inside one part. A session that has just started on the part
// the listener is already in has put the book at its resume point; an offline
// observation of that same part made before the start is older evidence and
// changes nothing, neither where the book resumes nor where the next session of
// that part would. An observation made at the very instant of the start loses
// to it as well. One made afterwards is newer, and moves the book within the
// part.
func TestOfflineListeningDoesNotRewindAPartASessionHasJustStarted(t *testing.T) {
	for _, activity := range []string{"none", "from an earlier day"} {
		t.Run(activity, func(t *testing.T) {
			b := newBookHarness(t)
			part := b.parts[4]
			if activity != "none" {
				b.listenedOnline(t, part, 600, b.now.Add(-30*time.Hour))
			}
			started := b.now.Add(-time.Hour)
			b.startedOnline(t, "session-noon", part, 900, started)
			for name, observedAt := range map[string]time.Time{"before the start": started.Add(-time.Hour), "at the start": started} {
				out := b.sync(t, "older-"+name[:2], ProgressEntry{ItemID: part.Public, PositionSeconds: 300, ObservedAt: observedAt.Format(time.RFC3339)})
				if out.Applied != 0 || out.Conflicts != 1 || out.Entries[0].Outcome != ProgressSuperseded || out.Entries[0].PositionSeconds != 900 {
					t.Fatalf("an observation %s was not superseded: %+v", name, out)
				}
				if item, position := b.pointer(t); item != part.ID || position != 900_000 || b.writer(t) != "session-noon" {
					t.Fatalf("an observation %s rewound the book to %d ms in part id %d (writer %q)", name, position, item, b.writer(t))
				}
				if position := b.partPosition(t, part); position != 900_000 {
					t.Fatalf("an observation %s rewound the part itself to %d ms", name, position)
				}
			}
			out := b.sync(t, "newer", ProgressEntry{ItemID: part.Public, PositionSeconds: 1200, ObservedAt: started.Add(30 * time.Minute).Format(time.RFC3339)})
			if out.Applied != 1 {
				t.Fatalf("an observation after the start was refused: %+v", out)
			}
			if item, position := b.pointer(t); item != part.ID || position != 1_200_000 {
				t.Fatalf("a later observation did not move the book within its part: %d ms in part id %d", position, item)
			}
		})
	}
}

// The other arrival order: the offline listening reaches the server first and
// is applied, then a session starts online in the same part. What was listened
// to offline before that start, arriving late, must not rewind it either, and
// neither must an observation older than the session's last report.
func TestOfflineListeningArrivingAfterALaterOnlineStartInTheSamePartIsSuperseded(t *testing.T) {
	b := newBookHarness(t)
	part := b.parts[4]
	if out := b.sync(t, "first", ProgressEntry{ItemID: part.Public, PositionSeconds: 300, ObservedAt: b.now.Add(-6 * time.Hour).Format(time.RFC3339)}); out.Applied != 1 {
		t.Fatalf("%+v", out)
	}
	if item, position := b.pointer(t); item != part.ID || position != 300_000 {
		t.Fatalf("the offline listening was not applied: %d ms in part id %d", position, item)
	}
	// Online, later: the session starts where the part was left, and listens on.
	b.startedOnline(t, "session-later", part, 300, b.now.Add(-3*time.Hour))
	late := b.sync(t, "late", ProgressEntry{ItemID: part.Public, PositionSeconds: 100, ObservedAt: b.now.Add(-4 * time.Hour).Format(time.RFC3339)})
	if late.Applied != 0 || late.Entries[0].Outcome != ProgressSuperseded {
		t.Fatalf("listening older than the later online start was applied: %+v", late)
	}
	if _, e := b.db.Exec(`UPDATE playback_observations SET reported_at=?,position=900 WHERE session_id='session-later'`, b.now.Add(-time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")); e != nil {
		t.Fatal(e)
	}
	for _, statement := range []string{`UPDATE progress SET position=900000 WHERE profile_id=? AND playback_id='session-later'`, `UPDATE book_resume SET position=900000 WHERE profile_id=? AND playback_id='session-later'`} {
		if _, e := b.db.Exec(statement, b.personal); e != nil {
			t.Fatal(e)
		}
	}
	between := b.sync(t, "between", ProgressEntry{ItemID: part.Public, PositionSeconds: 500, ObservedAt: b.now.Add(-2 * time.Hour).Format(time.RFC3339)})
	if between.Applied != 0 || between.Entries[0].Outcome != ProgressSuperseded || between.Entries[0].PositionSeconds != 900 {
		t.Fatalf("listening older than the session's last report was applied: %+v", between)
	}
	if item, position := b.pointer(t); item != part.ID || position != 900_000 || b.writer(t) != "session-later" || b.partPosition(t, part) != 900_000 {
		t.Fatalf("late offline listening rewound the book to %d ms in part id %d (writer %q, part at %d ms)", position, item, b.writer(t), b.partPosition(t, part))
	}
}

// Two parts observed within the same second are still ordered by when each was
// observed, whichever arrives first and whether they arrive together or in
// separate batches: the later observation is where the book resumes.
func TestOfflineListeningOrdersPartsObservedInTheSameSecond(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	second := time.Unix(1_759_990_000, 0).UTC()
	earlier := func(b *bookHarness) ProgressEntry {
		return ProgressEntry{ItemID: b.parts[2].Public, PositionSeconds: 300, ObservedAt: second.Add(100 * time.Millisecond).Format(time.RFC3339Nano)}
	}
	later := func(b *bookHarness) ProgressEntry {
		return ProgressEntry{ItemID: b.parts[4].Public, PositionSeconds: 900, ObservedAt: second.Add(900 * time.Millisecond).Format(time.RFC3339Nano)}
	}
	for name, deliver := range map[string]func(*bookHarness){
		"one batch, in order":        func(b *bookHarness) { b.sync(t, "both", earlier(b), later(b)) },
		"one batch, reversed":        func(b *bookHarness) { b.sync(t, "both", later(b), earlier(b)) },
		"separate batches, in order": func(b *bookHarness) { b.sync(t, "first", earlier(b)); b.sync(t, "second", later(b)) },
		"separate batches, reversed": func(b *bookHarness) { b.sync(t, "first", later(b)); b.sync(t, "second", earlier(b)) },
	} {
		t.Run(name, func(t *testing.T) {
			b := newBookHarness(t)
			deliver(b)
			if item, position := b.pointer(t); item != b.parts[4].ID || position != 900_000 {
				t.Fatalf("the book resumes in part id %d at %d ms, want the later observation's part (%d) at 900000", item, position, b.parts[4].ID)
			}
		})
	}
}

// A film is not a part of a book, and gets no pointer.
func TestOfflineProgressOnAFilmWritesNoBookPointer(t *testing.T) {
	b := newBookHarness(t)
	b.sync(t, "film", ProgressEntry{ItemID: b.item, PositionSeconds: 10, ObservedAt: b.now.Format(time.RFC3339)})
	var rows int
	if e := b.db.QueryRow(`SELECT count(*) FROM book_resume`).Scan(&rows); e != nil || rows != 0 {
		t.Fatalf("book pointers %d %v", rows, e)
	}
}

func TestFutureOfflineClockCannotFenceLaterOnlineActivity(t *testing.T) {
	b := newBookHarness(t)
	part := b.parts[0]
	future := b.now.Add(12 * time.Hour)
	out := b.sync(t, "future-clock", ProgressEntry{ItemID: part.Public, PositionSeconds: 120, ObservedAt: future.Format(time.RFC3339)})
	if out.Applied != 1 {
		t.Fatalf("usable skewed observation discarded: %+v", out)
	}
	var mark int64
	if err := b.db.QueryRow(`SELECT observed_ms FROM download_progress_marks WHERE item_id=?`, part.ID).Scan(&mark); err != nil || mark != b.now.UnixMilli() {
		t.Fatalf("future mark persisted: %d %v", mark, err)
	}
	b.listenedOnline(t, part, 240, b.now.Add(time.Second))
	out = b.sync(t, "later-online", ProgressEntry{ItemID: part.Public, PositionSeconds: 180, ObservedAt: future.Format(time.RFC3339)})
	if out.Entries[0].Outcome != ProgressStale && out.Entries[0].Outcome != ProgressSuperseded {
		t.Fatalf("skewed replay beat later activity: %+v", out)
	}
	var position int64
	if err := b.db.QueryRow(`SELECT position FROM progress WHERE item_id=? AND profile_id=?`, part.ID, b.personal).Scan(&position); err != nil || position != 240000 {
		t.Fatalf("newer online resume lost: %d %v", position, err)
	}
}
