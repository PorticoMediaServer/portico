package catalog

import (
	"strings"
	"testing"
)

// Library-wide listening lists (a music library's artists, releases and
// songs; an audiobook library's books) must answer a first page without
// reading the library: the page walks the browse title index by key, with no
// sort of the whole set, and the total comes from maintained counts. At 1M
// songs the old count(*) and ORDER BY over the whole projection timed out.
func TestLibraryListeningSectionsWalkTheTitleIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, c, _ := phase34ListeningFixture(t)
	db := c.DB
	for _, tc := range []struct{ library, view, section, kind string }{
		{"music", "browse", "artists", "artist"},
		{"music", "releases", "releases", "album"},
		{"music", "songs", "songs", "song"},
		{"books", "browse", "books", "book"},
	} {
		predicate, args := listeningSectionVisibility(tc.kind, Viewer{Profile: "local:account:profile", Libraries: []string{tc.library}})
		predicate = strings.ReplaceAll(predicate, "projected.id", "e.entity_id")
		predicate = strings.ReplaceAll(predicate, "projected.entity_id", "e.entity_id")
		query := `EXPLAIN QUERY PLAN SELECT e.entity_id,e.sort_key FROM catalog_browse_rows e INDEXED BY catalog_browse_title WHERE e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND e.kind=? AND ` + predicate + ` AND (e.sort_key COLLATE NOCASE > ? OR (e.sort_key COLLATE NOCASE = ? AND e.entity_id > ?)) ORDER BY e.sort_key COLLATE NOCASE ASC,e.entity_id ASC LIMIT 41 OFFSET 0`
		kinds := map[string]int{"artist": 5, "album": 6, "song": 7, "book": 8}
		rows, err := db.Query(query, append(append([]any{tc.library, kinds[tc.kind]}, args...), "", "", "")...)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				break
			}
			plan.WriteString(detail + "\n")
		}
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if got := plan.String(); !strings.Contains(got, "catalog_browse_title") || strings.Contains(got, "USE TEMP B-TREE FOR ORDER BY") {
			t.Fatalf("%s page lost its indexed walk:\n%s", tc.section, got)
		}

		// Behavior: the whole list pages by key, in title order, without
		// repeats, and the total matches the rows served.
		request := phase34ListeningRequest(tc.library, tc.view, "")
		request.Limit = 2
		seen := map[string]bool{}
		var titles []string
		total := -1
		for pages := 0; pages < 500; pages++ {
			page, err := s.Content(request)
			if err != nil {
				t.Fatalf("%s: %v", tc.section, err)
			}
			var sec *ContentSection
			for i := range page.Sections {
				if page.Sections[i].ID == tc.section {
					sec = &page.Sections[i]
				}
			}
			if sec == nil {
				break
			}
			if total == -1 {
				total = sec.TotalCount
			}
			for _, entry := range sec.Entries {
				if seen[entry.ID] {
					t.Fatalf("%s repeated %s", tc.section, entry.ID)
				}
				seen[entry.ID] = true
				titles = append(titles, strings.ToLower(entry.Title))
			}
			if sec.NextCursor == "" {
				break
			}
			request.Cursor = sec.NextCursor
		}
		if len(seen) == 0 || total != len(seen) {
			t.Fatalf("%s: total %d, served %d", tc.section, total, len(seen))
		}
	}
}
