package localmetadata

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/audiotags"
)

func TestAudioSidecarCoverSuppressesOnlyEmbeddedArtworkIssue(t *testing.T) {
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for _, fileIssue := range []string{"", "invalid_audio_tags"} {
		t.Run("file-issue="+fileIssue, func(t *testing.T) {
			service := fakeBook{"/music/Album/cover.png": raw.String()}.service(t)
			baseline := assets.Facts{LocalMetadataIssue: fileIssue, ArtworkKey: "embedded", AudioFileMetadata: &assets.AudioFileMetadata{ArtworkIssue: "invalid_embedded_cover"}}
			f := service.PrepareAudioSidecars(context.Background(), "library", "music", "/music", "/music/Album/song.flac", baseline)
			if f.ArtworkKey == "" || f.ArtworkKey == "embedded" || f.LocalMetadataIssue != fileIssue {
				t.Fatalf("sidecar result %+v", f)
			}
			service = fakeBook{}.service(t)
			f = service.PrepareAudioSidecars(context.Background(), "library", "music", "/music", "/music/Album/song.flac", baseline)
			if f.ArtworkKey != "embedded" || f.LocalMetadataIssue != "invalid_embedded_cover" {
				t.Fatalf("fallback result %+v", f)
			}
		})
	}
}

func TestAudioEmbeddedCueBaselineKeepsSidecarPrecedence(t *testing.T) {
	path := "/music/Album/song.flac"
	service := fakeBook{"/music/Album/song.cue": "FILE \"song.flac\" WAVE\n TRACK 01 AUDIO\n TITLE \"One\"\n INDEX 01 00:00:00\n TRACK 02 AUDIO\n TITLE \"Two\"\n INDEX 01 00:30:00\n"}.service(t)
	input := assets.Facts{Duration: 60, EmbeddedAudio: &audiotags.Tags{CueTracks: []audiotags.Chapter{{Title: "Bad", Start: 0, End: -1}, {Title: "End", Start: 30, End: 60}}}}
	full := service.PrepareKind(context.Background(), "library", "music", "/music", path, input)
	if full.LocalMetadataIssue != "" || len(full.MusicCueTracks) != 2 {
		t.Fatalf("valid sidecar did not replace invalid embedded cue: %+v", full)
	}
	baseline := full.AudioFileMetadata
	if baseline.Issue != "" || baseline.ArtworkIssue != "" || len(baseline.MusicCueTracks) != 2 || baseline.MusicCueTracks[0].End != -1 {
		t.Fatalf("file baseline changed: %+v", baseline)
	}
	service = fakeBook{}.service(t)
	refreshed := service.PrepareAudioSidecars(context.Background(), "library", "music", "/music", path, assets.Facts{Duration: 60, MusicCueTracks: baseline.MusicCueTracks, AudioFileMetadata: baseline})
	if refreshed.LocalMetadataIssue != "invalid_music_cue" {
		t.Fatalf("removed cue did not restore embedded validation: %+v", refreshed)
	}
}

func TestAudioUnavailableEmbeddedCoverDoesNotPublishReusableBaseline(t *testing.T) {
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	service := fakeBook{"/music/Album/cover.png": raw.String()}.service(t)
	f := service.PrepareKind(context.Background(), "library", "music", "/music", "/music/Album/song.flac", assets.Facts{AttachedPicture: true})
	if f.ArtworkKey == "" || f.LocalMetadataIssue != "" {
		t.Fatalf("valid sidecar lost: %+v", f)
	}
	if f.AudioFileMetadata != nil {
		t.Fatal("uncached embedded fallback published as reusable")
	}
}
