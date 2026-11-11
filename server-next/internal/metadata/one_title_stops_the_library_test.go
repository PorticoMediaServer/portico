package metadata_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// refusesOne is a provider that keeps failing the search for the first title it is asked
// about, as TMDB does for a query it will not take (422) or while one request keeps timing
// out, and answers every other title.
type refusesOne struct {
	metadata.Provider
	title string
}

func (p *refusesOne) Search(ctx context.Context, q metadata.Query) ([]metadata.Candidate, error) {
	if p.title == "" {
		p.title = q.Title
	}
	if q.Title == p.title {
		return nil, errors.New("tmdb answered 422 for /search/movie")
	}
	return p.Provider.Search(ctx, q)
}

// Since a scan now starts the lookup by itself, one title the provider keeps failing on must
// not keep every title after it from ever being looked up. Three films, the first one refused
// every time: after the lookup has run three times (three scans), the other two have been
// asked about.
func TestOneTitleTheProviderFailsOnDoesNotStopTheRestOfTheLibrary(t *testing.T) {
	f := newFixture(t, db.LibraryMovies, "alpha.one.2001.mkv", "the.matrix.1999.1080p.mkv", "zeta.two.2003.mkv")
	f.s.Providers["tmdb"] = &refusesOne{Provider: f.s.Providers["tmdb"]}
	for range 3 {
		f.s.MatchLibrary(ctx, f.lib.ID) // the error is the job's to report
	}
	waiting, err := f.d.ToMatch(ctx, f.lib.ID, []db.ItemKind{db.KindMovie}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) > 1 {
		var titles []string
		for _, item := range waiting {
			titles = append(titles, item.Title)
		}
		t.Fatalf("after three lookups %d of 3 films have still never been asked about (%q): the one the provider fails on stops the rest", len(waiting), titles)
	}
}

// down is a provider that answers nothing at all.
type down struct{ metadata.Provider }

func (down) Search(context.Context, metadata.Query) ([]metadata.Candidate, error) {
	return nil, fmt.Errorf("tmdb did not answer within 20s: %w", context.DeadlineExceeded)
}

// A provider that fails on every title is down: the run stops after a few in a row instead of
// asking about every title, and every title is still to be looked up, with no wait.
func TestAProviderThatIsDownStopsTheRun(t *testing.T) {
	f := newFixture(t, db.LibraryMovies, "a.one.2001.mkv", "b.two.2002.mkv", "c.three.2003.mkv", "d.four.2004.mkv",
		"e.five.2005.mkv", "f.six.2006.mkv", "g.seven.2007.mkv")
	f.s.Providers["tmdb"] = down{f.s.Providers["tmdb"]}
	stats, err := f.s.MatchLibrary(ctx, f.lib.ID)
	if !errors.Is(err, metadata.ErrProviderDown) || stats.Failed != 5 {
		t.Fatalf("a provider that is down: %+v, %v; want 5 failed and ErrProviderDown", stats, err)
	}
	// None of them is held back for the provider's fault: the next run asks about all seven.
	if waiting, err := f.d.ToMatch(ctx, f.lib.ID, []db.ItemKind{db.KindMovie}, 0, 50); err != nil || len(waiting) != 7 {
		t.Fatalf("titles still to be looked up: %d, %v; want all 7", len(waiting), err)
	}
}
