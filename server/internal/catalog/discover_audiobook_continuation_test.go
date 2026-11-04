package catalog

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestAudiobookDiscoverContinuesTheBookAtItsCurrentPart(t *testing.T) {
	db := homeFixture(t)
	c := catalogtest.New(t, db)
	s := New(db)
	r := homeRequestFixture("books")
	book, first, current := homeItem(t, db, "book"), homeItem(t, db, "p1"), homeItem(t, db, "p2")
	for _, part := range []struct {
		id       int64
		position int
		at       time.Time
	}{{first.ID, 300000, r.Now.Add(-time.Minute)}, {current.ID, 120000, r.Now}} {
		c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,?,0,'discover-book')`, part.id, part.position)
		c.Exec(`INSERT INTO progress_activity VALUES('p','books',?,?,'paused')`, part.id, part.at.Format(time.RFC3339))
	}
	c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position,unit,completed) VALUES('p',?,?,'discover-book',120000,0,0)`, book.ID, current.ID)
	c.Drain()

	discovery, err := s.DiscoverWithRestrictions("books", "p", r.Viewer.Restrictions, r.Now)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, section := range discovery.Sections {
		if section.ID != "continue_listening" {
			continue
		}
		found = true
		if len(section.Items) != 1 {
			t.Fatalf("one multipart book must be one continuation: %+v", section.Items)
		}
		entry := section.Items[0]
		if entry.ID != book.Public || entry.Kind != "book" || entry.Playback == nil || entry.Playback.ItemID != current.Public || entry.Playback.StartSeconds == nil || *entry.Playback.StartSeconds != 120 {
			t.Fatalf("book identity and current-part playback: %+v", entry)
		}
		progress := entry.BookProgress
		if progress == nil || progress.ItemID != current.Public || progress.PartPositionMs != 120000 || progress.PositionMs == nil || *progress.PositionMs != 5520000 || progress.DurationMs == nil || *progress.DurationMs != 16200000 || progress.RemainingMs == nil || *progress.RemainingMs != 10680000 {
			t.Fatalf("whole-book progress: %+v", progress)
		}
		if entry.Duration == nil || *entry.Duration != 16200 || entry.ProgressSeconds == nil || *entry.ProgressSeconds != 5520 {
			t.Fatalf("book progress summary: duration=%v position=%v", entry.Duration, entry.ProgressSeconds)
		}
	}
	if !found {
		t.Fatal("audiobook Discover omitted Continue Listening")
	}
	other, err := s.DiscoverWithRestrictions("books", "q", r.Viewer.Restrictions, r.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range other.Sections {
		if section.ID == "continue_listening" {
			t.Fatalf("another profile received the book continuation: %+v", section.Items)
		}
	}
}
