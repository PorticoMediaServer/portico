package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

func TestTopShelfSectionPreservesHomeServerText(t *testing.T) {
	row := catalog.HomeRow{
		ID: "continue", Title: "Owner-supplied fallback", ArtworkShape: "poster",
		TitleText: catalog.ServerText{Code: "home.row.continueWatching", Params: map[string]string{"name": "Actual name"}, Fallback: "Actual fallback"},
	}
	got := topShelfSection(row)
	if got.ID != row.ID || got.Title != row.Title || got.Shape != row.ArtworkShape || !reflect.DeepEqual(got.TitleText, row.TitleText) || got.Entries == nil {
		t.Fatalf("Home section facts changed: %#v", got)
	}
}

func TestTopShelfEntryPreservesSuppliedNumbering(t *testing.T) {
	zero, season, episode, absolute := 0, 2, 3, 37
	for _, tc := range []struct {
		name   string
		in     catalog.ContentEntry
		want   map[string]any
		absent []string
	}{
		{"seasonal", catalog.ContentEntry{Kind: "episode", Show: &catalog.ContentEntityRef{ID: "show", Name: "Actual show"}, SeasonNumber: &season, EpisodeNumber: &episode}, map[string]any{"showTitle": "Actual show", "seasonNumber": float64(2), "episodeNumber": float64(3)}, []string{"absoluteNumber", "dateBased", "special", "airDate"}},
		{"absolute", catalog.ContentEntry{Kind: "episode", EpisodeNumber: &absolute, AbsoluteNumber: &absolute}, map[string]any{"absoluteNumber": float64(37), "episodeNumber": float64(37)}, []string{"seasonNumber", "showTitle", "dateBased", "special", "airDate"}},
		{"date", catalog.ContentEntry{Kind: "episode", DateBased: true, AirDate: "2026-10-21"}, map[string]any{"dateBased": true, "airDate": "2026-10-21"}, []string{"seasonNumber", "episodeNumber", "absoluteNumber", "showTitle", "special"}},
		{"special", catalog.ContentEntry{Kind: "episode", Special: true, SeasonNumber: &zero, EpisodeNumber: &episode}, map[string]any{"special": true, "seasonNumber": float64(0), "episodeNumber": float64(3)}, []string{"absoluteNumber", "dateBased", "airDate", "showTitle"}},
		{"missing-parent-and-facts", catalog.ContentEntry{Kind: "episode", Subtitle: "Invented show · S9 E99 · 2026-10-21"}, nil, []string{"showTitle", "seasonNumber", "episodeNumber", "absoluteNumber", "dateBased", "special", "airDate"}},
		{"movie", catalog.ContentEntry{Kind: "movie", Subtitle: "2026"}, nil, []string{"showTitle", "seasonNumber", "episodeNumber", "absoluteNumber", "dateBased", "special", "airDate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.ID, tc.in.Title = "item", "Actual title"
			out := topShelfEntry(tc.in, "artwork-capability")
			if out.Kind != tc.in.Kind || out.Title != tc.in.Title || out.Subtitle != tc.in.Subtitle || out.DisplayURL != "portico://item/item" {
				t.Fatalf("entry identity changed: %#v", out)
			}
			raw, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err = json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			for key, want := range tc.want {
				if !reflect.DeepEqual(wire[key], want) {
					t.Fatalf("%s = %#v, want %#v", key, wire[key], want)
				}
			}
			for _, key := range tc.absent {
				if _, ok := wire[key]; ok {
					t.Fatalf("missing fact %s was invented: %s", key, raw)
				}
			}
		})
	}
}
