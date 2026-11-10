package db_test

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// shelf is a small hand-made library with search running. dir holds main.db and search.db.
type shelf struct {
	t      *testing.T
	dir    string
	d      *db.DB
	v      db.Viewer
	films  db.Library
	shows  db.Library
	music  db.Library
	nextID int
}

// open opens the two files in the shelf's folder; search is left out when asked.
func (s *shelf) open(search bool) {
	s.t.Helper()
	d, err := db.Open(ctx, filepath.Join(s.dir, "main.db"))
	if err != nil {
		s.t.Fatal(err)
	}
	if search {
		d.StartSearch(filepath.Join(s.dir, "search.db"), slog.New(slog.DiscardHandler))
	}
	s.d = d
}

func (s *shelf) add(lib db.Library, items ...db.ImportItem) {
	s.t.Helper()
	for n := range items {
		if len(items[n].Keys) == 0 {
			s.nextID++
			items[n].Keys = []db.ItemKey{{Kind: db.KeyFolder, Key: fmt.Sprint("made-", s.nextID)}}
		}
	}
	if _, err := s.d.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Items: items}); err != nil {
		s.t.Fatal(err)
	}
}

func film(title string, people ...string) db.ImportItem {
	it := db.ImportItem{Kind: db.KindMovie, Title: title}
	for _, name := range people {
		it.Credits = append(it.Credits, db.ImportCredit{Name: name, Role: db.RoleActor})
	}
	return it
}

func key(name string) db.ItemKey { return db.ItemKey{Kind: db.KeyName, Key: name} }

func newShelf(t *testing.T) *shelf {
	t.Helper()
	s := &shelf{t: t, dir: t.TempDir()}
	s.open(true)
	t.Cleanup(func() { s.d.Close() })
	var err error
	for _, lib := range []struct {
		into *db.Library
		kind string
	}{{&s.films, db.LibraryMovies}, {&s.shows, db.LibraryShows}, {&s.music, db.LibraryMusic}} {
		if *lib.into, err = s.d.CreateLibrary(ctx, lib.kind, lib.kind, "en", []string{"/media/" + lib.kind}); err != nil {
			t.Fatal(err)
		}
	}
	if s.v, err = s.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "viewer"}); err != nil {
		t.Fatal(err)
	}
	spirited := film("Spirited Away")
	spirited.OriginalTitle = "千と千尋の神隠し"
	s.add(s.films, film("Amélie", "Zoë Saldaña"), film("Spider-Man: Homecoming"), film("The Amazing Spider-Man"),
		film("The Godfather Part II"), film("The Godfather", "Marlon Brando"), film("WALL·E"), film("Don't Look Up"),
		film("Father of the Bride"), spirited)
	show, season := key("breaking bad"), key("breaking bad/1")
	s.add(s.shows,
		db.ImportItem{Kind: db.KindShow, Keys: []db.ItemKey{show}, Title: "Breaking Bad"},
		db.ImportItem{Kind: db.KindSeason, Keys: []db.ItemKey{season}, Parent: &show, Title: "Season 5"},
		db.ImportItem{Kind: db.KindEpisode, Parent: &season, Title: "Ozymandias"},
		db.ImportItem{Kind: db.KindEpisode, Parent: &season, Title: "Felina"})
	artist, album := key("the beatles"), key("the beatles/help")
	s.add(s.music,
		db.ImportItem{Kind: db.KindArtist, Keys: []db.ItemKey{artist}, Title: "The Beatles"},
		db.ImportItem{Kind: db.KindAlbum, Keys: []db.ItemKey{album}, Parent: &artist, Title: "Help!"},
		db.ImportItem{Kind: db.KindTrack, Parent: &album, Title: "Yesterday"})
	return s
}

// search waits for the index to catch up and returns one group of a search.
func (s *shelf) search(text, kind string) db.SearchGroup {
	s.t.Helper()
	if err := s.d.SearchCaughtUp(ctx); err != nil {
		s.t.Fatal(err)
	}
	groups, err := s.d.Search(ctx, s.v, db.SearchQuery{Text: text})
	if err != nil {
		s.t.Fatalf("search %q: %v", text, err)
	}
	for _, g := range groups {
		if g.Kind == kind {
			return g
		}
	}
	s.t.Fatalf("search %q returned no %s group", text, kind)
	return db.SearchGroup{}
}

func names(g db.SearchGroup) string {
	var out []string
	for _, item := range g.Items {
		out = append(out, item.Title)
	}
	for _, person := range g.People {
		out = append(out, person.Name)
	}
	return strings.Join(out, " | ")
}

// expect checks that a search finds exactly these titles of a kind, in this order.
func (s *shelf) expect(text, kind, want string) {
	s.t.Helper()
	g := s.search(text, kind)
	if got := names(g); got != want {
		s.t.Errorf("search %q, %s: got %q, want %q", text, kind, got, want)
	}
	if want := len(g.Items) + len(g.People); g.Total != want || g.TotalIsMinimum {
		s.t.Errorf("search %q, %s: total %d (minimum %v), want %d", text, kind, g.Total, g.TotalIsMinimum, want)
	}
}

// Search matches whole words and the beginnings of words, ignoring case, accents and
// punctuation, never the middle of a word and never a misspelling, and puts the best match first.
func TestSearchMatchesWordsAndTheirBeginnings(t *testing.T) {
	s := newShelf(t)
	for _, c := range []struct{ text, kind, want string }{
		{"amelie", "movie", "Amélie"},
		{"AMÉLIE", "movie", "Amélie"},
		{"am", "movie", "Amélie | The Amazing Spider-Man"},
		{"spider man", "movie", "Spider-Man: Homecoming | The Amazing Spider-Man"},
		{"spiderman", "movie", "Spider-Man: Homecoming | The Amazing Spider-Man"},
		{"Spider-Man", "movie", "Spider-Man: Homecoming | The Amazing Spider-Man"},
		{"spider-ma", "movie", "Spider-Man: Homecoming | The Amazing Spider-Man"},
		{"man spider", "movie", "Spider-Man: Homecoming | The Amazing Spider-Man"},
		{"the godfather", "movie", "The Godfather | The Godfather Part II"}, // the exact title first, though it was added later
		{"godf", "movie", "The Godfather Part II | The Godfather"},
		{"the godfather part", "movie", "The Godfather Part II"},
		{"father", "movie", "Father of the Bride"}, // not the middle of "Godfather"
		{"godfahter", "movie", ""},                 // typing mistakes are not corrected
		{"wall-e", "movie", "WALL·E"},
		{"walle", "movie", "WALL·E"},
		{"wall e", "movie", "WALL·E"},
		{"dont look", "movie", "Don't Look Up"},
		{"don't look up", "movie", "Don't Look Up"},
		{"千尋", "movie", "Spirited Away"}, // through its original title
		{"breaking bad", "show", "Breaking Bad"},
		{"breaking", "episode", "Ozymandias | Felina"}, // through the show's name
		{"breaking ozy", "episode", "Ozymandias"},
		{"season", "episode", ""}, // a season's name says nothing
		{"beatles", "artist", "The Beatles"},
		{"beatles", "album", "Help!"},
		{"beatles yesterday", "track", "Yesterday"},
		{"help", "track", "Yesterday"},
		{"zoe", "person", "Zoë Saldaña"},
		{"saldana zoe", "person", "Zoë Saldaña"},
		{"brando", "person", "Marlon Brando"},
		{"brando", "movie", ""}, // a film is not matched by its cast; the person is
	} {
		s.expect(c.text, c.kind, c.want)
	}
	for _, text := range []string{"", "   ", "!!!", strings.Repeat("a", 129), "a b c d e f g h i"} {
		if _, err := s.d.Search(ctx, s.v, db.SearchQuery{Text: text}); !errors.Is(err, db.ErrInvalid) {
			t.Errorf("search %q: %v, want a refusal", text, err)
		}
	}

	// One kind, a page at a time, to its end; then sorted by title.
	var seen []string
	q := db.SearchQuery{Text: "the", Kind: "movie", Limit: 2}
	for {
		groups, err := s.d.Search(ctx, s.v, q)
		if err != nil {
			t.Fatal(err)
		}
		if q.Cursor == "" && (len(groups) != 9 || groups[0].Total != 4 || groups[4].Kind != "artist" || groups[4].Total != 1) {
			t.Fatalf("the first page of one kind carries every kind's total: %+v", groups)
		}
		for _, item := range groups[0].Items {
			seen = append(seen, item.Title)
		}
		if q.Cursor = groups[0].NextCursor; q.Cursor == "" {
			break
		}
	}
	if got := strings.Join(seen, " | "); got != "The Amazing Spider-Man | The Godfather Part II | The Godfather | Father of the Bride" {
		t.Errorf("paging through films matching \"the\": %s", got)
	}
	groups, err := s.d.Search(ctx, s.v, db.SearchQuery{Text: "the", Kind: "movie", Sort: db.SortTitle})
	if got := names(groups[0]); err != nil || got != "The Amazing Spider-Man | Father of the Bride | The Godfather | The Godfather Part II" {
		t.Errorf("films matching \"the\" by title: %s, %v", got, err)
	}

	suggestions, err := s.d.SearchSuggestions(ctx, s.v, "b", 5)
	var offered []string
	for _, sug := range suggestions {
		offered = append(offered, sug.Kind+" "+sug.Title)
	}
	if got := strings.Join(offered, " | "); err != nil || got != "show Breaking Bad | movie Father of the Bride | person Marlon Brando | artist The Beatles" {
		t.Errorf("suggestions for \"b\": %s, %v", got, err)
	}
}

// A new, a renamed and a deleted title each reach search, and so does everything that
// carries a renamed or deleted title's name.
func TestSearchFollowsChanges(t *testing.T) {
	s := newShelf(t)
	id := func(text, kind string) string {
		t.Helper()
		g := s.search(text, kind)
		if len(g.Items) != 1 {
			t.Fatalf("search %q, %s: %q, want one title", text, kind, names(g))
		}
		return g.Items[0].PublicID
	}
	s.expect("zardoz", "movie", "")
	s.add(s.films, film("Zardoz", "Sean Connery"))
	s.expect("zardoz", "movie", "Zardoz")
	s.expect("connery", "person", "Sean Connery")

	if err := s.d.RenameItem(ctx, id("zardoz", "movie"), "Solaris"); err != nil {
		t.Fatal(err)
	}
	s.expect("zardoz", "movie", "")
	s.expect("solaris", "movie", "Solaris")

	if err := s.d.DeleteItem(ctx, id("solaris", "movie")); err != nil {
		t.Fatal(err)
	}
	s.expect("solaris", "movie", "")

	if err := s.d.RenameItem(ctx, id("breaking bad", "show"), "Better Call Saul"); err != nil {
		t.Fatal(err)
	}
	s.expect("breaking", "episode", "")
	s.expect("saul ozymandias", "episode", "Ozymandias")

	if err := s.d.DeleteItem(ctx, id("better call saul", "show")); err != nil {
		t.Fatal(err)
	}
	s.expect("ozymandias", "episode", "")
	s.expect("saul", "episode", "")
}

// The answers a shelf gives after the edits below, whatever state its index was in before.
func (s *shelf) expectEdited() {
	s.t.Helper()
	s.expect("zardoz", "movie", "Zardoz")
	s.expect("amelie", "movie", "")
	s.expect("delicatessen", "movie", "Delicatessen")
	s.expect("godfather", "movie", "The Godfather Part II")
	s.expect("the", "movie", "The Amazing Spider-Man | The Godfather Part II | Father of the Bride")
}

// A stop between a write to the main database and the index hearing of it, at any point,
// leaves search correct once it has caught up: whether the index is merely behind, or is an
// older copy that is given changes it has already seen.
func TestSearchCatchesUpAfterAStop(t *testing.T) {
	s := newShelf(t)
	amelie, godfather := s.search("amelie", "movie").Items[0].PublicID, s.search("the godfather", "movie").Items[0].PublicID
	s.d.Close()
	index := filepath.Join(s.dir, "search.db")
	early, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}

	// The server ran and stopped before search heard of any of this.
	s.open(false)
	s.add(s.films, film("Zardoz"))
	if err := errors.Join(s.d.RenameItem(ctx, amelie, "Delicatessen"), s.d.DeleteItem(ctx, godfather)); err != nil {
		t.Fatal(err)
	}
	s.d.Close()

	s.open(true)
	s.expectEdited()
	s.d.Close()

	// The index is put back as it was before those changes, with the newer file's leftovers
	// gone, and is given all of them again.
	if err := os.WriteFile(index, early, 0o600); err != nil {
		t.Fatal(err)
	}
	s.open(true)
	s.expectEdited()
}

// The index is rebuilt when it is missing, when it is not a file search can read, when it
// was folded under other rules, and when the changes it needs are gone. Searches are answered
// throughout a rebuild of an index that could already answer.
func TestSearchRebuildsItself(t *testing.T) {
	s := newShelf(t)
	amelie, godfather := s.search("amelie", "movie").Items[0].PublicID, s.search("the godfather", "movie").Items[0].PublicID
	s.add(s.films, film("Zardoz"))
	if err := errors.Join(s.d.RenameItem(ctx, amelie, "Delicatessen"), s.d.DeleteItem(ctx, godfather)); err != nil {
		t.Fatal(err)
	}
	s.expectEdited()
	index := filepath.Join(s.dir, "search.db")
	replaced := func(old os.FileInfo) {
		t.Helper()
		for range 5000 {
			if now, err := os.Stat(index); err == nil && !os.SameFile(old, now) {
				return
			}
			s.d.SearchCaughtUp(ctx)
		}
		t.Fatal("the index was not rebuilt")
	}

	s.d.Close()
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	s.open(true)
	s.expectEdited()

	s.d.Close()
	if err := os.WriteFile(index, []byte("this is not a database, however long it is made to be"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.open(true)
	s.expectEdited()

	// Built by a server with other Unicode tables: it answers at once and is replaced.
	s.d.Close()
	old, _ := os.Stat(index)
	defer db.SetSearchFolding("other rules")()
	s.open(true)
	if _, err := s.d.Search(ctx, s.v, db.SearchQuery{Text: "zardoz"}); err != nil {
		t.Fatalf("an index folded under other rules must keep answering: %v", err)
	}
	replaced(old)
	s.expectEdited()

	// The server ran without search for so long that the changes it missed were pruned.
	s.d.Close()
	defer db.SetChangesKeepRows(5)()
	s.open(false)
	for n := range 20 {
		s.add(s.films, film(fmt.Sprint("Sequel ", n)))
	}
	if deleted, err := s.d.PruneChanges(ctx); err != nil || deleted == 0 {
		t.Fatalf("pruned %d changes: %v", deleted, err)
	}
	s.d.Close()
	old, _ = os.Stat(index)
	s.open(true)
	replaced(old)
	s.expectEdited()
	s.expect("sequel 19", "movie", "Sequel 19")

	// Asked for by hand, with people searching all the while.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	failed := make(chan error, 1)
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			groups, err := s.d.Search(ctx, s.v, db.SearchQuery{Text: "zardoz"})
			if err == nil && names(groups[0]) != "Zardoz" {
				err = fmt.Errorf("found %q", names(groups[0]))
			}
			if err != nil {
				select {
				case failed <- err:
				default:
				}
			}
		}
	})
	for range 3 {
		if err := s.d.RebuildSearch(ctx); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	select {
	case err := <-failed:
		t.Fatalf("a search during a rebuild: %v", err)
	default:
	}
	s.expectEdited()
}

// A restricted profile never receives a restricted title from search: not in a group, a
// page, a sorted page or a suggestion, and never in a count, over a few thousand random
// queries. What each profile may see is worked out here from what the unrestricted profile
// sees, not by the query under test.
func TestSearchNeverShowsARestrictedTitle(t *testing.T) {
	l := newLibrary(t, 3000)
	l.d.StartSearch(filepath.Join(t.TempDir(), "search.db"), slog.New(slog.DiscardHandler))
	byRating, byLibrary := l.viewers[9], l.viewers[8]

	type fact struct {
		library, rating string
		labels          []string
	}
	facts := map[string]*fact{}
	creditedOn := map[string][]*fact{} // by person
	var vocabulary []string
	for _, lib := range l.libs {
		for _, kind := range []db.ItemKind{db.KindMovie, db.KindShow, db.KindSeason, db.KindEpisode, db.KindArtist, db.KindAlbum, db.KindTrack, db.KindBook} {
			for _, item := range everything(t, l.d, l.all, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind}, Limit: 200}) {
				detail, err := l.d.Item(ctx, l.all, item.PublicID)
				if err != nil {
					t.Fatal(err)
				}
				f := &fact{library: lib.Kind, rating: detail.ContentRating}
				for _, tag := range detail.Tags {
					if tag.Kind == db.TagLabel {
						f.labels = append(f.labels, tag.PublicID)
					}
				}
				if detail.Root != nil { // a show's rating and labels cover its seasons and episodes
					f.rating, f.labels = facts[detail.Root.PublicID].rating, append(f.labels, facts[detail.Root.PublicID].labels...)
				}
				if detail.Parent != nil {
					f.labels = append(f.labels, facts[detail.Parent.PublicID].labels...)
				}
				for _, credit := range detail.Credits {
					creditedOn[credit.PersonID] = append(creditedOn[credit.PersonID], f)
					vocabulary = append(vocabulary, strings.Fields(credit.Name)...)
				}
				facts[item.PublicID] = f
				vocabulary = append(vocabulary, strings.Fields(item.Title)...)
			}
		}
	}
	var blocked string
	for _, f := range facts {
		if len(f.labels) > 0 {
			blocked = f.labels[0]
		}
	}
	byLabel, err := l.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "no-label", BlockedTags: []string{blocked}})
	if err != nil || blocked == "" {
		t.Fatalf("a profile with a blocked label: %v", err)
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
	if err := l.d.SearchCaughtUp(ctx); err != nil {
		t.Fatal(err)
	}
	// A cap this small makes wide searches stop checking early, as they do in a large library.
	defer db.SetSearchCheckCap(60)()

	r := rand.New(rand.NewPCG(3, 4))
	word := func() string {
		w := vocabulary[r.IntN(len(vocabulary))]
		return w[:1+r.IntN(len(w))]
	}
	sorts := []string{db.SearchBest, db.SortTitle, db.SortYear, db.SortAdded}
	kinds := []string{"movie", "show", "episode", "artist", "album", "track", "book"}
	queries, shown := 0, 0
	for _, v := range []db.Viewer{byRating, byLibrary, byLabel} {
		check := func(text string, g db.SearchGroup) {
			t.Helper()
			for _, item := range g.Items {
				if shown++; !allowed(v, facts[item.PublicID]) {
					t.Fatalf("search %q showed restricted %s %q", text, g.Kind, item.Title)
				}
			}
			for _, person := range g.People {
				if shown++; !slices.ContainsFunc(creditedOn[person.PublicID], func(f *fact) bool { return allowed(v, f) }) {
					t.Fatalf("search %q showed %q, who is on no title this profile may see", text, person.Name)
				}
			}
		}
		for range 200 {
			text := word()
			if r.IntN(3) == 0 {
				text += " " + word()
			}
			groups, err := l.d.Search(ctx, v, db.SearchQuery{Text: text})
			if err != nil {
				t.Fatalf("search %q: %v", text, err)
			}
			everyone, err := l.d.Search(ctx, l.all, db.SearchQuery{Text: text})
			if err != nil {
				t.Fatal(err)
			}
			queries += 2
			for n, g := range groups {
				check(text, g)
				if g.Total > everyone[n].Total || g.Total < len(g.Items)+len(g.People) && !g.TotalIsMinimum {
					t.Fatalf("search %q: %d %s results for a restricted profile, %d for everyone", text, g.Total, g.Kind, everyone[n].Total)
				}
			}
			// One kind, sorted some way, paged to its end: every row is allowed, and there are
			// as many as the count said.
			q := db.SearchQuery{Text: text, Kind: kinds[r.IntN(len(kinds))], Sort: sorts[r.IntN(len(sorts))], Desc: r.IntN(2) == 0, Limit: 25}
			if q.Kind == "artist" && q.Sort != db.SearchBest {
				q.Sort = db.SortTitle
			}
			total, rows, minimum := -1, 0, false
			for {
				groups, err := l.d.Search(ctx, v, q)
				if errors.Is(err, db.ErrInvalid) && q.Sort != db.SearchBest {
					total, rows = 0, 0 // too many matches to sort
					break
				}
				if err != nil {
					t.Fatalf("search %+v: %v", q, err)
				}
				queries++
				for _, g := range groups {
					if g.Kind == q.Kind {
						check(text, g)
						if rows += len(g.Items); g.Counted {
							total, minimum = g.Total, g.TotalIsMinimum
						}
						q.Cursor = g.NextCursor
					}
				}
				if q.Cursor == "" {
					break
				}
			}
			if rows < total || rows > total && !minimum {
				t.Fatalf("search %+v: counted %d (a minimum: %v), paged through %d", q, total, minimum, rows)
			}
			suggestions, err := l.d.SearchSuggestions(ctx, v, text, 20)
			if err != nil {
				t.Fatal(err)
			}
			queries++
			for _, sug := range suggestions {
				if f := facts[sug.PublicID]; sug.Kind != "person" && !allowed(v, f) {
					t.Fatalf("search %q suggested restricted %q", text, sug.Title)
				}
			}
		}
	}
	t.Logf("%d queries showed %d results", queries, shown)
	if queries < 2000 || shown < 6000 {
		t.Fatalf("%d queries showed %d results; the test should show many", queries, shown)
	}
}

// The recent searches are the last twenty, newest first, without repeats, per profile.
func TestSearchHistory(t *testing.T) {
	s := newShelf(t)
	other, err := s.d.CreateSeedProfile(ctx, db.SeedProfile{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	for n := range 25 {
		if err := s.d.RememberSearch(ctx, s.v, fmt.Sprint("query ", n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(s.d.RememberSearch(ctx, s.v, "  Query   20 "), s.d.RememberSearch(ctx, other, "theirs")); err != nil {
		t.Fatal(err)
	}
	history, err := s.d.SearchHistory(ctx, s.v)
	if err != nil || len(history) != 20 || history[0] != "Query 20" || history[1] != "query 24" || history[19] != "query 5" {
		t.Fatalf("history: %q, %v", history, err)
	}
	if err := s.d.ForgetSearches(ctx, s.v, "query 24"); err != nil {
		t.Fatal(err)
	}
	if history, _ = s.d.SearchHistory(ctx, s.v); len(history) != 19 || history[1] != "query 23" {
		t.Fatalf("history after forgetting one: %q", history)
	}
	if err := s.d.ForgetSearches(ctx, s.v, ""); err != nil {
		t.Fatal(err)
	}
	if history, _ = s.d.SearchHistory(ctx, s.v); len(history) != 0 {
		t.Fatalf("history after clearing: %q", history)
	}
	if history, _ = s.d.SearchHistory(ctx, other); len(history) != 1 {
		t.Fatalf("another profile's history: %q", history)
	}
}
