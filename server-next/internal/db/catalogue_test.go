package db_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

var ctx = context.Background()

// library is a small seeded database with ten profiles, three of them restricted.
type library struct {
	d       *db.DB
	cfg     seed.Config
	viewers []db.Viewer
	all     db.Viewer // sees everything
	libs    []db.Library
}

func newLibrary(t *testing.T, items int) *library {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	l := &library{d: d, cfg: seed.Config{Seed: 7, Items: items}}
	if _, err := seed.Import(ctx, d, l.cfg, nil); err != nil {
		t.Fatal(err)
	}
	if l.viewers, err = seed.Profiles(ctx, d, 10); err != nil {
		t.Fatal(err)
	}
	l.all = l.viewers[0]
	if l.libs, err = d.Libraries(ctx, l.all); err != nil || len(l.libs) != 4 {
		t.Fatalf("libraries: %d, %v", len(l.libs), err)
	}
	return l
}

// everything pages through a whole list and fails if a row repeats.
func everything(t *testing.T, d *db.DB, v db.Viewer, q db.ItemQuery) []db.Item {
	t.Helper()
	var out []db.Item
	seen := map[string]bool{}
	for {
		page, err := d.Items(ctx, v, q)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		for _, item := range page.Items {
			if seen[item.PublicID] {
				t.Fatalf("%+v: %s (%s) was sent twice", q, item.PublicID, item.Title)
			}
			seen[item.PublicID] = true
		}
		out = append(out, page.Items...)
		if page.NextCursor == "" {
			return out
		}
		q.Cursor = page.NextCursor
	}
}

// The seeder's output passes SQLite's checks, holds the awkward cases it promises, and
// importing the same tree again writes nothing: every folder, file and item is found by its key.
func TestImportIsSoundAndRepeatable(t *testing.T) {
	l := newLibrary(t, 3000)
	if rows, err := seed.State(ctx, l.d, l.viewers, 300, 7); err != nil || rows < 2000 {
		t.Fatalf("seeding state wrote %d rows: %v", rows, err)
	}
	if err := l.d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := seed.Import(ctx, l.d, l.cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.RowsWritten != 0 || again.ItemsNew != 0 || again.FilesNew != 0 || again.FilesChanged != 0 || again.Files == 0 {
		t.Fatalf("a second import of the same tree changed something: %+v", again)
	}

	var twoVersions, split, twoEpisodes, cue bool
	shared := map[int64]int{} // how many items play a range of the same file
	for _, lib := range l.libs {
		for _, item := range everything(t, l.d, l.all, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{seed.LeafKind(lib.Kind)}, Limit: 200}) {
			detail, err := l.d.Item(ctx, l.all, item.PublicID)
			if err != nil {
				t.Fatal(err)
			}
			if len(detail.Versions) == 0 || len(detail.Versions[0].Parts) == 0 {
				t.Fatalf("%s %q has nothing to play", item.Kind, item.Title)
			}
			twoVersions = twoVersions || len(detail.Versions) == 2
			split = split || len(detail.Versions[0].Parts) == 2
			if part := detail.Versions[0].Parts[0]; part.StartMS != nil {
				shared[part.FileID]++
				twoEpisodes = twoEpisodes || item.Kind == db.KindEpisode && shared[part.FileID] == 2
				cue = cue || item.Kind == db.KindTrack && shared[part.FileID] > 2
			}
		}
	}
	if !twoVersions || !split || !twoEpisodes || !cue {
		t.Fatalf("missing a case: two versions %v, split film %v, two episodes in a file %v, cue album %v", twoVersions, split, twoEpisodes, cue)
	}
}

// Every sort, in both directions, sends each row exactly once, in order, and the total is the
// real count. Films without a release date come last either way.
func TestPagingSendsEveryRowOnceInOrder(t *testing.T) {
	l := newLibrary(t, 1500)
	films := l.libs[0].PublicID
	want := len(everything(t, l.d, l.all, db.ItemQuery{Library: films, Kinds: []db.ItemKind{db.KindMovie}, Limit: 200}))
	if want != l.cfg.Leaves(db.LibraryMovies) {
		t.Fatalf("%d films listed, %d made", want, l.cfg.Leaves(db.LibraryMovies))
	}
	for _, sort := range []string{db.SortTitle, db.SortAdded, db.SortYear, db.SortRelease} {
		for _, desc := range []bool{false, true} {
			q := db.ItemQuery{Library: films, Kinds: []db.ItemKind{db.KindMovie}, Sort: sort, Desc: desc, Limit: 37, WithTotal: true}
			page, err := l.d.Items(ctx, l.all, q)
			if err != nil {
				t.Fatal(err)
			}
			rows := everything(t, l.d, l.all, q)
			if len(rows) != want || page.Total != want {
				t.Errorf("sort %s desc %v: %d rows, total %d, want %d", sort, desc, len(rows), page.Total, want)
			}
			key := func(i db.Item) int64 {
				switch sort {
				case db.SortAdded:
					return i.AddedAt
				case db.SortYear:
					return int64(i.Year)
				}
				return 0
			}
			undated := false
			for n := 1; n < len(rows); n++ {
				if a, b := key(rows[n-1]), key(rows[n]); !desc && a > b || desc && a < b {
					t.Fatalf("sort %s desc %v: row %d is out of order", sort, desc, n)
				}
				if sort == db.SortRelease {
					if undated && rows[n].ReleaseDate != "" {
						t.Fatalf("sort release desc %v: a dated film came after an undated one", desc)
					}
					undated = undated || rows[n].ReleaseDate == ""
				}
			}
		}
	}
	// A season lists its episodes in episode order by default.
	shows := everything(t, l.d, l.all, db.ItemQuery{Library: l.libs[1].PublicID, Kinds: []db.ItemKind{db.KindShow}})
	seasons := everything(t, l.d, l.all, db.ItemQuery{Parent: shows[0].PublicID})
	episodes := everything(t, l.d, l.all, db.ItemQuery{Parent: seasons[0].PublicID, Limit: 4})
	for n, episode := range episodes {
		if episode.IndexNumber == nil || *episode.IndexNumber != n+1 {
			t.Fatalf("episode %d of the first season is numbered %v", n+1, episode.IndexNumber)
		}
	}
}

var ratingAges = map[string]int{"G": 0, "PG": 8, "PG-13": 13, "R": 17, "NC-17": 18}

// A restricted profile never receives a restricted title: not in a list, a total, a detail, a
// person's page or a person's count, over a few thousand random queries. What each profile may
// see is worked out here from what the unrestricted profile sees, not by the query under test.
func TestRestrictedProfileNeverReceivesARestrictedTitle(t *testing.T) {
	l := newLibrary(t, 3000)
	byRating, byLibrary := l.viewers[9], l.viewers[8]

	type fact struct {
		item    db.Item
		library string
		rating  string
		labels  []string
		people  []string
		genres  []string // its own and those of the title it belongs to
	}
	facts := map[string]*fact{}
	var ids, people []string
	for _, lib := range l.libs {
		for _, kind := range []db.ItemKind{db.KindMovie, db.KindShow, db.KindSeason, db.KindEpisode, db.KindArtist, db.KindAlbum, db.KindTrack, db.KindBook} {
			for _, item := range everything(t, l.d, l.all, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind}, Limit: 200}) {
				detail, err := l.d.Item(ctx, l.all, item.PublicID)
				if err != nil {
					t.Fatal(err)
				}
				f := &fact{item: item, library: lib.Kind, rating: detail.ContentRating}
				for _, tag := range detail.Tags {
					if tag.Kind == db.TagLabel {
						f.labels = append(f.labels, tag.PublicID)
					}
					if tag.Kind == db.TagGenre {
						f.genres = append(f.genres, tag.Name)
					}
				}
				for _, credit := range detail.Credits {
					f.people = append(f.people, credit.PersonID)
					people = append(people, credit.PersonID)
				}
				if detail.Root != nil { // a show's rating and labels cover its seasons and episodes
					root := facts[detail.Root.PublicID]
					f.rating, f.labels = root.rating, append(f.labels, root.labels...)
					f.genres = append(f.genres, root.genres...)
				}
				facts[item.PublicID] = f
				ids = append(ids, item.PublicID)
			}
		}
	}
	// The third profile has a label blocked that some show carries, so its episodes must go too.
	var blocked string
	for _, f := range facts {
		if f.item.Kind == db.KindShow && len(f.labels) > 0 {
			blocked = f.labels[0]
		}
	}
	if blocked == "" {
		t.Fatal("the test library has no labelled show")
	}
	byLabel, err := l.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "no-label", BlockedTags: []string{blocked}})
	if err != nil {
		t.Fatal(err)
	}
	allowed := func(v db.Viewer, f *fact) bool {
		switch v {
		case byRating:
			age, rated := ratingAges[f.rating]
			return rated && age <= 13
		case byLibrary:
			return f.library == db.LibraryMovies || f.library == db.LibraryMusic
		case byLabel:
			return !slices.Contains(f.labels, blocked)
		}
		return true
	}
	titles := func(v db.Viewer, person string) (n int) {
		for _, f := range facts {
			if allowed(v, f) && slices.Contains(f.people, person) {
				n++
			}
		}
		return n
	}

	r := rand.New(rand.NewPCG(1, 2))
	yes, no := true, false
	for _, v := range []db.Viewer{byRating, byLibrary, byLabel} {
		hidden := 0
		for _, f := range facts {
			if !allowed(v, f) {
				hidden++
			}
		}
		if hidden == 0 || hidden == len(facts) {
			t.Fatalf("the test library hides %d of %d titles from a restricted profile; it should hide some", hidden, len(facts))
		}
		for range 1200 {
			id := ids[r.IntN(len(ids))]
			f := facts[id]
			switch r.IntN(4) {
			case 0: // detail
				_, err := l.d.Item(ctx, v, id)
				if allowed(v, f) != (err == nil) || err != nil && !errors.Is(err, db.ErrNotFound) {
					t.Fatalf("detail of %q (allowed %v): %v", f.item.Title, allowed(v, f), err)
				}
			case 1: // a person and their count
				person := people[r.IntN(len(people))]
				got, err := l.d.Person(ctx, v, person)
				if want := titles(v, person); got.Titles != want || (want == 0) != errors.Is(err, db.ErrNotFound) {
					t.Fatalf("person %s: %d titles, %v; want %d", person, got.Titles, err, want)
				}
				for _, item := range everything(t, l.d, v, db.ItemQuery{Person: person}) {
					if !allowed(v, facts[item.PublicID]) {
						t.Fatalf("a person's titles included restricted %q", item.Title)
					}
				}
			default: // a list with random filters, and its total
				lib := l.libs[r.IntN(len(l.libs))]
				q := db.ItemQuery{Library: lib.PublicID, Limit: 1 + r.IntN(60), WithTotal: true, Desc: r.IntN(2) == 0,
					Sort: []string{db.SortTitle, db.SortAdded, db.SortYear, db.SortRelease}[r.IntN(4)]}
				kinds := map[string][]db.ItemKind{db.LibraryMovies: {db.KindMovie}, db.LibraryShows: {db.KindShow, db.KindSeason, db.KindEpisode},
					db.LibraryMusic: {db.KindArtist, db.KindAlbum, db.KindTrack}, db.LibraryBooks: {db.KindBook}}[lib.Kind]
				q.Kinds = kinds[r.IntN(len(kinds)):][:1]
				matches := func(f *fact) bool { return f.library == lib.Kind && f.item.Kind == q.Kinds[0] }
				switch r.IntN(6) {
				case 0:
					if len(f.genres) > 0 {
						genre := f.genres[0]
						q.Genre = genre
						inner := matches
						matches = func(f *fact) bool { return inner(f) && slices.Contains(f.genres, genre) }
					}
					// Common tags and rare tags are read by different statements; try both.
					db.SetRareTag([]int{1, 2000}[r.IntN(2)])
				case 1:
					q.YearFrom, q.YearTo = 1980, 2005
					inner := matches
					matches = func(f *fact) bool { return inner(f) && f.item.Year >= 1980 && f.item.Year <= 2005 }
				case 2:
					q.Watched = []*bool{&yes, &no}[r.IntN(2)]
				case 3:
					q.Favorite = &no
				case 4:
					q.Resolution = []string{"sd", "720p", "1080p", "4k"}[r.IntN(4)]
				}
				page, err := l.d.Items(ctx, v, q)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range page.Items {
					if !allowed(v, facts[item.PublicID]) {
						t.Fatalf("list %+v included restricted %q", q, item.Title)
					}
				}
				want, exact := 0, q.Watched == nil && q.Resolution == ""
				for _, f := range facts {
					if matches(f) && allowed(v, f) {
						want++
					}
				}
				if exact && page.Total != want || page.Total > want {
					t.Fatalf("list %+v: total %d, but the profile may see %d", q, page.Total, want)
				}
			}
		}
	}
}

func position(ms int64) *int64 { return &ms }

func state(t *testing.T, d *db.DB, v db.Viewer, item string, edit db.StateEdit) db.State {
	t.Helper()
	s, _, err := d.SetState(ctx, v, item, edit)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The later edit wins field by field, by when each edit was made and not by when it arrived;
// a progress report never undoes a later deliberate mark; a repeat changes nothing.
func TestStateLaterEditWinsFieldByField(t *testing.T) {
	l := newLibrary(t, 300)
	v, yes, no := l.all, true, false
	films := everything(t, l.d, v, db.ItemQuery{Library: l.libs[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}})
	film := films[0].PublicID

	// A phone marks it a favourite now. Then a tablet syncs an edit it made an hour ago while
	// offline: not a favourite, and on My List. Each field keeps its later edit.
	state(t, l.d, v, film, db.StateEdit{Favorite: &yes})
	s := state(t, l.d, v, film, db.StateEdit{Favorite: &no, InList: &yes, Ago: time.Hour})
	if !s.Favorite || !s.InList {
		t.Fatalf("after an older offline edit: favourite %v, on My List %v; want both", s.Favorite, s.InList)
	}
	// An edit made half an hour ago is later than the one made an hour ago.
	if s = state(t, l.d, v, film, db.StateEdit{InList: &no, Ago: 30 * time.Minute}); s.InList {
		t.Fatal("a later offline edit did not replace an earlier one")
	}

	// Watched now; then playback progress from ten minutes ago arrives late. It must not make
	// the film unwatched or bring a resume point back.
	state(t, l.d, v, film, db.StateEdit{Watched: &yes})
	s = state(t, l.d, v, film, db.StateEdit{PositionMS: position(60_000), Ago: 10 * time.Minute})
	if !s.Watched || s.PositionMS != 0 {
		t.Fatalf("late progress undid a deliberate mark: watched %v, position %d", s.Watched, s.PositionMS)
	}
	// Progress reported after the mark is a rewatch: the position moves and watched stays.
	if s = state(t, l.d, v, film, db.StateEdit{PositionMS: position(90_000)}); !s.Watched || s.PositionMS != 90_000 {
		t.Fatalf("progress after a mark: watched %v, position %d", s.Watched, s.PositionMS)
	}
	// Unwatched with a resume point, sent twice: the same state both times.
	edit := db.StateEdit{Watched: &no, PositionMS: position(5_000)}
	first, second := state(t, l.d, v, film, edit), state(t, l.d, v, film, edit)
	if first.Watched || first.PositionMS != 5_000 || second.Watched != first.Watched || second.PositionMS != first.PositionMS {
		t.Fatalf("a repeated edit changed the state: %+v then %+v", first, second)
	}

	rating := 11
	if _, _, err := l.d.SetState(ctx, v, film, db.StateEdit{Rating: &rating}); !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("a rating of 11: %v", err)
	}
	if _, _, err := l.d.SetState(ctx, v, film, db.StateEdit{Watched: &yes, PositionMS: position(5)}); !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("watched with a position: %v", err)
	}
	// Each change was announced to this profile's open screens.
	changes, reread, head, err := l.d.Changes(ctx, 0, 100000)
	if err != nil || reread || len(changes) == 0 || changes[len(changes)-1].ID != head {
		t.Fatalf("changes: %d rows, reread %v, %v", len(changes), reread, err)
	}
	if last := changes[len(changes)-1]; last.Topic != db.TopicState || last.ProfileID != v.ProfileID {
		t.Fatalf("the last change is %+v, want this profile's state", last)
	}
}

// Marking a show watched marks its episodes in one go, and the undo token puts back exactly
// what was there, except where an episode has been edited since.
func TestStateOnAShowSetsItsEpisodesAndUndoes(t *testing.T) {
	l := newLibrary(t, 600)
	v, yes := l.all, true
	shows := everything(t, l.d, v, db.ItemQuery{Library: l.libs[1].PublicID, Kinds: []db.ItemKind{db.KindShow}})
	var show db.Item
	for _, s := range shows {
		if s.Leaves >= 4 {
			show = s
			break
		}
	}
	episodes := func() []db.Item {
		var out []db.Item
		for _, season := range everything(t, l.d, v, db.ItemQuery{Parent: show.PublicID}) {
			out = append(out, everything(t, l.d, v, db.ItemQuery{Parent: season.PublicID})...)
		}
		return out
	}
	before := episodes()
	state(t, l.d, v, before[0].PublicID, db.StateEdit{Watched: &yes})                // already watched
	state(t, l.d, v, before[1].PublicID, db.StateEdit{PositionMS: position(30_000)}) // part-way

	s, undo, err := l.d.SetState(ctx, v, show.PublicID, db.StateEdit{Watched: &yes})
	if err != nil || !s.Watched || undo == "" {
		t.Fatalf("marking the show: watched %v, undo %q, %v", s.Watched, undo, err)
	}
	for _, episode := range episodes() {
		if !episode.State.Watched || episode.State.PositionMS != 0 {
			t.Fatalf("episode %q after marking the show: %+v", episode.Title, episode.State)
		}
	}
	listed, err := l.d.Items(ctx, v, db.ItemQuery{Library: l.libs[1].PublicID, Kinds: []db.ItemKind{db.KindShow}, Watched: &yes, Limit: 200})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].PublicID != show.PublicID {
		t.Fatalf("the watched filter on shows found %d shows, %v", len(listed.Items), err)
	}

	// Another profile cannot use the token, and marking a watched show again offers nothing.
	if err := l.d.UndoState(ctx, l.viewers[1], undo); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("another profile's undo: %v", err)
	}
	if _, undo, _ = l.d.SetState(ctx, v, show.PublicID, db.StateEdit{Watched: &yes}); undo != "" {
		t.Fatal("marking an already watched show offered an undo for nothing")
	}
}

// Undo puts back exactly what each track held before the mark, except a track that has been
// edited since: that later edit stands. A token works once.
func TestUndoPutsBackWhatWasThere(t *testing.T) {
	l := newLibrary(t, 600)
	v, yes := l.all, true
	albums := everything(t, l.d, v, db.ItemQuery{Library: l.libs[2].PublicID, Kinds: []db.ItemKind{db.KindAlbum}})
	album := albums[0].PublicID
	tracks := everything(t, l.d, v, db.ItemQuery{Parent: album})
	state(t, l.d, v, tracks[0].PublicID, db.StateEdit{Watched: &yes})
	state(t, l.d, v, tracks[1].PublicID, db.StateEdit{PositionMS: position(30_000)})

	_, undo, err := l.d.SetState(ctx, v, album, db.StateEdit{Watched: &yes})
	if err != nil || undo == "" {
		t.Fatalf("marking the album: undo %q, %v", undo, err)
	}
	time.Sleep(2 * time.Millisecond)
	state(t, l.d, v, tracks[2].PublicID, db.StateEdit{Watched: &yes}) // played again after the mark
	if err := l.d.UndoState(ctx, v, undo); err != nil {
		t.Fatal(err)
	}
	after := everything(t, l.d, v, db.ItemQuery{Parent: album})
	if !after[0].State.Watched || after[1].State.Watched || after[1].State.PositionMS != 30_000 ||
		!after[2].State.Watched || after[3].State.Watched {
		t.Fatalf("after undo: %+v, %+v, %+v, %+v", after[0].State, after[1].State, after[2].State, after[3].State)
	}
	if err := l.d.UndoState(ctx, v, undo); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("a used undo token: %v", err)
	}
}
