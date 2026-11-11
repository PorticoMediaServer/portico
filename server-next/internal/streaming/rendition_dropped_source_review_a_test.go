package streaming

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Review desk A, for #2780. A converter whose source drops half way (a remote source's
// connection, a cut-short file) exits 0, as FFmpeg 8.0.1 and 8.1.3 both do, with an error
// on standard error and only the first slots written. That is a failure, not the end of the
// track: asking for a later slot must start the converter again and play, never answer
// "ended" for the rest of the film.
//
// The stand-in is the real FFmpeg, except that the first audio window of each track loses
// its slots from 2 on and says what FFmpeg says when the source drops.
func TestADroppedSourceIsNotTheEndOfTheTrack(t *testing.T) {
	rig := newMediaRig(t)
	film := rig.generate("remux.mkv", 24, "-map", "0:v", "-map", "1:a", "-map", "1:a", "-c:v", "libx264", "-c:a", "aac")
	standIn := filepath.Join(t.TempDir(), "ffmpeg")
	script := `#!/bin/sh
audio=0; pattern=; previous=
for a in "$@"; do
  [ "$a" = -vn ] && audio=1
  [ "$previous" = -hls_segment_filename ] && pattern=$a
  previous=$a
done
if [ $audio = 1 ] && [ -n "$pattern" ] && [ ! -e "$pattern.dropped" ]; then
  : > "$pattern.dropped"
  ` + rig.ffmpeg + ` "$@" || exit $?
  for n in 2 3 4; do f=$(printf "$pattern" $n); [ -e "$f" ] && : > "$f"; done
  echo "[http @ 0x1] Stream ends prematurely at 100, should be 200" >&2
  exit 0
fi
exec ` + rig.ffmpeg + ` "$@"
`
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
	stream, plan := rig.start("dropped", film, 0, nil)
	if stream.Mode != "hls" || len(plan.Renditions) < 2 {
		t.Fatalf("mode %s with %d audio renditions", stream.Mode, len(plan.Renditions))
	}
	slots := hlsSegmentCount(plan.Duration)
	if slots < 4 {
		t.Fatalf("a film of %.3f s has %d slots", plan.Duration, slots)
	}
	rendition := plan.Renditions[1]
	// The start of the track plays; the converter that wrote it lost its source after slot 1.
	if len(rig.fetch(stream.Grant, renditionSegmentFile(rendition, 0))) == 0 {
		t.Fatal("the track's first slot did not play")
	}
	later := renditionSegmentFile(rendition, 3)
	began := time.Now()
	for {
		response := rig.get(context.Background(), stream.Grant, later, "")
		if response.Code == http.StatusOK {
			return
		}
		if response.Code != http.StatusServiceUnavailable || time.Since(began) > 60*time.Second {
			t.Fatalf("slot 3 of %d, 18 s into a 24 s film, answered %d after %s: %s", slots, response.Code, time.Since(began).Round(time.Millisecond), response.Body.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}
