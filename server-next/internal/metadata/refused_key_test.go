package metadata_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// refusesTheKey answers every request as TMDB does for a key it no longer accepts.
type refusesTheKey struct{ metadata.Provider }

func (refusesTheKey) Search(context.Context, metadata.Query) ([]metadata.Candidate, error) {
	return nil, metadata.StatusError{Provider: "tmdb", Status: 401, Path: "/search/movie"}
}

// A key the provider refuses is refused for every title. That is not seven titles each at
// fault: the run stops, and no title is made to wait for it.
func TestAKeyTheProviderRefusesIsNotEveryTitlesOwnFault(t *testing.T) {
	f := newFixture(t, db.LibraryMovies, "a.one.2001.mkv", "b.two.2002.mkv", "c.three.2003.mkv", "d.four.2004.mkv",
		"e.five.2005.mkv", "f.six.2006.mkv", "g.seven.2007.mkv")
	f.s.Providers["tmdb"] = refusesTheKey{f.s.Providers["tmdb"]}
	stats, err := f.s.MatchLibrary(ctx, f.lib.ID)
	if !errors.Is(err, metadata.ErrProviderDown) {
		t.Errorf("a refused key: %+v, %v; want the run stopped as a provider that cannot be asked", stats, err)
	}
	if waiting, err := f.d.ToMatch(ctx, f.lib.ID, []db.ItemKind{db.KindMovie}, 0, 50); err != nil || len(waiting) != 7 {
		t.Errorf("titles still to be looked up at once: %d, %v; want all 7 (none made to wait for the key)", len(waiting), err)
	}
}
