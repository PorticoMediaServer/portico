package mediaengine_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/opensubtitles/opensubtitlestest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

func TestDownloadedSubtitleCanBeSelectedDuringPlayback(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()
	library := t.TempDir()
	film := filepath.Join(library, "The Matrix (1999).mp4")
	if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i",
		"testsrc2=size=320x240:rate=24", "-t", "5", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast", film).CombinedOutput(); err != nil {
		t.Fatalf("making film: %v\n%s", err, out)
	}
	if err := os.WriteFile(strings.TrimSuffix(film, ".mp4")+".fr.srt", []byte("1\n00:00:01,000 --> 00:00:03,000\nSidecar control\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID, nil, &page, 200)
	item := page.Items[0]
	facts, err := d.Item(ctx, db.Viewer{UserID: 1, ProfileID: 1}, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.ApplyDetails(ctx, db.Background, facts.ID, db.ItemDetails{ExternalIDs: []db.ExternalID{{Provider: "imdb", ID: "tt0133093"}}}); err != nil {
		t.Fatal(err)
	}
	provider := opensubtitlestest.New(t)
	h.server.UseSubtitleHost(provider.URL)
	key, user, password := "key", "owner", "right"
	h.call("PUT", "/v1/admin/metadata/settings", api.MetadataSettingsChange{OpenSubtitlesAPIKey: &key,
		OpenSubtitlesUsername: &user, OpenSubtitlesPassword: &password}, nil, 200)
	playing := h.startPlayback(api.PlaybackStart{ItemID: item.ID, Subtitles: &api.SubtitlePick{}})
	// An ordinary sidecar already works and does not interrupt the picture.
	sidecar := playing.Presentation.Subtitles[0].TrackID
	var selected api.PlaybackSession
	path := "/v1/playback/sessions/" + playing.ID
	h.call("PATCH", path, api.PlaybackChange{Subtitles: &api.SubtitlePick{TrackID: &sidecar}}, &selected, 200)
	if selected.Presentation.Subtitle == nil || selected.Presentation.Subtitle.TrackID != sidecar || selected.Presentation.URL != playing.Presentation.URL {
		t.Fatal("ordinary sidecar selection failed or interrupted video")
	}
	if code, _, body := h.media(selected.Presentation.Subtitles[0].URL); code != 200 || !strings.Contains(string(body), "Sidecar control") {
		t.Fatalf("sidecar captions: %d %s", code, body)
	}
	h.call("PATCH", path, api.PlaybackChange{Subtitles: &api.SubtitlePick{}}, &selected, 200)
	var found api.SubtitleResults
	h.call("GET", "/v1/items/"+item.ID+"/subtitles/search?languages=en", nil, &found, 200)
	if len(found.Items) == 0 {
		t.Fatal("fake provider returned no results")
	}
	var downloaded api.ItemSubtitle
	h.call("POST", "/v1/items/"+item.ID+"/subtitles", api.SubtitleDownload{Ref: found.Items[0].Ref}, &downloaded, 200)
	h.call("PATCH", path, api.PlaybackChange{Subtitles: &api.SubtitlePick{TrackID: &downloaded.ID, Delivery: "sidecar"}}, &selected, 200)
	check := func(session api.PlaybackSession) {
		t.Helper()
		if session.Presentation.Subtitle == nil || session.Presentation.Subtitle.TrackID != downloaded.ID || session.Presentation.Subtitle.Delivery != "sidecar" {
			t.Fatalf("accepted selection did not name the downloaded track: %+v", session.Presentation)
		}
		for _, sub := range session.Presentation.Subtitles {
			if sub.TrackID == downloaded.ID {
				code, _, body := h.media(sub.URL)
				if code != 200 || !strings.Contains(string(body), "First line") {
					t.Fatalf("downloaded captions: %d %s", code, body)
				}
				return
			}
		}
		t.Fatal("accepted track has no served subtitle document")
	}
	check(selected)
	h.call("GET", path, nil, &selected, 200)
	check(selected)
	// A later playback can use the kept file without another download, too.
	check(h.startPlayback(api.PlaybackStart{ItemID: item.ID, Subtitles: &api.SubtitlePick{TrackID: &downloaded.ID}}))
	if provider.Downloads.Load() != 1 {
		t.Fatal("playback downloaded from the provider again")
	}
}
