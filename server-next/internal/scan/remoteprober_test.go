package scan_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// A real film on a WebDAV server is read by the real prober through its source's throttle,
// by range, with a handful of requests.
func TestRemoteProberReadsAFileOnWebDAV(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	decodertest.QualifiedFFprobe(t)
	film := filepath.Join(t.TempDir(), "film.mkv")
	out, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25", "-f", "lavfi", "-i",
		"sine=frequency=440:sample_rate=48000", "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-metadata:s:a:0", "language=eng", film).CombinedOutput()
	if err != nil {
		decodertest.Unavailable(t, fmt.Sprintf("fixture encode unavailable: %v %s", err, out))
	}
	raw, err := os.ReadFile(film)
	if err != nil {
		t.Fatal(err)
	}
	dav := remotetest.NewDAV(t, map[string]string{"Films/film.mkv": string(raw)})
	src, _ := remote.NewWebDAV(dav.URL, dav.User, dav.Password)
	file, err := remote.Throttled(src, remote.NewThrottle(remote.Limits{Connections: 4, PerSecond: 4})).Open(ctx, "Films/film.mkv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	facts, err := scan.RemoteProber{}.ProbeReader(ctx, file, file.Size())
	if err != nil {
		t.Fatal(err)
	}
	if facts.Container != "matroska" || facts.DurationMS < 1900 || facts.DurationMS > 2200 || len(facts.Streams) != 2 {
		t.Fatalf("facts = %+v", facts)
	}
	if v, a := facts.Streams[0], facts.Streams[1]; v.Kind != db.StreamVideo || v.Width != 320 || a.Language != "eng" {
		t.Fatalf("streams = %+v", facts.Streams)
	}
	if dav.Reads.Load() > 4 {
		t.Fatalf("reading a small film's facts took %d requests", dav.Reads.Load())
	}
}
