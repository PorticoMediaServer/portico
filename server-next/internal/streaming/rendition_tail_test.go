package streaming

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The grid never lists a last slot shorter than a second: such a tail belongs to the slot
// before it, whose length says so.
func TestAShortTailBelongsToTheSlotBeforeIt(t *testing.T) {
	for _, tc := range []struct {
		duration float64
		slots    int
		last     string
	}{{12.021, 2, "#EXTINF:6.021000,"}, {12.9, 2, "#EXTINF:6.900000,"}, {13, 3, "#EXTINF:1.000000,"}, {6.5, 1, "#EXTINF:6.500000,"}, {0.4, 1, "#EXTINF:0.400000,"}} {
		manifest := string(hlsTimelineManifest(tc.duration))
		lines := strings.Split(strings.TrimSpace(manifest), "\n")
		if got := hlsSegmentCount(tc.duration); got != tc.slots || strings.Count(manifest, "#EXTINF:") != tc.slots || lines[len(lines)-3] != tc.last {
			t.Fatalf("%.3f s: %d slots, manifest\n%s", tc.duration, got, manifest)
		}
	}
}

// An audio track asked for at its last slot, by a converter that exits cleanly and writes
// nothing there (Portico's own FFmpeg does that for a short tail): that is the end of the
// track, answered at once, not "still being prepared" while the converter starts again
// and again. The converter here is the real FFmpeg except for that one window, so the test
// does not depend on which FFmpeg is installed.
func TestATrackWithNothingLeftAtItsEndIsAnsweredAtOnce(t *testing.T) {
	rig := newMediaRig(t)
	film := rig.generate("remux.mkv", 12, "-map", "0:v", "-map", "1:a", "-map", "1:a", "-c:v", "libx264", "-c:a", "aac")
	// Like FFmpeg, except that an audio window starting at slot 1 exits cleanly and empty.
	standIn := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\naudio=0; tail=0; previous=\n" +
		"for a in \"$@\"; do [ \"$a\" = -vn ] && audio=1; [ \"$previous\" = -start_number ] && [ \"$a\" = 1 ] && tail=1; previous=$a; done\n" +
		"[ $audio = 1 ] && [ $tail = 1 ] && exit 0\nexec " + rig.ffmpeg + " \"$@\"\n"
	if err := os.WriteFile(standIn, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	streams, err := New(ctx, Options{Folder: t.TempDir(), FFmpeg: standIn, Libraries: func() []string { return []string{rig.library} }})
	if err != nil {
		t.Fatal(err)
	}
	go streams.Run(ctx)
	t.Cleanup(func() {
		wait, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		_ = streams.Shutdown(wait)
		cancel()
	})
	rig.streams = streams
	stream, plan := rig.start("tail", film, 0, nil)
	if stream.Mode != "hls" || len(plan.Renditions) < 2 {
		t.Fatalf("mode %s with %d audio renditions", stream.Mode, len(plan.Renditions))
	}
	if slots := hlsSegmentCount(plan.Duration); slots != 2 {
		t.Fatalf("a film of %.3f s has %d slots", plan.Duration, slots)
	}
	rendition := plan.Renditions[1]
	last := renditionSegmentFile(rendition, 1)
	began := time.Now()
	for {
		response := rig.get(context.Background(), stream.Grant, last, "")
		if response.Code == http.StatusNotFound {
			break
		}
		if response.Code != http.StatusServiceUnavailable || time.Since(began) > 30*time.Second {
			t.Fatalf("the track's last slot answered %d after %s: %s", response.Code, time.Since(began), response.Body.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
	// The rest of the track still plays, its last slot included once a window from the
	// start has written it.
	if len(rig.fetch(stream.Grant, renditionSegmentFile(rendition, 0))) == 0 || len(rig.fetch(stream.Grant, last)) == 0 {
		t.Fatal("the track's first slots did not play")
	}
	streams.Stop("tail")
}
