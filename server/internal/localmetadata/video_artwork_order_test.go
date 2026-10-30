package localmetadata

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

func TestVideoArtworkOrderUsesActualAliasAndNumericSuffix(t *testing.T) {
	for _, row := range []struct {
		name string
		rank int64
	}{
		{"Film-poster.jpg", 0},
		{"Film.jpg", 8},
		{"poster.jpg", 16},
		{"Poster.JPEG", 17},
		{"folder.webp", 28},
		{"poster-0.jpg", 1024},
		{"poster-0002.png", 1026},
		{"poster-7.png", 1031},
		{"poster-10.jpg", 1034},
		{"poster-9223372036854774782.png", assets.LocalArtworkUnknownRank - 1},
		{"poster-9223372036854774783.png", assets.LocalArtworkUnknownRank},
		{"poster-9999999999999999999999999999.jpg", assets.LocalArtworkUnknownRank},
	} {
		slot, rank := videoArtworkOrder("movie", "/root", "/root/Film/Film.mkv", "/root/Film/"+row.name, -1)
		if slot != "item/poster" || rank != row.rank {
			t.Errorf("%s: slot=%s rank=%d want %d", row.name, slot, rank, row.rank)
		}
	}
	// Context precedence is the existing season lookup: explicit season names
	// in the season directory, then the show directory, then generic fallback.
	for _, row := range []struct {
		path string
		rank int64
	}{
		{"/root/Show/Season 01/Season01.jpg", 8},
		{"/root/Show/season1.jpg", 256},
		{"/root/Show/Season 01/poster.jpg", 512},
		{"/root/Show/Season 01/Season01-2.png", 1026},
		{"/root/Show/Season 01/Season01a.png", assets.LocalArtworkUnknownRank},
	} {
		slot, rank := videoArtworkOrder("tv", "/root", "/root/Show/Season 01/episode.mkv", row.path, 1)
		if slot != "season/1/poster" || rank != row.rank {
			t.Errorf("%s: slot=%s rank=%d want %d", row.path, slot, rank, row.rank)
		}
	}
}
