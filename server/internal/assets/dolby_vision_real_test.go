package assets

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
)

// Procedural repository clips exercise actual decoder side data, not supplied
// licensed films or a configuration-only JSON fixture. They do not certify
// mastered FEL residual reconstruction or a physical display.
func TestDolbyVisionDecodedSourceFactsFromGeneratedMedia(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	fixtures, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "video", "hdr-generated"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		file, layer   string
		dv, hdr10plus bool
	}{
		{"source-MEL.mp4", "MEL", true, false}, {"source-FEL.mp4", "FEL", true, false},
		{"dv-8.1.hevc", "", true, false}, {"dv-8.4.hevc", "", true, false},
		{"base-pq.hevc", "", false, false}, {"hdr10plus.hevc", "", false, true},
	} {
		t.Run(sample.file, func(t *testing.T) {
			path := filepath.Join(fixtures, sample.file)
			if filepath.Ext(sample.file) == ".mp4" {
				path = filepath.Join(filepath.Dir(fixtures), "dovi-probe-r40", sample.file)
			} else {
				path = filepath.Join(t.TempDir(), "source.mp4")
				out, err := exec.Command(ffmpeg, "-v", "error", "-nostdin", "-threads", "1", "-r", "24", "-i", filepath.Join(fixtures, sample.file), "-map", "0:v:0", "-c:v", "copy", "-use_editlist", "0", path).CombinedOutput()
				if err != nil {
					t.Fatalf("procedural fixture mux: %v %s", err, out)
				}
			}
			probe := Probe{Binary: ffprobe}
			facts, err := probe.Inspect(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if len(facts.Streams) != 1 {
				t.Fatalf("unexpected streams: %d", len(facts.Streams))
			}
			before := facts.Streams[0].Detail
			if before.DolbyVisionRPU || before.DolbyVisionBaseLayer || before.DolbyVisionMetadataKnown {
				t.Fatal("stream configuration claimed decoded packet observations")
			}
			probe.AnalyzePlayback(context.Background(), path, &facts)
			got := facts.Streams[0].Detail
			if got.DolbyVisionRPU != sample.dv || got.DolbyVisionBaseLayer != sample.dv || got.DolbyVisionMetadataKnown != sample.dv || got.DolbyVisionEnhancementType != sample.layer || got.HDR10Plus != sample.hdr10plus || got.DolbyVisionCMVersion != "" {
				t.Fatalf("decoded source facts: %+v; wanted DV=%t EL=%q HDR10+=%t", got, sample.dv, sample.layer, sample.hdr10plus)
			}
		})
	}
}
