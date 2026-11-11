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
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// The viewer's subtitle timing nudge reaches the subtitles the server sends. For a player
// that follows what it is given, the cues themselves move and the stream has a new address.
// An app that draws subtitles itself (it asks for "sidecar") is told the nudge and keeps
// its stream.
func TestTheTimingNudgeReachesServedSubtitles(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	h, d := newHouse(t, ffmpeg, ffprobe)
	ctx := context.Background()
	library := filepath.Join(t.TempDir(), "Films")
	film := filepath.Join(library, "Nudged (2020)", "Nudged (2020).mp4")
	if err := os.MkdirAll(filepath.Dir(film), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strings.TrimSuffix(film, ".mp4")+".en.srt", []byte("1\n00:00:01,000 --> 00:00:03,000\nA line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "5", "-pix_fmt", "yuv420p", "-ac", "2", "-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac", "-movflags", "+faststart", film).CombinedOutput(); err != nil {
		t.Fatalf("making the film: %v\n%s", err, out)
	}
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)

	var session api.PlaybackSession
	h.call("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID}, &session, 201)
	if len(session.Presentation.Subtitles) != 1 {
		t.Fatalf("the film's subtitles: %+v", session.Presentation)
	}
	track := session.Presentation.Subtitles[0].TrackID
	change := func(delivery string, nudge int64) api.Presentation {
		t.Helper()
		h.call("PATCH", "/v1/playback/sessions/"+session.ID, api.PlaybackChange{Subtitles: &api.SubtitlePick{TrackID: &track, Delivery: delivery},
			SubtitleOffsetMs: &nudge}, &session, 200)
		return session.Presentation
	}
	cues := func(p api.Presentation) string {
		t.Helper()
		code, _, body := h.media(p.Subtitles[0].URL)
		if code != 200 {
			t.Fatalf("%s answered %d", p.Subtitles[0].URL, code)
		}
		return string(body)
	}

	// Half a second later, for a player that follows what it is sent.
	first := change("", 0)
	later := change("", 500)
	if later.URL == first.URL || later.Subtitles[0].OffsetMs != 0 || !strings.Contains(cues(later), "00:00:01.500 --> 00:00:03.500") {
		t.Fatalf("a nudge of half a second: address %s then %s, subtitles %+v:\n%s", first.URL, later.URL, later.Subtitles, cues(later))
	}
	// An app that draws them itself is told the nudge when its stream starts; the cues are
	// not moved, and when it changes the nudge nothing is restarted.
	own := change("sidecar", 500)
	again := change("sidecar", 750)
	if own.Subtitles[0].OffsetMs != 500 || again.URL != own.URL || !strings.Contains(cues(again), "00:00:01.000 --> 00:00:03.000") {
		t.Fatalf("an app that draws subtitles itself: address %s then %s, subtitles %+v:\n%s", own.URL, again.URL, own.Subtitles, cues(again))
	}
}
