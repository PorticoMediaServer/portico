package metadata_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// refusesOneRefresh is a provider that keeps failing the details of the first matched title it
// is asked to refresh, as TMDB does for an id it answers 5xx or 422 for, and answers every other.
type refusesOneRefresh struct {
	metadata.Provider
	on      bool
	refused string
	others  int
}

func (p *refusesOneRefresh) Details(ctx context.Context, ref metadata.Ref, locale metadata.Locale) (metadata.Record, error) {
	if !p.on {
		return p.Provider.Details(ctx, ref, locale)
	}
	if p.refused == "" {
		p.refused = ref.ID
	}
	if ref.ID == p.refused {
		return metadata.Record{}, errors.New("tmdb answered 500 for /movie/" + ref.ID)
	}
	p.others++
	return p.Provider.Details(ctx, ref, locale)
}

// The refresh that now runs by itself, after a scan and every hour, must not be stopped for
// good by one title either. Two matched films are due; the provider keeps failing on the first
// one it is asked about. After three runs the other film has been refreshed.
func TestOneTitleTheProviderFailsOnDoesNotStopTheRestOfTheRefresh(t *testing.T) {
	f := newFixture(t, db.LibraryMovies, "the.matrix.1999.1080p.mkv", "heat.1995.1080p.mkv")
	p := &refusesOneRefresh{Provider: f.s.Providers["tmdb"]}
	f.s.Providers["tmdb"] = p
	if stats := f.match(); stats.Matched != 2 {
		t.Fatalf("matched %d of 2 films", stats.Matched)
	}
	p.on = true
	later := time.Now().Add(90 * 24 * time.Hour) // both are due
	for run := range 3 {
		f.s.RefreshLibrary(ctx, f.lib.ID, later.Add(time.Duration(run)*2*time.Hour), 100) // the error is the job's to report
	}
	if p.others == 0 {
		t.Fatalf("after three refresh runs the film the provider answers for was never asked about: the one it fails on (%s) stops the rest", p.refused)
	}
}
