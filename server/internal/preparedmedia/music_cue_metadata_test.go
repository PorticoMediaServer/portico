package preparedmedia

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/audiotags"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestMusicCueDownloadMetadataUsesExactCurrentRangeAndTrackCohort(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("music", "Music", "music", "/music")
	album := c.Album(c.Artist(library, "Band"), "Album", 2026)
	first := c.Song(album, 1, "/music/first.flac", "First cue")
	second := c.Song(album, 2, "/music/second.flac", "Second cue")
	_, asset := c.File(first.ID, "/music/image.flac", 20)
	c.File(second.ID, "/music/image.flac", 20)
	c.Exec(`INSERT INTO music_virtual_tracks(asset_id,ordinal,entity_id,start_seconds,end_seconds) VALUES(?,1,?,0,10),(?,2,?,10,20)`, asset, first.ID, asset, second.ID)
	service := &Service{db: c.DB}
	end := 20.0
	w := work{Job: Job{ItemID: second.Public}, Source: selection{Cue: true, Asset: asset, Start: 10, End: &end}}
	picture := musicCover(t)
	original := func() audiotags.Tags {
		return audiotags.Tags{Values: map[string][]string{"title": {"Whole image"}, "track": {"1"}, "total_tracks": {"1"}, "disc": {"2/3"}, "artist": {"Band"}, "cuesheet": {"private whole-image cue sheet"}, "cue_sheet": {"alternate whole-image cue sheet"}}, Pictures: []audiotags.Picture{{Type: 3, MIME: "image/png", Data: picture}}}
	}
	tags, err := service.cueMusicTags(context.Background(), w, original())
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"title": "Second cue", "track": "2", "total_tracks": "2", "disc": "2/3", "artist": "Band"} {
		if len(tags.Values[key]) != 1 || tags.Values[key][0] != want {
			t.Fatal("incorrect extracted cue metadata", key, tags.Values)
		}
	}
	if len(tags.Values["cuesheet"]) != 0 || len(tags.Values["cue_sheet"]) != 0 || len(tags.Pictures) != 1 || !bytes.Equal(tags.Pictures[0].Data, picture) {
		t.Fatal("cue extraction lost artwork or retained the whole-image sheet")
	}
	// Exercise the actual metadata writer and retained-art verifier too.
	encoded := numberMetadataMP4(t, tags)
	if err := verifyMusicMetadata(bytes.NewReader(encoded), int64(len(encoded)), tags); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name   string
		change func(*work)
	}{
		{"wrong item", func(w *work) { w.ItemID = first.Public }},
		{"wrong asset", func(w *work) { w.Source.Asset = second.Token }},
		{"changed start", func(w *work) { w.Source.Start = 9 }},
		{"changed end", func(w *work) { v := 19.0; w.Source.End = &v }},
		{"missing end", func(w *work) { w.Source.End = nil }},
		{"nonfinite range", func(w *work) { w.Source.Start = math.NaN() }},
	} {
		t.Run(row.name, func(t *testing.T) {
			changed := w
			row.change(&changed)
			got, err := service.cueMusicTags(context.Background(), changed, original())
			if !errors.Is(err, ErrSourceChanged) || got.Values["title"][0] != "Whole image" {
				t.Fatal("stale/foreign metadata was accepted or partially applied", err, got.Values)
			}
		})
	}
	c.Exec(`DELETE FROM music_virtual_tracks WHERE asset_id=? AND ordinal=1`, asset)
	if _, err := service.cueMusicTags(context.Background(), w, original()); !errors.Is(err, ErrSourceChanged) {
		t.Fatal("sparse cohort invented a track total", err)
	}
	c.Exec(`INSERT INTO music_virtual_tracks(asset_id,ordinal,entity_id,start_seconds,end_seconds) VALUES(?,1,?,0,10)`, asset, first.ID)
	c.Delete(second.ID)
	if _, err := service.cueMusicTags(context.Background(), w, original()); !errors.Is(err, ErrSourceChanged) {
		t.Fatal("retired cue title was exported", err)
	}
}
