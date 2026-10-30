package metadata

import (
	"context"
	"testing"
)

func TestEpisodeThumbnailProviderPolicyBypassesUnrelatedWorkWithoutReading(t *testing.T) {
	for _, f := range []struct{ kind, role, actor, provider string }{
		{"movie", "still", "", "tmdb"}, {"episode", "poster", "", "tvdb"},
		{"episode", "still", "owner", "tmdb"}, {"episode", "thumbnail", "", "local"},
		{"episode", "still", "", "upload"}, {"episode", "still", "", "generated"},
	} {
		// nil deliberately proves these cases make no policy/extra SQL read.
		ok, err := EpisodeProviderStillAllowedTx(context.Background(), nil, "library", f.kind, f.role, f.actor, f.provider)
		if err != nil || !ok {
			t.Fatalf("unrelated artwork refused: %+v: %v", f, err)
		}
	}
}
