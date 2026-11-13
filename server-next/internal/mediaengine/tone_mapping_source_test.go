package mediaengine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

func TestToneMappingObservesFrameMasteringOnTheVideoStream(t *testing.T) {
	ctx := context.Background()
	ffmpeg, probe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	root := t.TempDir()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.CreateLibrary(ctx, "HDR", "movies", "en", []string{root}); err != nil {
		t.Fatal(err)
	}
	supervisor := &processpool.Supervisor{Limit: 1}
	e := &Engine{db: d, processes: supervisor, probe: mediafacts.Probe{Binary: probe, Supervisor: supervisor}, facts: map[string]mediafacts.Facts{}}
	for _, mastered := range []bool{true, false} {
		file := filepath.Join(root, fmt.Sprint(mastered)+".mkv")
		params := "pools=1:frame-threads=1:log-level=error:hdr10=1:repeat-headers=1"
		if mastered {
			params += ":master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50):max-cll=1000,400"
		}
		// Audio is stream zero: the frame observation must select the actual video.
		args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=sample_rate=48000", "-f", "lavfi", "-i", "testsrc2=size=64x64:rate=4",
			"-t", "1", "-map", "0:a", "-map", "1:v", "-c:a", "aac", "-c:v", "libx265", "-preset", "ultrafast", "-pix_fmt", "yuv420p10le",
			"-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", "-x265-params", params, file}
		if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, out)
		}
		facts, err := e.probe.Inspect(ctx, file)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		place := db.FileOnDisk{Path: file, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano()}
		got := e.observeToneMappingSource(ctx, file, place, 0, facts)
		if !got.FrameHDRChecked || len(got.Streams) != 2 || (got.Streams[1].Detail.MasterDisplay != "") != mastered {
			t.Fatalf("mastered=%v: %+v", mastered, got)
		}
		if facts.Streams[1].Detail.MasterDisplay != "" {
			t.Fatal("observation mutated shared header facts")
		}
		key := fmt.Sprint(0, "\x00", file, "\x00", place.Size, "\x00", place.ModifiedNS)
		if !e.facts[key].FrameHDRChecked {
			t.Fatal("successful observation was not cached for this file version")
		}
		clear(e.facts)
		e.probe.Binary = filepath.Join(root, "missing-ffprobe")
		failed := e.observeToneMappingSource(ctx, file, place, 0, facts)
		if !failed.FrameHDRChecked || failed.Streams[1].Detail.MasterDisplay != "" {
			t.Fatal("failed frame read was not remembered as unknown mastering data", failed)
		}
		e.probe.Binary = probe
		// The working tool would find mastering data in the mastered fixture.
		// Replaying this version must use the cached unknown result immediately.
		if replay := e.observeToneMappingSource(ctx, file, place, 0, facts); !replay.FrameHDRChecked || replay.Streams[1].Detail.MasterDisplay != "" {
			t.Fatal("another Play retried a failed observation", replay)
		}
	}
}
