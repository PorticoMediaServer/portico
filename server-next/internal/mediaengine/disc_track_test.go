package mediaengine

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// A track is read out of a disc's main feature the way one is read out of a file: that is how
// a disc's own subtitle is burned in, or handed to a player that draws it. No fixture disc has
// a subtitle (making one needs disc-authoring tools), so the sound track is read instead,
// through the same reading.
func TestATrackIsReadOutOfADiscsMainFeature(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	reader, err := exec.LookPath(filepath.Join(filepath.Dir(ffmpeg), mediatools.Name("portico-disc")))
	if err != nil {
		decodertest.Unavailable(t, "these media tools have no disc reader (portico-disc)")
	}
	t.Setenv("PORTICO_PORTICO-DISC", reader)
	streams, err := streaming.New(context.Background(), streaming.Options{Folder: t.TempDir(), FFmpeg: ffmpeg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := streams.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for _, index := range []string{"bluray-two-clip-bdmv/BDMV/index.bdmv", "dvd-two-title-video-ts/VIDEO_TS/VIDEO_TS.IFO", "bluray-udf250.iso"} {
		path, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "discs", index))
		if err != nil {
			t.Fatal(err)
		}
		d, err := (&Engine{probe: mediafacts.Probe{Binary: ffprobe}}).readDisc(context.Background(), path)
		if err != nil {
			t.Fatal(index, err)
		}
		track := -1
		for _, s := range d.facts.Streams {
			if s.Type == "audio" {
				track = s.Index
				break
			}
		}
		if data, err := streams.SubtitleTrack(context.Background(), streaming.DiscSource(d.open), track, false); err != nil || len(data) < 1000 {
			t.Fatalf("%s: read %d bytes: %v", index, len(data), err)
		}
	}
}
