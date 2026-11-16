package api

import (
	"context"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

// An Albums view can exceed a queue even when it has only two cards. Its filters and
// container order apply before expansion; refill walks every song, including after restart.
func TestContainerViewsPlayAllTheirLeaves(t *testing.T) {
	th := newTheatre(t)
	db.QueueMax, playqueue.Fill, playqueue.RefillBelow = 12, 5, 2
	t.Cleanup(func() { db.QueueMax, playqueue.Fill, playqueue.RefillBelow = 10_000, 1000, 200 })
	_, first := th.album("Alpha", 20)
	_, second := th.album("Zulu", 13)
	view := &playqueue.QueueView{Library: th.libs[db.LibraryMusic].PublicID, Kinds: []string{"album"}, Desc: true}
	want := slices.Concat(second, first)
	for _, shuffle := range []bool{false, true} {
		q := th.create(QueueCreate{Source: playqueue.QueueSource{View: view}, Shuffle: shuffle}).Queue
		if !q.Refills || q.Total != 5 {
			t.Fatalf("two albums were counted as two queue entries: %+v", q)
		}
		var played []string
		for len(played) < len(want) {
			var page QueueEntries
			path := "/v1/queues/" + q.ID
			th.queue("GET", path+"/entries?around=current&before=0&after=0", "", nil, &page)
			if len(page.Items) != 1 {
				t.Fatalf("current entry: %+v", page)
			}
			played = append(played, page.Items[0].ItemID)
			if len(played) == 7 {
				// Refilling resumes from the saved selection, not a service's memory.
				th.s.queues = playqueue.New(th.s.db, nil, th.s.now)
			}
			w := th.queue("POST", path+"/advance", "", QueueAdvance{Reason: "completion"}, &q)
			if len(played) < len(want) && w.Code != 200 || len(played) == len(want) && w.Code != 409 {
				t.Fatalf("advance after %d titles: %d %s", len(played), w.Code, w.Body)
			}
		}
		if shuffle {
			slices.Sort(played)
			if !slices.Equal(played, slices.Sorted(slices.Values(want))) {
				t.Fatalf("shuffle repeated or skipped a leaf: %v", played)
			}
		} else if !slices.Equal(played, want) {
			t.Fatalf("container order, then track order: %v; want %v", played, want)
		}
	}
	// A chosen track and turning shuffle off resume inside its album, then the next album.
	q := th.create(QueueCreate{Source: playqueue.QueueSource{View: view}, StartAt: second[8]}).Queue
	if got := th.order(q.ID); !slices.Equal(got, slices.Concat(second[8:], first)[:len(got)]) {
		t.Fatalf("start within the first album: %v", got)
	}
	on, off := true, false
	th.queue("PATCH", "/v1/queues/"+q.ID, "", QueueChange{Shuffle: &on}, &q)
	th.queue("PATCH", "/v1/queues/"+q.ID, "", QueueChange{Shuffle: &off}, &q)
	if got := th.order(q.ID); !slices.Equal(got, slices.Concat(second[8:], first)[:len(got)]) {
		t.Fatalf("shuffle off lost the container/child position: %v", got)
	}
	// Filters apply to album cards, not to tracks (an album's year may differ from a song's).
	year := 2001
	cards := get[ItemPage](t, th.s, th.owner.AccessToken, "/v1/items?library="+view.Library+"&kind=album")
	for _, card := range cards.Items {
		if card.Title == "Alpha" {
			doc, err := th.s.db.MetadataDocument(context.Background(), card.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := th.s.db.EditMetadata(context.Background(), card.ID, doc.Revision, db.MetadataEdit{Year: &year}); err != nil {
				t.Fatal(err)
			}
		}
	}
	artistQuery := db.ItemQuery{Library: view.Library, Kinds: []db.ItemKind{db.KindArtist}}
	artistItems, _, err := th.s.db.ViewItems(context.Background(), db.Viewer{UserID: 1, ProfileID: 1}, artistQuery, 100)
	artistWant, errWant := th.s.db.PlayableItems(context.Background(), db.Viewer{UserID: 1, ProfileID: 1}, slices.Concat(second, first))
	if err != nil || errWant != nil || !slices.Equal(artistItems, artistWant) {
		t.Fatalf("artists play albums oldest first, then track order: %v, want %v (%v, %v)", artistItems, artistWant, err, errWant)
	}
	filtered := *view
	filtered.YearFrom = 2001
	made := th.create(QueueCreate{Source: playqueue.QueueSource{View: &filtered}})
	if !slices.Equal(th.order(made.Queue.ID), first[:5]) {
		t.Fatalf("album filter did not select its children: %v", th.order(made.Queue.ID))
	}
}

// The container must be visible as well as each leaf: an allowed episode cannot reveal a
// blocked show, and an adult episode is absent from both the count and shuffled traversal.
func TestContainerViewQueuesRespectBothLevelsOfLimits(t *testing.T) {
	th := newTheatre(t)
	ctx := context.Background()
	profile, err := th.s.db.CreateProfile(ctx, db.NewProfile{UserID: 1, Name: "Young", Now: th.s.now()})
	if err != nil {
		t.Fatal(err)
	}
	age := 13
	if _, err := th.s.db.SetProfileRestrictions(ctx, 1, profile.PublicID,
		db.Restrictions{AllLibraries: true, AllowUnrated: true, MaxRatingAge: &age}, th.s.now()); err != nil {
		t.Fatal(err)
	}
	limited := db.Viewer{UserID: 1, ProfileID: profile.ID}
	q := db.ItemQuery{Library: th.libs[db.LibraryShows].PublicID, Kinds: []db.ItemKind{db.KindShow}}
	editRating := func(id, rating string) {
		t.Helper()
		doc, err := th.s.db.MetadataDocument(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := th.s.db.EditMetadata(ctx, id, doc.Revision, db.MetadataEdit{ContentRating: &rating}); err != nil {
			t.Fatal(err)
		}
	}
	editRating(th.eps[1], "R")
	items, _, err := th.s.db.ViewItems(ctx, limited, q, 10)
	first, errFirst := th.s.db.PlayableItems(ctx, limited, []string{th.eps[0]})
	count, errCount := th.s.db.ViewPlayableCount(ctx, limited, q)
	shuffled, _, errShuffle := th.s.db.ShuffledItems(ctx, limited, q, 7, 0, 10)
	if err != nil || errFirst != nil || errCount != nil || errShuffle != nil || count != 1 || !slices.Equal(items, first) || !slices.Equal(shuffled, first) {
		t.Fatalf("limited leaves: %v / %v, count %d, shuffle %v; errors %v %v %v %v", items, first, count, shuffled, err, errFirst, errCount, errShuffle)
	}
	show, err := th.s.db.Items(ctx, db.Viewer{UserID: 1, ProfileID: 1}, q)
	if err != nil {
		t.Fatal(err)
	}
	editRating(show.Items[0].PublicID, "R")
	if items, _, err := th.s.db.ViewItems(ctx, limited, q, 10); err != nil || len(items) != 0 {
		t.Fatalf("a hidden holder exposed its leaves: %v %v", items, err)
	}
}
