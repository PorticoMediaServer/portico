package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

// HM-5, Q24: Continue your books holds a book, comic or magazine being read,
// with its real saved position, and drops it once it is finished.
func TestHomeContinueYourBooksIncludesReadingInProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}
	for _, tc := range []struct{ kind, format string }{{"book", "epub"}, {"comic", "cbz"}} {
		t.Run(tc.format, func(t *testing.T) {
			r := newReadingFixture(t, tc.kind, tc.format)
			// The derived worker runs beside the server; drain it so the
			// Reading projection Home reads through is ready.
			readingSeriesDrain(t, r)
			h := New(r.d)
			row := func() catalog.HomeRow {
				t.Helper()
				w := readingHTTP(t, h, "GET", "/v1/home/rows/continue_books", r.f.owner.AccessToken, "", nil)
				var out catalog.HomeRow
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
					t.Fatalf("continue_books: %d %s", w.Code, w.Body.String())
				}
				return out
			}
			m := readingOpened(t, r, h)
			save := func(expected int64, progression float64) {
				t.Helper()
				location := ReadingLocation{Format: ReadingFormat(tc.format), Progression: progression}
				if tc.format == "epub" {
					location.CFI, location.Section = "epubcfi(/6/4!/4/2:0)", m.Spine[0]
				} else {
					page := int64(0)
					location.PageIndex = &page
				}
				if w := readingHTTP(t, h, "PUT", "/v1/reading/sessions/"+m.SessionID+"/progress", r.f.owner.AccessToken, m.Revision, ReadingProgressRequest{ExpectedRevision: expected, Location: location}); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				readingSeriesDrain(t, r)
			}
			save(0, .4)
			got := row()
			if len(got.Entries) != 1 || got.Entries[0].ID != r.item {
				t.Fatalf("a %s in progress is missing from Continue your books: %+v", tc.kind, got.Entries)
			}
			progress := got.Entries[0].ReadingProgress
			if progress == nil || progress.Progression != .4 || progress.Stale {
				t.Fatalf("reading progress: %+v", progress)
			}
			if tc.format == "epub" && progress.PageIndex != nil || tc.format == "cbz" && (progress.PageIndex == nil || *progress.PageIndex != 0) {
				t.Fatalf("page index must be the saved one and absent for EPUB: %+v", progress)
			}
			save(1, 1)
			if got = row(); len(got.Entries) != 0 {
				t.Fatalf("a finished %s stays in Continue your books: %+v", tc.kind, got.Entries)
			}
		})
	}
}
