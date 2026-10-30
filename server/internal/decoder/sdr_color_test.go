package decoder

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

func TestSDRConversionRetainsSourceColourDescription(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	binary, probe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	dir := t.TempDir()
	source, output := filepath.Join(dir, "source.mkv"), filepath.Join(dir, "output.mp4")
	runTinyMedia(t, ctx, binary, []string{"-v", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc2=s=160x90:r=24:d=0.25", "-c:v", "ffv1", "-colorspace", "smpte170m", "-color_primaries", "smpte170m", "-color_trc", "smpte170m", "-color_range", "tv", source}, dir)
	g, err := BuildConversion(ConversionRequest{ConvertVideo: true, SoftwarePreset: "ultrafast"})
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-v", "error", "-nostdin", "-i", source}, g.Video...)
	args = append(args, "-an", output)
	runTinyMedia(t, ctx, binary, args, dir)
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: probe, Args: []string{"-v", "error", "-show_entries", "stream=color_space,color_primaries,color_transfer,color_range", "-of", "json", output}, ReadPaths: []string{output}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Streams []struct {
			Matrix    string `json:"color_space"`
			Primaries string `json:"color_primaries"`
			Transfer  string `json:"color_transfer"`
			Range     string `json:"color_range"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Streams) != 1 {
		t.Fatalf("unexpected streams: %s", data)
	}
	s := result.Streams[0]
	if s.Matrix != "smpte170m" || s.Range != "tv" {
		t.Fatalf("SDR samples were relabelled without a colour transform: %s", data)
	}
}
