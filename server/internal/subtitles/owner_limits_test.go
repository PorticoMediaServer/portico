package subtitles

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/medialimits"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
)

func TestOwnerSubtitleTrackLimitKeepsUsefulTracks(t *testing.T) {
	defer medialimits.ConfigureSubtitles(medialimits.DefaultSubtitleTracks, medialimits.DefaultSubtitleFileMiB)
	embedded := make([]Track, 180)
	for i := range embedded {
		embedded[i].StreamIndex = i
	}
	enumerate := func(func(storage.Snapshot) error) error { return nil }
	medialimits.ConfigureSubtitles(128, 128)
	got, err := discover(context.Background(), nil, "", "movie.mkv", embedded, 0, true, enumerate)
	if err != nil || len(got.Tracks) != 128 || got.Status != "capacity" {
		t.Fatal(got, err)
	}
	medialimits.ConfigureSubtitles(256, 256)
	got, err = discover(context.Background(), nil, "", "movie.mkv", embedded, 0, true, enumerate)
	if err != nil || len(got.Tracks) != 180 || got.Status != "known" {
		t.Fatal(got, err)
	}
	if BinaryLimitBytes() != 256<<20 {
		t.Fatal("file limit not applied")
	}
}
