package metadata_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
)

// pacedFailure answers every search with what the pacing client gives while a provider is
// unwell: err.
type pacedFailure struct {
	metadata.Provider
	err error
}

func (p pacedFailure) Search(context.Context, metadata.Query) ([]metadata.Candidate, error) {
	return nil, p.err
}

// What the pacing client returns while the provider is unwell, a pause it asked for or its own
// 5xx, is the provider's trouble: the lookup stops as provider down and no title is made to wait.
// Taken for each title's refusal, a long pause would put a wait on every title in seconds.
func TestAPausedOrUnwellProviderBehindThePacingClientStopsTheRun(t *testing.T) {
	for name, err := range map[string]error{
		"a pause": &fetch.SlowDown{Provider: "tmdb", Until: time.Now().Add(time.Hour)},
		"a 503":   &fetch.Status{Provider: "tmdb", Code: 503},
	} {
		f := newFixture(t, db.LibraryMovies, "a.one.2001.mkv", "b.two.2002.mkv", "c.three.2003.mkv", "d.four.2004.mkv",
			"e.five.2005.mkv", "f.six.2006.mkv", "g.seven.2007.mkv")
		f.s.Providers["tmdb"] = pacedFailure{f.s.Providers["tmdb"], err}
		if _, got := f.s.MatchLibrary(ctx, f.lib.ID); !errors.Is(got, metadata.ErrProviderDown) {
			t.Errorf("%s: the run ended %v; want it stopped as a provider that cannot be asked", name, got)
		}
		if waiting, err := f.d.ToMatch(ctx, f.lib.ID, []db.ItemKind{db.KindMovie}, 0, 50); err != nil || len(waiting) != 7 {
			t.Errorf("%s: titles still to be looked up at once: %d, %v; want all 7", name, len(waiting), err)
		}
	}
}
