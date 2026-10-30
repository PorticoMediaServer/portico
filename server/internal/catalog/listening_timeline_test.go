package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestBookTimelinePagesSeekProgressAndUnknownFacts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, n := phase34ListeningFixture(t)
	c.Exec(`UPDATE catalog_assets SET duration=CASE id WHEN ? THEN 60 ELSE 90 END WHERE id IN(?,?)`, n["part-a1"].Asset, n["part-a1"].Asset, n["part-a2"].Asset)
	c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position) VALUES('local:account:profile',?,?,'resume',15000)`, n["book-a"].ID, n["part-a2"].ID)
	c.Drain()
	r := listeningRequest("books", "book", n["book-a"].Public)
	r.Limit = 1
	position := int64(75000)
	page, err := s.BookTimeline(r, n["book-a"].Public, &position)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 2 || len(page.Parts) != 1 || page.NextCursor == "" || page.DurationMs == nil || *page.DurationMs != 150000 || page.Seek == nil || page.Seek.ItemID != n["part-a2"].Public || page.Seek.StartMs != 15000 {
		t.Fatalf("timeline: %+v", page)
	}
	if page.Progress == nil || page.Progress.PositionMs == nil || *page.Progress.PositionMs != 75000 || *page.Progress.RemainingMs != 75000 || page.Progress.Chapter == nil || page.Progress.Chapter.Index != 0 || page.Progress.Chapter.StartMs != 0 {
		t.Fatalf("progress: %+v", page.Progress)
	}
	r.Cursor = page.NextCursor
	second, err := s.BookTimeline(r, n["book-a"].Public, nil)
	if err != nil || len(second.Parts) != 1 || second.Parts[0].ItemID != n["part-a2"].Public || *second.Parts[0].OffsetMs != 60000 || second.NextCursor != "" {
		t.Fatal(second, err)
	}
	other := r
	other.Viewer.Fence = "changed"
	other.ViewerFence = "changed"
	if _, err = s.BookTimeline(other, n["book-a"].Public, nil); !errors.Is(err, ErrCursor) {
		t.Fatal("cursor crossed viewer", err)
	}
	r.Cursor = ""
	c.Exec(`UPDATE catalog_assets SET duration=0 WHERE id=?`, n["part-a1"].Asset)
	c.Drain()
	page, err = s.BookTimeline(r, n["book-a"].Public, nil)
	if err != nil || page.DurationMs != nil || page.Progress == nil || page.Progress.PositionMs != nil || page.Progress.RemainingMs != nil {
		t.Fatalf("unknown length invented totals: %+v %v", page, err)
	}
	if _, err = s.BookTimeline(r, n["book-a"].Public, &position); err == nil {
		t.Fatal("seek crossed unknown prefix")
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, n["part-a1"].Asset, false)
	})
	c.Drain()
	page, err = s.BookTimeline(r, n["book-a"].Public, nil)
	if err != nil || page.TotalCount != 1 || len(page.Parts) != 1 || page.Parts[0].ItemID != n["part-a2"].Public || page.Parts[0].OffsetMs != nil || page.DurationMs != nil {
		t.Fatalf("unavailable part leaked or compressed timeline: %+v %v", page, err)
	}
	r.Viewer.Libraries = []string{"music"}
	if _, err = s.BookTimeline(r, n["book-a"].Public, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("foreign library timeline", err)
	}
}

func TestBookTimelineMetadataAndOriginalEditionTitle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, n := phase34ListeningFixture(t)
	c.Fields(n["book-a"].ID, map[string]any{"title": "Alpha (Unabridged)"})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return saveLocalBookMetadata(tx, n["book-a"].ID, map[string]string{"author": "Author", "narrator": "Reader", "series": "Saga", "series_position": "2.5", "publisher": "Local Press", "language": "en", "description": "Local description.", "date": "2020-03-20"}, map[string]string{"description": "opf"})
	})
	c.Drain()
	r := listeningRequest("books", "book", n["book-a"].Public)
	page, err := s.Content(r)
	if err != nil {
		t.Fatal(err)
	}
	e := page.Entity
	if e == nil || e.Title != "Alpha (Unabridged)" || e.DisplayTitle != "" || e.Edition != "Unabridged" || e.Publisher != "Local Press" || e.Language != "en" || e.Overview != "Local description." || e.PublishedYear == nil || *e.PublishedYear != 2020 || len(e.Narrators) != 1 || len(e.Series) != 1 || e.Series[0].Position != "2.5" {
		t.Fatalf("metadata: %+v", e)
	}
	var original string
	if err = c.DB.QueryRow(`SELECT title FROM catalog_entities WHERE id=?`, n["book-a"].ID).Scan(&original); err != nil || original != "Alpha (Unabridged)" {
		t.Fatal(original, err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetBookChaptersTx(ctx, tx, n["part-a1"].ID, nil)
	})
	c.Drain()
	page, err = s.Content(r)
	if err != nil {
		t.Fatal(err)
	}
	parts := false
	for _, sec := range page.Sections {
		if sec.ID == "files" && sec.TotalCount == 2 {
			parts = true
		}
	}
	if !parts {
		t.Fatal("parts missing without chapter marks")
	}
	data, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
}

func TestBookTimelineRestrictedPartsNeverLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, n := phase34ListeningFixture(t)
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17)`)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAttributesTx(ctx, tx, n["part-a1"].ID, "contentRating", []string{"R"})
	})
	c.Drain()
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := listeningRequest("books", "book", n["book-a"].Public)
	r.Viewer.Restrictions = phase34RestrictionOf(phase34Ceiling(13), false)
	page, err := s.BookTimeline(r, n["book-a"].Public, nil)
	if err != nil || page.TotalCount != 1 || len(page.Parts) != 1 || page.Parts[0].ItemID != n["part-a2"].Public || page.DurationMs != nil || page.Parts[0].OffsetMs != nil {
		t.Fatalf("restricted timeline: %+v %v", page, err)
	}
}

func TestBookHomeDocumentAndPageEmbedActualChapterAndPartTarget(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	r := homeRequestFixture()
	part, book := homeItem(t, db, "p2"), homeItem(t, db, "book")
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,120000,0,'book-play')`, part.ID)
	c.Exec(`INSERT INTO progress_activity VALUES('p','books',?,?,'paused')`, part.ID, r.Now.Format(time.RFC3339))
	c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position,unit,completed) VALUES('p',?,?,'book-play',120000,0,0)`, book.ID, part.ID)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetBookChaptersTx(ctx, tx, part.ID, []compactcatalog.Chapter{{Title: "Actual stored chapter", Start: 100, End: 200}})
	})
	c.Drain()
	page, err := s.HomeSingleRow(r, "continue_books", HomeRowPage{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || len(page.Resumes) != 1 {
		t.Fatalf("resume identities: %+v", page)
	}
	e := page.Entries[0]
	if e.ID != book.Public || e.Playback == nil || e.Playback.ItemID != part.Public || *e.Playback.StartSeconds != 120 || page.Resumes[0].Target.ID != part.Public {
		t.Fatalf("part target changed: %+v", page)
	}
	p := e.BookProgress
	if p == nil || p.ItemID != part.Public || p.PartPositionMs != 120000 || p.PositionMs == nil || *p.PositionMs != 5520000 || p.DurationMs == nil || *p.DurationMs != 16200000 || p.RemainingMs == nil || *p.RemainingMs != 10680000 || p.Chapter == nil || p.Chapter.Index != 0 || p.Chapter.Title != "Actual stored chapter" {
		t.Fatalf("embedded facts: %+v", p)
	}
	if !reflect.DeepEqual(page.Resumes[0].Target.BookProgress, p) {
		t.Fatal("resume target lost embedded progress")
	}
	document, err := s.HomeRows(r)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range document.Rows {
		if row.ID == "continue_books" {
			found = true
			if len(row.Entries) != 1 || !reflect.DeepEqual(row.Entries[0].BookProgress, p) {
				t.Fatal("document and page differ", row)
			}
		}
	}
	if !found {
		t.Fatal("in-progress book missing from document")
	}
}

func TestBookTimelineAndListeningViewsShareCanonicalPartOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, n := phase34ListeningFixture(t)
	c.Fields(n["part-a1"].ID, map[string]any{"sort_title": "Zebra"})
	c.Fields(n["part-a2"].ID, map[string]any{"sort_title": "Alpha"})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, name := range []string{"part-a1", "part-a2"} {
			if err := compactcatalog.SetFactsTx(ctx, tx, n[name].ID, map[string]any{"part_number": nil}); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	r := listeningRequest("books", "book", n["book-a"].Public)
	timeline, err := s.BookTimeline(r, n["book-a"].Public, nil)
	if err != nil || len(timeline.Parts) != 2 || timeline.Parts[0].ItemID != n["part-a2"].Public {
		t.Fatal(timeline, err)
	}
	page, err := s.Content(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range page.Sections {
		if section.ID == "files" && section.Entries[0].ID != timeline.Parts[0].ItemID {
			t.Fatal("part page order differs", section)
		}
		if section.ID == "chapters" && section.Entries[0].Playback.ItemID != timeline.Parts[0].ItemID {
			t.Fatal("chapter order differs", section)
		}
	}
	selection, err := s.ListeningSelection(r, ListeningTarget{"books", "book", n["book-a"].Public}, "ordered", "", false, "")
	if err != nil || selection.Entries[0].ItemID != timeline.Parts[0].ItemID {
		t.Fatal("queue order differs", selection, err)
	}
}
