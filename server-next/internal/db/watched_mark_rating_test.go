package db_test

// Review probes for #2473 (the stored watched mark). Drop this file into
// server-next/internal/db/ on the PR's branch rebased onto main and run:
//
//	GO_OFFLOAD_OFF=1 go test -p 2 -count=1 -run 'TestWatchedMarkFollowsARatingChange|TestWatchedMarkFollowsAMove' ./internal/db/
//
// TestWatchedMarkFollowsARatingChange fails today: a provider's answer (ApplyDetails) changes a
// show's rating, which changes what a rating-limited profile may see under the show, and nothing
// settles the mark. TestWatchedMarkFollowsAMove passes and can be kept or dropped.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

type markProbe struct {
	t       *testing.T
	d       *db.DB
	root    string
	lib     db.Library
	scanner *scan.Scanner
}

func newMarkProbe(t *testing.T, files ...string) *markProbe {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	p := &markProbe{t: t, d: d, root: t.TempDir(), scanner: &scan.Scanner{DB: d}}
	for n, rel := range files {
		p.write(rel, 101+n)
	}
	if p.lib, err = d.CreateLibrary(ctx, "Shows", db.LibraryShows, "en", []string{p.root}); err != nil {
		t.Fatal(err)
	}
	p.scan()
	return p
}

func (p *markProbe) write(rel string, size int) {
	p.t.Helper()
	full := filepath.Join(p.root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(full, make([]byte, size), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

func (p *markProbe) scan() {
	p.t.Helper()
	libraries, err := p.d.AllLibraries(context.Background())
	if err != nil {
		p.t.Fatal(err)
	}
	if _, err := p.scanner.Scan(context.Background(), libraries[0]); err != nil {
		p.t.Fatal(err)
	}
}

func (p *markProbe) items(v db.Viewer, q db.ItemQuery) []db.Item {
	p.t.Helper()
	q.Library, q.Limit = p.lib.PublicID, 50
	page, err := p.d.Items(context.Background(), v, q)
	if err != nil {
		p.t.Fatal(err)
	}
	return page.Items
}

func (p *markProbe) show(v db.Viewer, title string) db.Item {
	p.t.Helper()
	for _, show := range p.items(v, db.ItemQuery{Kinds: []db.ItemKind{db.KindShow}}) {
		if show.Title == title {
			return show
		}
	}
	p.t.Fatalf("no show %q", title)
	return db.Item{}
}

func (p *markProbe) rate(show db.Item, rating string, age int) {
	p.t.Helper()
	if err := p.d.ApplyDetails(context.Background(), db.Background, show.ID, db.ItemDetails{ContentRating: rating, RatingAge: &age}); err != nil {
		p.t.Fatal(err)
	}
}

func (p *markProbe) check(when string) {
	p.t.Helper()
	_, wrong, err := db.WatchedMarks(p.d)
	if err != nil {
		p.t.Fatal(err)
	}
	if len(wrong) > 0 {
		p.t.Errorf("%s: %d marks are wrong, the first: %s", when, len(wrong), wrong[0])
	}
}

func (p *markProbe) listedWatched(v db.Viewer, title string) bool {
	p.t.Helper()
	yes := true
	for _, show := range p.items(v, db.ItemQuery{Kinds: []db.ItemKind{db.KindShow}, Watched: &yes}) {
		if show.Title == title {
			return true
		}
	}
	return false
}

// A provider's answer changes a show's rating. A profile with a rating limit then sees more
// or fewer of its episodes, so its watched mark on the show must be settled in that write.
func TestWatchedMarkFollowsARatingChange(t *testing.T) {
	ctx := context.Background()
	p := newMarkProbe(t, "Bluey/Season 1/Bluey - S01E01.mkv", "Bluey/Season 1/Bluey - S01E02.mkv")
	viewers, err := seed.Profiles(ctx, p.d, 2)
	if err != nil {
		t.Fatal(err)
	}
	everyone, limited := viewers[0], viewers[1] // the second is limited to 13
	yes := true
	show := p.show(everyone, "Bluey")
	p.rate(show, "TV-Y7", 7)
	if _, _, err := p.d.SetState(ctx, limited, show.PublicID, db.StateEdit{Watched: &yes}); err != nil {
		t.Fatal(err)
	}
	if !p.listedWatched(limited, "Bluey") {
		t.Fatal("a show the limited profile marked watched is not listed as watched")
	}
	p.check("after the limited profile watched the whole show")

	// The provider now says the show is for adults: the limited profile sees none of it.
	p.rate(show, "TV-MA", 17)
	p.check("after the provider raised the show's rating above the profile's limit")

	// A new episode arrives while the profile cannot see the show.
	p.write("Bluey/Season 1/Bluey - S01E03.mkv", 301)
	p.scan()
	p.check("after an episode arrived that the profile cannot see")

	// The provider corrects the rating. The profile sees three episodes and has watched two.
	p.rate(show, "TV-Y7", 7)
	if got := len(p.items(limited, db.ItemQuery{Kinds: []db.ItemKind{db.KindEpisode}})); got != 3 {
		t.Fatalf("the limited profile lists %d episodes after the rating came back down; want 3", got)
	}
	if p.listedWatched(limited, "Bluey") {
		t.Error("the limited profile's Watched list shows a show with an episode it has not watched")
	}
	p.check("after the provider lowered the rating again")

	before, _, _ := db.WatchedMarks(p.d)
	if err := p.d.RebuildWatchedMarks(ctx); err != nil {
		t.Fatal(err)
	}
	after, _, _ := db.WatchedMarks(p.d)
	if len(before) != len(after) {
		t.Errorf("a rebuild changed the marks: %d before, %d after", len(before), len(after))
	}
}

// An unwatched episode's file is renamed into another show: the show it left is now all
// watched, and the show it joined is not.
func TestWatchedMarkFollowsAMove(t *testing.T) {
	ctx := context.Background()
	p := newMarkProbe(t, "Bluey/Season 1/Bluey - S01E01.mkv", "Bluey/Season 1/Bluey - S01E02.mkv", "The Wire/Season 1/The Wire - S01E01.mkv")
	viewers, err := seed.Profiles(ctx, p.d, 2)
	if err != nil {
		t.Fatal(err)
	}
	everyone := viewers[0]
	yes := true
	for _, episode := range p.items(everyone, db.ItemQuery{Kinds: []db.ItemKind{db.KindEpisode}}) {
		if episode.Title != "Bluey - S01E02" && !(episode.IndexNumber != nil && *episode.IndexNumber == 2) {
			if _, _, err := p.d.SetState(ctx, everyone, episode.PublicID, db.StateEdit{Watched: &yes}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if p.listedWatched(everyone, "Bluey") || !p.listedWatched(everyone, "The Wire") {
		t.Fatal("before the move: Bluey must not be watched and The Wire must be")
	}
	p.check("before the move")
	if err := os.Rename(filepath.Join(p.root, "Bluey/Season 1/Bluey - S01E02.mkv"), filepath.Join(p.root, "The Wire/Season 1/The Wire - S01E02.mkv")); err != nil {
		t.Fatal(err)
	}
	p.scan()
	if !p.listedWatched(everyone, "Bluey") || p.listedWatched(everyone, "The Wire") {
		t.Error("after the move: Bluey must be watched and The Wire must not be")
	}
	p.check("after the move")
}
