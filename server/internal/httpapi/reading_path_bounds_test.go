package httpapi

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// A generic folder name trims to the nearest real folder, so refreshing
// Author/Unsorted works on the same logical folder as Author. It must publish
// only titles that belong to that folder: a deeper Author/Series title keeps
// its own two-title interpretation instead of the ancestor's count.
func TestReadingPrivatePathAncestorRefreshKeepsDeeperFolders(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	onePath := readingTreeFile(t, r, "Author/Series/One.epub")
	twoPath := readingTreeFile(t, r, "Author/Series/Two.epub")
	soloPath := readingTreeFile(t, r, "Author/Unsorted/Solo.epub")
	readingScan(t, r.f, r.library)
	one, two, solo := readingPathItem(t, r, onePath), readingPathItem(t, r, twoPath), readingPathItem(t, r, soloPath)
	list, err := r.s.List(ctx, r.p, r.library, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]reading.LibraryRow{}
	for _, v := range list.Items {
		rows[v.ID] = v
	}
	for _, tc := range []struct {
		id, author, series string
	}{{one, "Author", "Series"}, {two, "Author", "Series"}, {solo, "Author", ""}} {
		row, ok := rows[tc.id]
		if !ok {
			t.Fatalf("%s missing from the authorized page", tc.id)
		}
		if row.Author == nil || row.Author.Value != tc.author || row.Author.Source != compactcatalog.ReadingFolder {
			t.Fatalf("%s author=%+v want %q/folder", tc.id, row.Author, tc.author)
		}
		if tc.series == "" {
			if row.Series != nil {
				t.Fatalf("%s invented series %+v", tc.id, row.Series)
			}
			continue
		}
		if row.Series == nil || row.Series.Name != tc.series || row.Series.Source != compactcatalog.ReadingFolder {
			t.Fatalf("%s series=%+v want %q/folder", tc.id, row.Series, tc.series)
		}
	}
}

// One refresh publishes at most 128 candidates per keyset batch. A folder with
// more titles than that must still publish every candidate, and a second scan
// of unchanged files must leave exactly the same accepted source rows.
func TestReadingPrivatePathPublicationCoversEveryCandidate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	const files = 130
	for i := 0; i < files; i++ {
		readingTreeFile(t, r, fmt.Sprintf("Shelf/Work %03d.epub", i))
	}
	readingScan(t, r.f, r.library)
	snapshot := func() []string {
		t.Helper()
		// Folder facts are published to reading_path_head at COMPLETE. Every
		// version has a row there; those carrying a fact are the candidates.
		rows, err := r.f.db.Query(`SELECT item_id||'|'||asset_id||'|'||COALESCE(author,'')||'|'||COALESCE(author_source,'')||'|'||COALESCE(series,'')||'|'||COALESCE(series_source,'')||'|'||COALESCE(series_number,0)||'|'||object_id||'|'||source_revision FROM reading_path_head WHERE author IS NOT NULL OR series IS NOT NULL ORDER BY item_id,asset_id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var row string
			if err = rows.Scan(&row); err != nil {
				t.Fatal(err)
			}
			out = append(out, row)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	titles := map[string]bool{}
	for _, row := range before {
		parts := strings.Split(row, "|")
		fact := func(value, source string) bool {
			return value == "" && source == "" || value == "Shelf" && source == "folder"
		}
		if len(parts) != 9 || !fact(parts[2], parts[3]) || !fact(parts[4], parts[5]) || parts[7] == "" || parts[8] == "" {
			t.Fatalf("candidate published with unexpected facts: %q", row)
		}
		titles[parts[0]] = true
	}
	if len(titles) != files {
		t.Fatalf("published %d of %d candidates (keyset batch lost candidates)", len(titles), files)
	}
	readingScan(t, r.f, r.library)
	after := snapshot()
	if len(after) != len(before) {
		t.Fatalf("unchanged rescan changed the row count: %d then %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("unchanged rescan rewrote accepted facts: %q then %q", before[i], after[i])
		}
	}
}
