package localmetadata

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

func TestMusicPackedTrackEvidenceUsesOnlyClearCurrentListingAndKeepsTags(t *testing.T) {
	names := []string{"101 - One.flac", "102 - Two.flac", "201 - Three.flac", "301 - Four.flac", "302 - Five.flac"}
	listed := func(_ string) ([]string, bool) { return names, true }
	facts := func() assets.Facts { return assets.Facts{Tags: map[string]string{}, TagSources: map[string]string{}} }
	ctx := WithMusicTrackListing(context.Background(), listed)
	got := musicPackedTrackEvidence(ctx, "/music/Artist/Album/302 - Five.flac", facts())
	if got.Tags["disc"] != "3" || got.Tags["track"] != "2" || got.TagSources["disc"] != "filename" || got.TagSources["track"] != "filename" || len(got.AudioEvidence) != 2 {
		t.Fatal(got)
	}
	for _, row := range []struct {
		name, path string
		tags       map[string]string
		names      []string
		known      bool
		cue        bool
	}{
		{name: "embedded disc", path: "/music/Album/302 - Five.flac", tags: map[string]string{"disc": "9"}, names: names, known: true},
		{name: "explicit track", path: "/music/Album/302 - Five.flac", tags: map[string]string{"track": "302"}, names: names, known: true},
		{name: "disc folder", path: "/music/Album/Disc 4/302 - Five.flac", names: names, known: true},
		{name: "not observed", path: "/music/Album/302 - Five.flac", names: names},
		{name: "current absent", path: "/music/Album/303 - Six.flac", names: names, known: true},
		{name: "ambiguous", path: "/music/Album/302 - Five.flac", names: []string{"101 - One.flac", "302 - Five.flac"}, known: true},
		{name: "mixed names", path: "/music/Album/302 - Five.flac", names: append(append([]string{}, names...), "Other.flac"), known: true},
		{name: "unsafe name", path: "/music/Album/302 - Five.flac", names: []string{"../101 - One.flac", "302 - Five.flac"}, known: true},
		{name: "oversized", path: "/music/Album/302 - Five.flac", names: stringsToNames(naming.MaxPackedTrackCohort + 1), known: true},
		{name: "cue", path: "/music/Album/302 - Five.flac", names: names, known: true, cue: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			before := facts()
			for key, value := range row.tags {
				before.Tags[key] = value
				before.TagSources[key] = "embedded"
			}
			if row.cue {
				before.MusicCueTracks = []assets.Chapter{{Title: "Cue song", End: 1}}
			}
			want := map[string]string{}
			for key, value := range before.Tags {
				want[key] = value
			}
			ctx := WithMusicTrackListing(context.Background(), func(string) ([]string, bool) { return row.names, row.known })
			got := musicPackedTrackEvidence(ctx, row.path, before)
			if !reflect.DeepEqual(got.Tags, want) || len(got.AudioEvidence) != 0 {
				t.Fatal("refused cohort changed explicit facts", got)
			}
		})
	}
}

func stringsToNames(count int) []string {
	return strings.Split(strings.Repeat("101 - One.flac,", count-1)+"302 - Five.flac", ",")
}
