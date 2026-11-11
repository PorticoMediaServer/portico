package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func (b *bookShelf) state(id string, body map[string]any) StateReply {
	b.Helper()
	var out StateReply
	if w := call(b.T, b.s, "PUT", "/v1/items/"+id+"/state", b.owner.AccessToken, body, &out); w.Code != 200 {
		b.Fatalf("PUT state answered %d: %s", w.Code, w.Body)
	}
	return out
}

func (b *bookShelf) homeRow(id string) []string {
	b.Helper()
	var ids []string
	for _, row := range get[Home](b.T, b.s, b.owner.AccessToken, "/v1/home").Rows {
		if row.ID == id {
			for _, it := range row.Items {
				ids = append(ids, it.ID)
			}
		}
	}
	return ids
}

// One place per book, whichever file it was made in: the later place wins, another version
// opens near it, reaching the end marks the book read, marking by hand clears the place, and
// with Pause history on no place is kept.
func TestAPlaceInABook(t *testing.T) {
	b := newBookShelf(t)
	nineteen := b.find(db.KindBook, "Nineteen")
	book := get[BookAnswer](t, b.s, b.owner.AccessToken, "/v1/items/"+nineteen+"/book")
	place := func(progression float64, extra string) string {
		return fmt.Sprintf(`{"progression":%g,"fingerprint":%q,"cfi":"epubcfi(/6/2!/4/2)"%s}`, progression, book.Fingerprint, extra)
	}

	b.state(nineteen, map[string]any{"position": place(0.4, "")})
	// A place made earlier on a device that was out of reach arrives later and loses.
	if got := b.state(nineteen, map[string]any{"position": place(0.1, ""), "madeAgoMs": 60_000}); got.Position != place(0.4, "") {
		t.Fatalf("an older place won: %s", got.Position)
	}
	if w := call(t, b.s, "PUT", "/v1/items/"+nineteen+"/state", b.owner.AccessToken, map[string]any{"position": "page 4"}, nil); w.Code != 400 {
		t.Fatalf("a place with no progression answered %d", w.Code)
	}
	if again := get[BookAnswer](t, b.s, b.owner.AccessToken, "/v1/items/"+nineteen+"/book"); !again.PlaceExact || !strings.Contains(string(again.Place), "0.4") {
		t.Fatalf("the same file opened at %s (exact %v)", again.Place, again.PlaceExact)
	}
	for _, v := range book.Versions {
		if v.Format == "pdf" {
			if pdf := get[BookAnswer](t, b.s, b.owner.AccessToken, "/v1/items/"+nineteen+"/book?version="+v.ID); pdf.PlaceExact || pdf.Place == nil {
				t.Fatalf("another version should open near the place: %s (exact %v)", pdf.Place, pdf.PlaceExact)
			}
		}
	}
	if row := b.homeRow("continue_reading"); len(row) != 1 || row[0] != nineteen || len(b.homeRow("continue")) != 0 {
		t.Fatalf("Continue your books %v, Continue Watching %v", row, b.homeRow("continue"))
	}

	if got := b.state(nineteen, map[string]any{"position": place(1, `,"atEnd":true`)}); !got.Watched {
		t.Fatal("reaching the end did not mark the book read")
	}
	if row := b.homeRow("continue_reading"); len(row) != 0 {
		t.Fatalf("a finished book is still in Continue your books: %v", row)
	}
	if got := b.state(nineteen, map[string]any{"watched": false}); got.Watched || got.Position != "" {
		t.Fatalf("marking unread by hand should clear the place: %+v", got.ItemState)
	}

	if err := b.s.db.SetProfilePreference(context.Background(), 1, db.PrefPauseHistory, "true", b.clock); err != nil {
		t.Fatal(err)
	}
	if got := b.state(nineteen, map[string]any{"position": place(0.7, `,"atEnd":true`)}); got.Position != "" || got.Watched {
		t.Fatalf("a place was kept with Pause history on: %+v", got.ItemState)
	}
	if got := b.state(nineteen, map[string]any{"watched": true}); !got.Watched {
		t.Fatal("a mark by hand was not kept with Pause history on")
	}
}
