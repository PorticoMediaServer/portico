package musicbrainz_test

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/musicbrainz"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/musicbrainz/musicbrainztest"
)

// MusicBrainz lists all of Abbey Road's editions at score 100, a 2019 three-disc deluxe edition
// first. The edition taken is the one most like the files.
func TestTheEditionLikeTheFilesIsTaken(t *testing.T) {
	c := musicbrainz.New(musicbrainztest.New(t).URL, 0)
	for _, tc := range []struct {
		name   string
		year   int
		layout []int
		want   string
	}{
		{"no year, no tracks: the earliest official edition", 0, nil, "d6010be3-98f8-422c-a6c9-787e2e491e58"},
		{"seventeen tracks on one disc", 0, []int{17}, "d6010be3-98f8-422c-a6c9-787e2e491e58"},
		{"the deluxe edition's three discs", 0, []int{17, 12, 11}, "d23f0ad2-f0d5-4714-9fb0-ed80189799d3"},
		{"filed under 2019, one disc", 2019, []int{17}, "c4783e19-c30e-400f-bd5a-4d7209c22ba0"},
	} {
		got, err := c.Search(context.Background(), metadata.Query{Kind: "album", Title: "Abbey Road", Creator: "The Beatles",
			Year: tc.year, Layout: tc.layout})
		if err != nil || len(got) != 1 {
			t.Fatalf("%s: %d answers, %v", tc.name, len(got), err)
		}
		if got[0].Ref.ID != tc.want {
			t.Errorf("%s: took %s (%d)", tc.name, got[0].Ref.ID, got[0].Year)
		}
		if want := "https://coverartarchive.org/release-group/9162580e-5df4-32de-80cc-f45a8d8a9b1d/front"; got[0].PosterURL != want {
			t.Errorf("%s: cover %q", tc.name, got[0].PosterURL)
		}
	}
}
