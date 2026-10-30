package metadata

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// Protocol-only probe: selecting a returned edition here does not select a
// catalog match, open a database, or change any user's library.
func TestMusicBrainzLiveBoundedPublicLookup(t *testing.T) {
	if os.Getenv("PORTICO_TEST_MUSICBRAINZ_LIVE") != "1" {
		t.Skip("explicit live provider probe disabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	p := metadataprovider.NewMusicBrainz()
	candidates, e := p.SearchReleases(ctx, "Kind of Blue", "Miles Davis")
	if e != nil {
		t.Fatal(e)
	}
	if len(candidates) == 0 {
		t.Fatal("probe returned no reviewable release editions")
	}
	t.Logf("bounded public search returned %d edition candidates; none selected as a catalog match", len(candidates))
	result, e := p.Release(ctx, candidates[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	tracks := 0
	for _, medium := range result.Release.Media {
		tracks += len(medium.Tracks)
	}
	t.Logf("validated one release lookup: %d media, %d tracks, release-group identity present: %t", len(result.Release.Media), tracks, result.Release.ReleaseGroup.ID != "")
}
