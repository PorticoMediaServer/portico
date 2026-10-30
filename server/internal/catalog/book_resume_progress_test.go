package catalog

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A book file counts as played at the played threshold, but a listener who is
// still in its last stretch keeps their place in the book: only the file's
// real end takes the book's resume point back to the start of a file.
func TestBookResumeSurvivesThePlayedThreshold(t *testing.T) {
	c := catalogtest.Open(t)
	books := c.Library("books", "Books", "audiobook", "/books")
	book := c.Book(books, "Long Book", "Author")
	part := c.BookFile(book, 1, "/books/long/part1.m4b")
	c.Drain()
	s := New(c.DB)
	v := identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile"}
	key := identity.PersonalKey(v)
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,0,0,0,'play')`, key, part.ID)
	c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position,unit) VALUES(?,?,?,'play',0,0)`, key, book.ID, part.ID)
	resume := func() int64 {
		t.Helper()
		var position int64
		if err := c.DB.QueryRow(`SELECT position FROM book_resume WHERE profile_id=? AND book_id=?`, key, book.ID).Scan(&position); err != nil {
			t.Fatal(err)
		}
		return position
	}
	const duration = 43200. // twelve hours
	accept(t, s, c.DB, v, part.Public, 1, 21600, duration, "playing")
	if got := resume(); got != 21_600_000 {
		t.Fatalf("resume mid-book is %d ms", got)
	}
	// Thirty minutes from the end is past the default 95% threshold.
	accept(t, s, c.DB, v, part.Public, 2, 41400, duration, "playing")
	personal, err := s.Personal(key, part.Public)
	if err != nil || personal.Watched {
		t.Fatalf("a twelve-hour book completed thirty minutes early: %+v %v", personal, err)
	}
	// The file's own position is what its next session starts from.
	if personal.ProgressSeconds != 41400 {
		t.Fatalf("stopping thirty minutes from the end left the file's resume point at %v s", personal.ProgressSeconds)
	}
	if got := resume(); got != 41_400_000 {
		t.Fatalf("stopping thirty minutes from the end left the book's resume point at %d ms", got)
	}
	accept(t, s, c.DB, v, part.Public, 3, 42000, duration, "paused")
	if got := resume(); got != 42_000_000 {
		t.Fatalf("a later report in the last stretch left the resume point at %d ms", got)
	}
	accept(t, s, c.DB, v, part.Public, 4, duration, duration, "ended")
	if got := resume(); got != 43_200_000 {
		t.Fatalf("the file's real end left the resume point at %d ms", got)
	}
	if personal, err = s.Personal(key, part.Public); err != nil || !personal.Watched || personal.ProgressSeconds != duration {
		t.Fatalf("after its real end the file is %+v %v", personal, err)
	}
}
