package live

import (
	"strings"
	"testing"
)

// A channel sent without a guide id keeps its key when only its address's query changes, as
// a provider's per-download token does, so the owner's number, favourite and choice survive.
func TestAChannelWithoutAGuideIDKeepsItsKeyAcrossATokenChange(t *testing.T) {
	keyOf := func(token string) string {
		var got LiveSentChannel
		playlist := "#EXTM3U\n#EXTINF:-1 group-title=\"News\",Local News\nhttp://streams.invalid/live/news.m3u8?token=" + token + "\n"
		if err := ParsePlaylist(strings.NewReader(playlist), &LiveSourceReport{}, func(c LiveSentChannel) error { got = c; return nil }); err != nil {
			t.Fatal(err)
		}
		return got.Key
	}
	if a, b := keyOf("first"), keyOf("second"); a != b {
		t.Fatalf("the same channel got key %q, then %q after its token changed", a, b)
	}
}
