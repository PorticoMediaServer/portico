package api

import (
	"context"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

// Home Play reads the whole row on the server, expands album cards and refills. Continue's
// chosen order survives even when playing makes the live Continue row empty.
func TestHomeRowsAreWholeQueueSources(t *testing.T) {
	th := newTheatre(t)
	db.QueueMax, playqueue.Fill, playqueue.RefillBelow = 12, 5, 2
	t.Cleanup(func() { db.QueueMax, playqueue.Fill, playqueue.RefillBelow = 10_000, 1000, 200 })
	_, first := th.album("Alpha", 20)
	_, second := th.album("Zulu", 13)
	library := th.libs[db.LibraryMusic].PublicID
	playAll := func(q Queue) []string {
		t.Helper()
		var played []string
		for range 60 {
			path := "/v1/queues/" + q.ID
			var page QueueEntries
			th.queue("GET", path+"/entries?around=current&before=0&after=0", "", nil, &page)
			if len(page.Items) != 1 {
				t.Fatalf("current entry: %+v", page)
			}
			played = append(played, page.Items[0].ItemID)
			if len(played) == 7 {
				th.s.queues = playqueue.New(th.s.db, nil, th.s.now)
			}
			w := th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "completion"}, &q)
			if w.Code == 409 {
				return played
			}
			if w.Code != 200 {
				t.Fatalf("advance: %d %s", w.Code, w.Body)
			}
		}
		t.Fatal("the source did not end")
		return nil
	}
	for _, shuffle := range []bool{false, true} {
		source := playqueue.QueueSource{HomeRow: &playqueue.HomeRowSource{ID: "recently_added", Library: library, Sort: "title", Order: "asc"}}
		q := th.create(QueueCreate{Source: source, Shuffle: shuffle}).Queue
		if !q.Refills || q.Total != 5 {
			t.Fatalf("whole Home row did not become a refillable queue: %+v", q)
		}
		got, want := playAll(q), slices.Concat(first, []string{th.song}, second)
		if shuffle {
			slices.Sort(got)
			slices.Sort(want)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("Home row shuffle=%t: %v; want %v", shuffle, got, want)
		}
	}
	var saved ListCreated
	if w := th.queue("POST", "/v1/lists", "", ListCreate{Kind: "saved_view", Name: "Albums", Library: library,
		Query: "kind=album&sort=title&order=desc"}, &saved); w.Code != 201 {
		t.Fatal(w.Body)
	}
	if code, _ := patchPreferences(t, th.s, th.owner.AccessToken, "profile-server", map[string]any{"home.savedViews": []string{saved.ID}}); code != 200 {
		t.Fatalf("saved row: %d", code)
	}
	savedQueue := th.create(QueueCreate{Source: playqueue.QueueSource{HomeRow: &playqueue.HomeRowSource{
		ID: "view:" + saved.ID, Library: "not-the-saved-library", Sort: "title", Order: "asc"}}}).Queue
	if got, want := playAll(savedQueue), slices.Concat(first, second); !slices.Equal(got, want) {
		t.Fatalf("saved row lost its album filter, library or order: %v; want %v", got, want)
	}
	for _, id := range first[:13] {
		if w := call(t, th.s, "PUT", "/v1/items/"+id+"/state", th.owner.AccessToken, map[string]any{"positionMs": 60_000}, nil); w.Code != 200 {
			t.Fatalf("progress: %d %s", w.Code, w.Body)
		}
	}
	row := get[HomeRow](t, th.s, th.owner.AccessToken, "/v1/home/rows/continue_listening?library="+library)
	if len(row.Items) != 13 {
		t.Fatalf("Continue source: %+v", row)
	}
	want := make([]string, len(row.Items))
	for n, item := range row.Items {
		want[n] = item.ID
	}
	q := th.create(QueueCreate{Source: playqueue.QueueSource{HomeRow: &playqueue.HomeRowSource{ID: "continue_listening", Library: library}}}).Queue
	for _, id := range first[:13] {
		if w := call(t, th.s, "PUT", "/v1/items/"+id+"/state", th.owner.AccessToken, map[string]any{"positionMs": 0}, nil); w.Code != 200 {
			t.Fatal(w.Body)
		}
	}
	if got := playAll(q); !slices.Equal(got, want) {
		t.Fatalf("Continue changed the saved queue selection: %v, want %v", got, want)
	}
	books, err := th.s.db.CreateLibrary(context.Background(), "Books", db.LibraryBooks, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := th.s.db.Import(context.Background(), db.ImportBatch{LibraryID: books.ID, PathID: books.Paths[0].ID, Items: []db.ImportItem{
		{Kind: db.KindBook, Title: "A book", Keys: []db.ItemKey{{Kind: db.KeyName, Key: "a-book"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	if w := th.queue("POST", "/v1/queues", "", QueueCreate{Source: playqueue.QueueSource{HomeRow: &playqueue.HomeRowSource{
		ID: "recently_added", Library: books.PublicID}}}, nil); w.Code != 422 {
		t.Fatalf("a reading-only row was accepted as playable: %d %s", w.Code, w.Body)
	}
	if w := th.queue("POST", "/v1/queues", "", QueueCreate{Source: playqueue.QueueSource{HomeRow: &playqueue.HomeRowSource{ID: "view:not-yours"}}}, nil); w.Code != 404 {
		t.Fatalf("hidden saved view: %d %s", w.Code, w.Body)
	}
}
