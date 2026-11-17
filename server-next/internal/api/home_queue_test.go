package api

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

func TestSavedViewPlaysFromSaved(t *testing.T) {
	th := newTheatre(t)
	_, want := th.album("Selected", 3)
	library := th.libs[db.LibraryMusic].PublicID
	var saved ListCreated
	if w := th.queue("POST", "/v1/lists", "", ListCreate{Kind: "saved_view", Name: "Albums", Library: library,
		Query: "kind=album&sort=title&order=asc"}, &saved); w.Code != 201 {
		t.Fatal(w.Body)
	}
	for _, shuffle := range []bool{false, true} {
		q := th.create(QueueCreate{Source: playqueue.QueueSource{List: saved.ID}, Shuffle: shuffle}).Queue
		got := th.order(q.ID)
		if shuffle {
			slices.Sort(got)
			slices.Sort(want)
		}
		if q.Shuffle != shuffle || !slices.Equal(got, want) {
			t.Fatalf("saved view shuffle=%t: %v; want %v", shuffle, got, want)
		}
	}
	// The same view ID cannot be played by a different profile.
	addMember(t, th.s, th.owner.AccessToken, "another", "member")
	viewer := signInAs(t, th.s, "another", testPassword, 2)
	if w := call(t, th.s, "POST", "/v1/queues", viewer.AccessToken, QueueCreate{Source: playqueue.QueueSource{List: saved.ID}}, nil); w.Code != 404 {
		t.Fatalf("another profile's view: %d %s", w.Code, w.Body)
	}
}

func TestHomeRowFacetsAndQueueShareTheFilteredSource(t *testing.T) {
	th := newTheatre(t)
	library := th.libs[db.LibraryMovies]
	batch := db.ImportBatch{LibraryID: library.ID, PathID: library.Paths[0].ID, Scan: 2, Dirs: []db.ImportDir{{}}}
	for _, title := range []struct{ name, genre, release string }{
		{"Drama Here", "Drama", "2000-01-01"},
		{"Comedy Here", "Comedy", "2000-01-01"},
		{"Future Drama", "Drama", "2999-01-01"},
	} {
		path := db.FileRef{Name: title.name + ".mkv"}
		batch.Files = append(batch.Files, db.ImportFile{FileRef: path, Role: db.FileMedia, Size: 1, DurationMS: 180_000})
		batch.Items = append(batch.Items, db.ImportItem{
			Kind: db.KindMovie, Title: title.name, ReleaseDate: title.release, Keys: []db.ItemKey{{Kind: db.KeyName, Key: title.name}},
			Tags: []db.TagName{{Kind: db.TagGenre, Name: title.genre}}, Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: path}}}},
		})
	}
	if _, err := th.s.db.Import(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	token, row := th.owner.AccessToken, "recently_released"
	scope := "&library=" + library.PublicID
	genres := get[Facets](t, th.s, token, "/v1/facets?facet=genre&homeRow="+row+scope)
	letters := get[Facets](t, th.s, token, "/v1/facets?facet=letter&homeRow="+row+scope+"&sort=title")
	if len(genres.Values) != 2 || genres.Values[0].Count != 1 || genres.Values[1].Count != 1 {
		t.Fatalf("genre counts included a title outside the row: %+v", genres)
	}
	if !slices.Equal(letters.Values, []FacetValue{{Value: "C", Count: 1}, {Value: "D", Count: 1}}) {
		t.Fatalf("letter positions included a title outside the row: %+v", letters)
	}
	list := get[HomeRow](t, th.s, token, "/v1/home/rows/"+row+"?genre=Drama"+scope)
	if len(list.Items) != 1 || list.Items[0].Title != "Drama Here" {
		t.Fatalf("filtered row: %+v", list)
	}
	source := playqueue.QueueSource{HomeRow: &playqueue.HomeRowSource{ID: row, Library: library.PublicID, Genre: "Drama"}}
	for _, shuffle := range []bool{false, true} {
		queue := th.create(QueueCreate{Source: source, Shuffle: shuffle}).Queue
		if got := th.order(queue.ID); !slices.Equal(got, []string{list.Items[0].ID}) {
			t.Fatalf("queue shuffle=%t: %v does not match the row", shuffle, got)
		}
	}
}

// Home Play reads the whole row on the server, expands album cards and refills. Continue's
// chosen order survives even when playing makes the live Continue row empty.
func TestHomeRowsAreWholeQueueSources(t *testing.T) {
	th := newTheatre(t)
	db.QueueMax, playqueue.Fill, playqueue.RefillBelow = 12, 5, 2
	t.Cleanup(func() { db.QueueMax, playqueue.Fill, playqueue.RefillBelow = 10_000, 1000, 200 })
	alpha, first := th.album("Alpha", 20)
	zulu, second := th.album("Zulu", 13)
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
	// Popular uses the same shared seven-day ranking as See all. Plays after starting the
	// queue must not move its cards during refills, including after the queue service restarts.
	if code, _ := patchPreferences(t, th.s, th.owner.AccessToken, "profile-server", map[string]any{db.PrefShareActivity: true}); code != 200 {
		t.Fatalf("sharing activity: %d", code)
	}
	plays := 0
	play := func(id string, count int) {
		t.Helper()
		for range count {
			plays++
			if _, err := th.s.db.SaveProgress(context.Background(), db.User, []db.Progress{{Viewer: db.Viewer{UserID: 1, ProfileID: 1},
				ItemID: id, PlayID: fmt.Sprint("popular-", plays), PositionMS: hour, WatchedMS: hour, At: th.clock.Now()}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	play(first[0], 2)
	play(second[0], 1)
	play(th.song, 1)
	leaves := map[string][]string{alpha: first, zulu: second, th.song: {th.song}}
	popular := func() (HomeRow, []string) {
		t.Helper()
		row := get[HomeRow](t, th.s, th.owner.AccessToken, "/v1/home/rows/popular?library="+library)
		if len(row.Items) != 3 {
			t.Fatalf("Popular cards: %+v", row)
		}
		var ids []string
		for _, card := range row.Items {
			ids = append(ids, leaves[card.ID]...)
		}
		return row, ids
	}
	for _, shuffle := range []bool{false, true} {
		row, want := popular()
		q := th.create(QueueCreate{Source: playqueue.QueueSource{HomeRow: &playqueue.HomeRowSource{ID: "popular", Library: library}}, Shuffle: shuffle}).Queue
		if !q.Refills {
			t.Fatal("Popular did not become a refillable queue")
		}
		last := row.Items[len(row.Items)-1].ID
		th.clock.Add(time.Minute)
		play(leaves[last][0], 10)
		if changed, _ := popular(); changed.Items[0].ID != last {
			t.Fatal("new plays did not change the live Popular order")
		}
		got := playAll(q)
		if shuffle {
			slices.Sort(got)
			slices.Sort(want)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("Popular shuffle=%t: %v; want %v", shuffle, got, want)
		}
	}
	_, popularOrder := popular()
	from := first[8]
	popularQueue := th.create(QueueCreate{Source: playqueue.QueueSource{HomeRow: &playqueue.HomeRowSource{ID: "popular", Library: library}}, StartAt: from}).Queue
	if got, want := playAll(popularQueue), popularOrder[slices.Index(popularOrder, from):]; !slices.Equal(got, want) {
		t.Fatalf("Popular from a track: %v; want %v", got, want)
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
