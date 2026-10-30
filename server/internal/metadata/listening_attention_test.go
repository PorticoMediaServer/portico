package metadata

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// The binding 22 October rule supersedes the former missing-author list:
// unresolved music remains listed; a local audiobook is a normal catalog item
// with an ordinary editable Author, never a public attention entry.
func TestMusicAndBooksNeedAttention(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	album := c.Album(c.Artist(music, "Artist"), "Release", 2020)
	c.Song(album, 1, "/music/01.mp3", "Song")
	book := c.Book(books, "Untagged", "")
	c.BookFile(book, 1, "/books/Untagged/part.m4b")
	c.Exec(`INSERT INTO mb_jobs(kind,entity_id,album_id,status) VALUES('album',?,?,'unresolved')
 ON CONFLICT(kind,entity_id) DO UPDATE SET status='unresolved',generation=generation+1`, album.ID, album.ID)
	c.Drain()
	s := New(c.DB, "")
	ctx := context.Background()
	page, err := s.Attention(ctx, "music", "", 25)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Kind != "album" || page.Total != 1 {
		t.Fatalf("music attention: %+v %v", page, err)
	}
	assertQuietBook := func(wantMusic int) {
		t.Helper()
		page, err := s.Attention(ctx, "books", "", 25)
		if err != nil || len(page.Entries) != 0 || page.Total != 0 {
			t.Fatalf("local audiobook publicly listed: %+v %v", page, err)
		}
		all, err := s.Attention(ctx, "", "", 25)
		if err != nil || all.Total != int64(wantMusic) || len(all.Entries) != wantMusic {
			t.Fatalf("global attention: %+v %v", all, err)
		}
		for _, entry := range all.Entries {
			if entry.Kind != "album" {
				t.Fatalf("non-music global entry: %+v", entry)
			}
		}
		counts, err := s.AttentionCounts(ctx)
		if err != nil || counts.Total != int64(wantMusic) {
			t.Fatalf("counts: %+v %v", counts, err)
		}
		found := false
		for _, count := range counts.Libraries {
			if count.LibraryID == "books" {
				found = true
				if count.Total != 0 {
					t.Fatalf("book count: %+v", count)
				}
			}
			if count.LibraryID == "music" && count.Total != int64(wantMusic) {
				t.Fatalf("music count: %+v", count)
			}
		}
		if !found {
			t.Fatal("audiobook library count disappeared rather than returning zero")
		}
	}
	assertQuietBook(1)
	catalogBooks, _, err := catalog.New(c.DB).Books(catalog.Viewer{Libraries: []string{"books"}}, "books", "", 25)
	if err != nil || len(catalogBooks) != 1 || catalogBooks[0].ID != book.Public || catalogBooks[0].Title != "Untagged" || catalogBooks[0].Author != "" {
		t.Fatalf("missing-author book did not remain in the ordinary catalog: %+v %v", catalogBooks, err)
	}
	target := RepairTarget{Kind: "book", ID: book.Public}
	before, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	authorControl := false
	for _, field := range before.Schema {
		if field.Field == "author" && field.Type == "text" && field.Bulk {
			authorControl = true
		}
	}
	if !authorControl || before.Snapshot.Fields["author"].Value != "" {
		t.Fatal("ordinary single/bulk Author control missing", before.Schema)
	}
	author := "Someone"
	after, err := s.Repair(ctx, target, RepairCommand{ExpectedRevision: before.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"author": {Value: &author}}}, editorActor(), allowAll)
	if err != nil || after.Snapshot.Fields["author"].Value != author {
		t.Fatal("ordinary Author edit failed", after, err)
	}
	c.Drain()
	catalogBooks, _, err = catalog.New(c.DB).Books(catalog.Viewer{Libraries: []string{"books"}}, "books", "", 25)
	if err != nil || len(catalogBooks) != 1 || catalogBooks[0].ID != book.Public || catalogBooks[0].Author != author {
		t.Fatal("edited author not in catalog", catalogBooks, err)
	}
	assertQuietBook(1)
	empty := ""
	after, err = s.Repair(ctx, target, RepairCommand{ExpectedRevision: after.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{"author": {Value: &empty}}}, editorActor(), allowAll)
	if err != nil || after.Snapshot.Fields["author"].Value != "" {
		t.Fatal("clearing optional Author failed", after, err)
	}
	assertQuietBook(1)
	page, err = s.Attention(ctx, "music", "", 25)
	if err != nil || len(page.Entries) != 1 {
		t.Fatal(page, err)
	}
	entry := page.Entries[0]
	if err = s.AttentionAction(ctx, "music", AttentionCommand{Kind: entry.Kind, EntityID: entry.EntityID, ExpectedRevision: entry.Revision, Action: "retry"}, MBActor{}, nil); err != nil {
		t.Fatal(err)
	}
	if page, err = s.Attention(ctx, "music", "", 25); err != nil || len(page.Entries) != 0 || page.Total != 0 {
		t.Fatalf("an album looked up again is still waiting: %+v %v", page, err)
	}
	assertQuietBook(0)
}

// Legacy raw-row bookkeeping is separate from public-list eligibility. Keep
// its existing cleanup assertion until that storage is deliberately retired.
func TestBookAttentionBookkeepingClearsWhenAuthorIsAdded(t *testing.T) {
	c := catalogtest.Open(t)
	books := c.Library("books", "Books", "audiobook", "/books")
	book := c.Book(books, "Untagged", "")
	c.Exec(`UPDATE catalog_books SET author='' WHERE entity_id=?`, book.ID)
	c.Exec(`UPDATE catalog_books SET author='Someone' WHERE entity_id=?`, book.ID)
	var n int
	if err := c.DB.QueryRow(`SELECT count(*) FROM listening_metadata_attention WHERE entity_id=?`, book.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("authored book still waits internally: %d %v", n, err)
	}
}
