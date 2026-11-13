package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A book or comic library's series names its publication, by the reading items' rule, so every
// client opens its card (Home, Discover, search, a library tab) as a reading series; an audiobook
// series names none and is unchanged on the wire.
func TestAReadingSeriesNamesItsPublication(t *testing.T) {
	b := newBookShelf(t)
	ctx := context.Background()
	audiobooks, err := b.s.db.CreateLibrary(ctx, "Audiobooks", db.LibraryAudiobooks, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	tides := db.ItemKey{Kind: db.KeyName, Key: "series|tides"}
	if _, err := b.s.db.Import(ctx, db.ImportBatch{LibraryID: audiobooks.ID, PathID: audiobooks.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{{FileRef: db.FileRef{Name: "a.m4b"}, Role: db.FileMedia, Size: 1, ModifiedNS: 1, Container: "mp4", DurationMS: 60_000,
			Streams: []db.Stream{{Kind: db.StreamAudio, Codec: "aac", Channels: 2, SampleRate: 44100}}}},
		Items: []db.ImportItem{
			{Kind: db.KindSeries, Title: "Tides", Keys: []db.ItemKey{tides}},
			{Kind: db.KindAudiobook, Title: "Low Water", Keys: []db.ItemKey{{Kind: db.KeyName, Key: "audiobook|low water"}},
				Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "a.m4b"}}}}},
				ExtraOf: []db.ItemKey{tides}, ExtraKind: db.RelationSeriesMember},
		}}); err != nil {
		t.Fatal(err)
	}
	publication := map[string]string{}
	for _, lib := range get[Libraries](t, b.s, b.owner.AccessToken, "/v1/libraries").Items {
		for _, it := range get[ItemPage](t, b.s, b.owner.AccessToken, "/v1/items?library="+lib.ID+"&kind=series").Items {
			publication[lib.Kind+" "+it.Title] = it.PublicationType
			if one := get[Item](t, b.s, b.owner.AccessToken, "/v1/items/"+it.ID); one.PublicationType != it.PublicationType {
				t.Errorf("%s's own page names %q, its card %q", it.Title, one.PublicationType, it.PublicationType)
			}
		}
	}
	if got := publication[db.LibraryComics+" Saga"]; got != "book" {
		t.Errorf("a comic library's series names publication %q, want book", got)
	}
	if got, ok := publication[db.LibraryAudiobooks+" Tides"]; !ok || got != "" {
		t.Errorf("an audiobook series names publication %q (listed %v), want none", got, ok)
	}
}
