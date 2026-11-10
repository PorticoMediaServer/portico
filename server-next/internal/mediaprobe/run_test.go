package mediaprobe

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

// A real, confined ffprobe reads a generated file through the bridge: it is
// given a loopback address and never a path, and its facts come back typed.
func TestConfinedProbeReadsAFileThroughTheBridge(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	path := filepath.Join(t.TempDir(), "probe.mkv")
	generate := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "3", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-c:a", "aac", "-ac", "2", path)
	if out, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := decoder.ResolveLibraries(ffmpeg, ffprobe, nil)
	if err != nil {
		t.Fatal(err)
	}
	tool := Tool{Executable: ffprobe, Libraries: libraries}
	tool.Identity.ExecutableSHA256, tool.Identity.Version = strings.Repeat("0", 64), "test"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := Run(ctx, &processpool.Supervisor{Limit: 1}, "probe-test", tool, file, info.Size())
	if err != nil {
		if os.Getenv("PORTICO_REQUIRE_DECODER_SANDBOX") != "1" && strings.Contains(err.Error(), "sandbox") {
			t.Skipf("no decoder sandbox here: %v", err)
		}
		t.Fatal(err)
	}
	video, audio := false, false
	for _, stream := range result.Facts.Streams {
		video = video || stream.Kind == "video" && stream.Codec == "h264" && stream.Video != nil && stream.Video.Width == 320
		audio = audio || stream.Kind == "audio" && stream.Codec == "aac" && stream.Audio != nil && stream.Audio.SampleRate == 48000
	}
	duration := result.Facts.Format.DurationSeconds
	if !video || !audio || duration == nil || duration.Numerator <= 0 {
		t.Fatalf("facts are missing what the file has: %+v", result.Facts)
	}
}
