package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// TestListeningGroupsLargeLibraryCost is an opt-in measurement for B85: a
// synthetic audiobook library with one author per ten books and a series tag
// on every other book. It reports the first build, an unchanged re-pass, and
// one batch of the whole-library aggregation used by the replaced path.
//
//	PORTICO_LISTENING_GROUPS_BENCH=50000 test-only.sh internal/catalog listening_groups_bench_test.go -- -run TestListeningGroupsLargeLibraryCost -count=1 -v
func TestListeningGroupsLargeLibraryCost(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PORTICO_LISTENING_GROUPS_BENCH"))
	if n <= 0 {
		t.Skip("set PORTICO_LISTENING_GROUPS_BENCH to the book count")
	}
	c := catalogtest.Open(t)
	library := c.Library("books", "Books", "audiobook", "/books")
	seedStart := time.Now()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < n; i++ {
			book := fmt.Sprintf("b%09d", i)
			file := fmt.Sprintf("f%09d", i)
			bookID, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Book, Key: compactcatalog.BookKey(book), Title: book})
			if err != nil {
				return err
			}
			if _, _, err = compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Author, Key: compactcatalog.AuthorKey(fmt.Sprintf("Author %07d", i/10)), Title: fmt.Sprintf("Author %07d", i/10)}); err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, bookID, map[string]any{"library_id": library, "local_key": book, "author": fmt.Sprintf("Author %07d", i/10)}); err != nil {
				return err
			}
			path := "/books/" + file + ".m4b"
			fileID, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Part, Parent: bookID, Key: compactcatalog.ItemKey("/books", path, 0), Title: file, Added: "2026-04-01T00:00:00.000Z"})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, fileID, map[string]any{"book_id": bookID, "disc_number": 1, "part_number": 1}); err != nil {
				return err
			}
			assetID, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "m4b", AudioCodec: "aac", Duration: 600})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, fileID, assetID, compactcatalog.Link{}); err != nil {
				return err
			}
			if i%2 == 0 {
				if _, err = tx.ExecContext(ctx, `INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES('books',?,'series','embedded',?)`, token, fmt.Sprintf("Series %07d", i/20)); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES('books',?,'series_index','embedded','1')`, token); err != nil {
					return err
				}
			}
		}
		return nil
	})
	c.Drain()
	if _, err := c.DB.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d books in %s", n, time.Since(seedStart).Round(time.Millisecond))
	s := New(c.DB)
	ctx := context.Background()
	pass := func() (time.Duration, int) {
		start, batches := time.Now(), 0
		more := true
		for more {
			var err error
			more, err = s.RefreshListeningGroups(ctx)
			if err != nil {
				t.Fatal(err)
			}
			batches++
		}
		c.Drain()
		return time.Since(start), batches
	}
	took, batches := pass()
	var groups int
	if err := c.DB.QueryRow(`SELECT count(*) FROM listening_book_groups`).Scan(&groups); err != nil {
		t.Fatal(err)
	}
	t.Logf("first build: %d groups in %d batches, %s", groups, batches, took.Round(time.Millisecond))
	took, batches = pass()
	var start time.Time
	t.Logf("unchanged re-pass: %d batches, %s", batches, took.Round(time.Millisecond))
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"books"}}
	page := func(label string, request ContentRequest) {
		best := time.Duration(1 << 62)
		for k := 0; k < 3; k++ {
			start := time.Now()
			if _, err := s.Content(request); err != nil {
				t.Fatal(label, err)
			}
			best = min(best, time.Since(start))
		}
		t.Logf("%s: best of 3 %s", label, best.Round(100*time.Microsecond))
	}
	base := ContentRequest{Viewer: viewer, ServerID: "server", Library: "books", Profile: "p", ViewerFence: "f", Limit: 40}
	authors, series := base, base
	authors.View, series.View = "authors", "series"
	page("authors page", authors)
	page("series page", series)
	var authorID, seriesID string
	if err := c.DB.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE kind=13 ORDER BY title LIMIT 1`).Scan(&authorID); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT token FROM catalog_book_groups WHERE kind=2 ORDER BY name LIMIT 1`).Scan(&seriesID); err != nil {
		t.Fatal(err)
	}
	author, seriesPage := base, base
	author.View, author.EntityID = "author", authorID
	seriesPage.View, seriesPage.EntityID = "book_series", seriesID
	page("one author's books", author)
	page("one series' books", seriesPage)
	start = time.Now()
	var members int
	if err := c.DB.QueryRow(`SELECT count(DISTINCT b.id) FROM listening_book_context b JOIN catalog_book_groups g ON g.token=? WHERE b.library_id=g.library_id AND lower(trim(b.series_name))=g.name_key`, seriesID).Scan(&members); err != nil {
		t.Fatal(err)
	}
	t.Logf("previous series member count for ONE group row: %s (a page evaluated it for every group)", time.Since(start).Round(100*time.Microsecond))
	if _, err := c.DB.Exec(`DELETE FROM listening_book_groups`); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	rows, err := c.DB.Query(`WITH candidates AS (
	 SELECT cl.library_id,'author' kind,min(trim(b.author)) name,lower(trim(b.author)) name_key
	 FROM catalog_books b JOIN catalog_entities e ON e.id=b.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id
	 WHERE trim(b.author)<>'' GROUP BY cl.library_id,lower(trim(b.author))
	 UNION ALL
	 SELECT l.id,'book_series',min(ctx.series_name),lower(trim(ctx.series_name))
	 FROM catalog_book_context ctx JOIN catalog_libraries l ON l.id=ctx.library_id
	 WHERE ctx.series_name<>'' GROUP BY l.id,lower(trim(ctx.series_name))
	) SELECT c.library_id FROM candidates c
	 WHERE NOT EXISTS(SELECT 1 FROM listening_book_groups g WHERE g.library_id=c.library_id AND g.kind=c.kind AND g.name_key=c.name_key)
	 ORDER BY c.library_id,c.kind,c.name_key LIMIT 65`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	one := time.Since(start)
	t.Logf("previous implementation: one batch %s; a first build needed about %d batches (~%s)", one.Round(time.Millisecond), (groups+63)/64, (one * time.Duration((groups+63)/64)).Round(time.Second))
}
