package reading

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A page reads only the viewer's books, a book's sections in order, and goes on from its
// cursor across books with each hit once. Reading a new book leaves the revision alone, so a
// reader's next page still holds; changing or dropping a book the index held raises it.
func TestBookTextScansOnlyVisibleBooksAndRaisesTheRevisionOnChange(t *testing.T) {
	ctx := context.Background()
	bt, err := OpenBookText(filepath.Join(t.TempDir(), "booktext.db"), nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer bt.Close()
	start := bt.Revision()
	for n, text := range []string{"A red fox. Another red fox.", "The hidden red fox.", "Red fox at the end."} {
		file := int64(n + 1)
		if err := bt.replace(ctx, file, Fingerprint(file, 0), []Section{{Index: 0, Path: "a.xhtml", Text: text}, {Index: 1, Path: "b.xhtml", Text: "no match"}}, false); err != nil {
			t.Fatal(err)
		}
	}
	if bt.Revision() != start || start == 0 {
		t.Fatalf("revision %d after new books, started %d", bt.Revision(), start)
	}
	owners := func(ids []int64) (map[int64]db.BookTextOwner, error) {
		out := map[int64]db.BookTextOwner{}
		for _, id := range ids {
			out[id] = db.BookTextOwner{ItemID: string(rune('a' + id)), Size: id}
		}
		return out, nil
	}
	var got []string
	for cursor, pages := "", 0; ; pages++ {
		page, err := bt.search(ctx, []int64{1, 3}, owners, "RED fox", cursor, 1)
		if err != nil || len(page.Hits) != 1 || pages > 3 {
			t.Fatalf("page %d: %+v %v", pages, page, err)
		}
		got = append(got, page.Hits[0].Owner.ItemID+":"+page.Hits[0].Excerpt[page.Hits[0].MatchStart:page.Hits[0].MatchStart+page.Hits[0].MatchLength])
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if want := "b:red fox b:red fox d:Red fox"; fmtList(got) != want {
		t.Fatalf("hits %s, want %s", fmtList(got), want)
	}
	if err := bt.replace(ctx, 3, Fingerprint(3, 1), nil, true); err != nil {
		t.Fatal(err)
	}
	if bt.Revision() != start+1 {
		t.Fatalf("revision %d after a changed book, want %d", bt.Revision(), start+1)
	}
}

func fmtList(items []string) string {
	out := ""
	for n, s := range items {
		if n > 0 {
			out += " "
		}
		out += s
	}
	return out
}

// However a page is cut, by the hit limit, by the 200 matching sections read at a time or by
// the time a page may take, the pages hold every visible hit once and no hidden one, and a
// cursor reads as nothing and is refused when altered.
func TestBookTextPagesHoldEveryHitOnceHoweverTheyAreCut(t *testing.T) {
	ctx := context.Background()
	bt, err := OpenBookText(filepath.Join(t.TempDir(), "booktext.db"), nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer bt.Close()
	want := 0
	for file := int64(1); file <= 6; file++ {
		var sections []Section
		for n := range 120 {
			sections = append(sections, Section{Index: n, Path: "s.xhtml", Text: "a blue moon"})
		}
		if err := bt.replace(ctx, file, Fingerprint(file, 0), sections, false); err != nil {
			t.Fatal(err)
		}
		if file%2 == 1 {
			want += len(sections)
		}
	}
	owners := func(ids []int64) (map[int64]db.BookTextOwner, error) {
		out := map[int64]db.BookTextOwner{}
		for _, id := range ids {
			out[id] = db.BookTextOwner{Size: id}
		}
		return out, nil
	}
	for _, budget := range []time.Duration{time.Minute, 0} {
		pageTime = budget
		seen, cursor := map[[2]int64]bool{}, ""
		for pages := 0; ; pages++ {
			page, err := bt.search(ctx, []int64{1, 3, 5}, owners, "blue moon", cursor, 50)
			if err != nil || pages > 500 {
				t.Fatalf("budget %v page %d: %v", budget, pages, err)
			}
			for _, h := range page.Hits {
				at := [2]int64{h.Owner.Size, int64(h.SectionIndex)}
				if h.Owner.Size%2 == 0 || seen[at] {
					t.Fatalf("budget %v: hit %v hidden or repeated", budget, at)
				}
				seen[at] = true
			}
			if page.Next == "" {
				break
			}
			if raw, _ := base64.RawURLEncoding.DecodeString(page.Next); len(raw) != 12+16+16 {
				t.Fatalf("a cursor is not sealed: %q", raw)
			}
			cursor = page.Next
		}
		if len(seen) != want {
			t.Fatalf("budget %v: %d hits, want %d", budget, len(seen), want)
		}
	}
	pageTime = 250 * time.Millisecond
	page, _ := bt.search(ctx, []int64{1}, owners, "blue moon", "", 1)
	altered := []byte(page.Next)
	altered[len(altered)/2] ^= 1
	if _, err := bt.search(ctx, []int64{1}, owners, "blue moon", string(altered), 1); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("an altered cursor: %v", err)
	}
}
