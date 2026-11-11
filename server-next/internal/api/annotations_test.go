package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Bookmarks, highlights and notes belong to the profile and the book: the later edit wins
// whatever order edits arrive in, a deleted one stays deleted, comics take bookmarks only, a
// book holds 5,000, and nobody else sees them.
func TestAnnotations(t *testing.T) {
	b := newBookShelf(t)
	owner := b.owner.AccessToken
	book := "/v1/items/" + b.find(db.KindBook, "Nineteen")
	highlight := map[string]any{"kind": "highlight", "color": "green", "location": "epubcfi(/6/2!/4/2,/1:0,/1:9)", "madeOn": "abc",
		"progress": 0.25, "text": "It was a bright", "note": "first"}
	put := func(token, path string, body map[string]any) (int, Annotation) {
		var out Annotation
		w := call(t, b.s, "PUT", path, token, body, &out)
		return w.Code, out
	}
	list := func(token string) []Annotation {
		return get[Page[Annotation]](t, b.s, token, book+"/annotations").Items
	}

	if code, a := put(owner, book+"/annotations/h1", highlight); code != 200 || a.Color != "green" || a.Note != "first" {
		t.Fatalf("adding a highlight answered %d %+v", code, a)
	}
	if code, _ := put(owner, book+"/annotations/h1", highlight); code != 200 || len(list(owner)) != 1 {
		t.Fatalf("sending the same highlight again answered %d with %d listed", code, len(list(owner)))
	}
	later := map[string]any{}
	for k, v := range highlight {
		later[k] = v
	}
	later["note"] = "edited"
	put(owner, book+"/annotations/h1", later)
	// An edit made a minute ago on a device that was out of reach arrives now, and loses.
	older := map[string]any{"madeAgoMs": 60_000}
	for k, v := range highlight {
		older[k] = v
	}
	older["note"] = "stale"
	if code, a := put(owner, book+"/annotations/h1", older); code != 200 || a.Note != "edited" {
		t.Fatalf("an older edit answered %d and left %q", code, a.Note)
	}
	if code, _ := put(owner, book+"/annotations/h2", map[string]any{"kind": "highlight", "color": "purple", "location": "x", "progress": 0.1}); code != 400 {
		t.Fatalf("a purple highlight answered %d", code)
	}

	comic := "/v1/items/" + b.find(db.KindIssue, "Saga 001") + "/annotations/"
	if code, _ := put(owner, comic+"c1", map[string]any{"kind": "highlight", "color": "yellow", "location": "2", "progress": 0.5}); code != 400 {
		t.Fatalf("a highlight in a comic answered %d", code)
	}
	if code, _ := put(owner, comic+"c1", map[string]any{"kind": "bookmark", "location": "2", "progress": 0.5}); code != 200 {
		t.Fatalf("a bookmark in a comic answered %d", code)
	}

	// Nobody else sees them: not a profile that may read the book, and not one that may not.
	started, _ := b.s.tokens.CheckAccess(owner, time.Now())
	v, err := b.s.db.CreateSeedProfile(context.Background(), db.SeedProfile{Name: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := b.s.tokens.Access(auth.Claims{UserID: v.UserID, ProfileID: v.ProfileID, Role: "member", Boot: started.Boot}, time.Now(), time.Now().Add(time.Hour))
	if got := list(other); len(got) != 0 {
		t.Fatalf("another profile sees %d annotations", len(got))
	}
	if code, _ := put(other, book+"/annotations/h1", highlight); code != 200 || len(list(owner)) != 1 || list(owner)[0].Note != "edited" {
		t.Fatal("another profile's annotation with the same id touched the first profile's")
	}
	if w := call(t, b.s, "GET", book+"/annotations", b.limited, nil, nil); w.Code != 404 {
		t.Fatalf("a hidden book's annotations answered %d", w.Code)
	}

	// Deleting one deletes it everywhere: an older edit that arrives later does not bring it back,
	// and a deletion that arrives before the annotation it deletes keeps it from appearing.
	if w := call(t, b.s, "DELETE", book+"/annotations/h1", owner, nil, nil); w.Code != 204 || len(list(owner)) != 0 {
		t.Fatalf("deleting answered %d", w.Code)
	}
	if code, _ := put(owner, book+"/annotations/h1", older); code != 410 {
		t.Fatalf("an edit to a deleted highlight answered %d", code)
	}
	var results AnnotationResults
	itemID := b.find(db.KindBook, "Nineteen")
	call(t, b.s, "POST", "/v1/annotations:batch", owner, map[string]any{"changes": []map[string]any{
		{"itemId": itemID, "id": "h3", "delete": true},
		{"itemId": itemID, "id": "h3", "kind": "bookmark", "location": "x", "progress": 0.3, "madeAgoMs": 5000},
	}}, &results)
	if len(results.Results) != 2 || results.Results[1].Error == nil || results.Results[1].Error.Code != "annotation_deleted" || len(list(owner)) != 0 {
		t.Fatalf("a deletion sent before its annotation: %+v", results)
	}

	// A book holds 5,000 for one profile; the next is refused, and the list comes a page at a time.
	for start := 0; start < db.MaxAnnotations; start += maxAnnotationBatch {
		var changes []map[string]any
		for n := start; n < start+maxAnnotationBatch; n++ {
			changes = append(changes, map[string]any{"itemId": itemID, "id": fmt.Sprint("b", n), "kind": "bookmark", "location": "x",
				"progress": float64(n) / db.MaxAnnotations})
		}
		if w := call(t, b.s, "POST", "/v1/annotations:batch", owner, map[string]any{"changes": changes}, &results); w.Code != 200 || results.Results[0].Error != nil {
			t.Fatalf("a batch answered %d: %+v", w.Code, results.Results[0])
		}
	}
	if code, _ := put(owner, book+"/annotations/one-more", map[string]any{"kind": "bookmark", "location": "x", "progress": 1}); code != 409 {
		t.Fatalf("the 5,001st answered %d", code)
	}
	page := get[Page[Annotation]](t, b.s, owner, book+"/annotations")
	next := get[Page[Annotation]](t, b.s, owner, book+"/annotations?cursor="+page.NextCursor)
	if len(page.Items) != 500 || page.NextCursor == "" || next.Items[0].ID != "b500" {
		t.Fatalf("the first page held %d and the next started at %s", len(page.Items), next.Items[0].ID)
	}
}
