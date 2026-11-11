package metadata_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// refusesZeta is a provider that keeps failing the search for one film and answers every other.
type refusesZeta struct{ metadata.Provider }

func (p refusesZeta) Search(ctx context.Context, q metadata.Query) ([]metadata.Candidate, error) {
	if strings.Contains(strings.ToLower(q.Title), "zeta") {
		return nil, errors.New("tmdb answered 422 for /search/movie")
	}
	return p.Provider.Search(ctx, q)
}

// A library is matched; later one more film arrives that the provider keeps failing on. It is
// then the only title left to ask about, so every run asks about it alone. That must not read
// as "the provider is down" for ever: the title has to be recorded as tried and wait, or the
// lookup fails on every run, with no wait, and the job that refreshes the library after the
// lookup never reaches the refresh.
func TestTheOnlyTitleLeftThatTheProviderFailsOnIsNotAProviderThatIsDown(t *testing.T) {
	f := newFixture(t, db.LibraryMovies, "the.matrix.1999.1080p.mkv")
	if stats := f.match(); stats.Matched != 1 {
		t.Fatalf("matched %d of 1 film", stats.Matched)
	}
	if err := os.WriteFile(filepath.Join(f.lib.Paths[0].Path, "zeta.two.2003.mkv"), make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}
	if made := f.rescan(); made != 1 {
		t.Fatalf("the rescan made %d titles", made)
	}
	f.s.Providers["tmdb"] = refusesZeta{f.s.Providers["tmdb"]}
	var last error
	for range 3 { // three runs: three scans, or three hours
		_, last = f.s.MatchLibrary(ctx, f.lib.ID)
	}
	if errors.Is(last, metadata.ErrProviderDown) {
		t.Errorf("the third lookup still ends as %q: the one film is asked about on every run and the run fails every time", last)
	}
	waiting, err := f.d.ToMatch(ctx, f.lib.ID, []db.ItemKind{db.KindMovie}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 0 {
		t.Errorf("after three failed lookups the film is still asked about at once, with no wait recorded")
	}
}
