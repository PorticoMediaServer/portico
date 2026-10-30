package decoder

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

func TestBitmapBurnSeekKeepsAudioAndPictureOnOneClock(t *testing.T) {
	binary, probe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	source, subtitle := filepath.Join(dir, "source.mkv"), filepath.Join(dir, "selected.sup")
	runTinyMedia(t, ctx, binary, []string{"-v", "error", "-nostdin", "-f", "lavfi", "-i", "color=black:s=160x90:r=24:d=8", "-f", "lavfi", "-i", "sine=r=48000:d=8", "-c:v", "ffv1", "-c:a", "aac", source}, dir)
	if err := os.WriteFile(subtitle, pgsFixture(), 0600); err != nil {
		t.Fatal(err)
	}
	origin, err := BurnInputOrigin(ctx, probe, subtitle, "pgs")
	if err != nil || origin != 5000000 {
		t.Fatalf("origin %d: %v", origin, err)
	}
	for _, copyAudio := range []bool{true, false} {
		t.Run(strconv.FormatBool(copyAudio), func(t *testing.T) {
			g, err := BuildConversion(ConversionRequest{ConvertVideo: true, CopyAudio: copyAudio, ConvertAudio: !copyAudio, SoftwarePreset: "ultrafast", BurnIn: &BurnIn{File: subtitle, Format: "pgs", InputStartUS: origin, PositionUS: 4000000, Width: 160, Height: 90}})
			if err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(dir, "output-"+strconv.FormatBool(copyAudio)+".mp4")
			args := append([]string{"-v", "error", "-nostdin", "-ss", "4"}, g.Input...)
			args = append(args, "-i", source)
			args = append(args, g.ExtraInput...)
			args = append(args, "-map", g.VideoMap, "-map", "0:a:0")
			args = append(args, g.Video...)
			args = append(args, g.Audio...)
			args = append(args, "-t", "2", "-output_ts_offset", "4", "-use_editlist", "0", output)
			runTinyMedia(t, ctx, binary, args, dir)
			cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: probe, Args: []string{"-v", "error", "-show_entries", "packet=stream_index,pts_time", "-of", "json", output}, ReadPaths: []string{output}})
			if err != nil {
				t.Fatal(err)
			}
			data, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Packets []struct {
					Stream int    `json:"stream_index"`
					PTS    string `json:"pts_time"`
				} `json:"packets"`
			}
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			first := map[int]float64{}
			for _, packet := range result.Packets {
				pts, err := strconv.ParseFloat(packet.PTS, 64)
				if err != nil {
					t.Fatal(err)
				}
				if old, ok := first[packet.Stream]; !ok || pts < old {
					first[packet.Stream] = pts
				}
			}
			if len(first) != 2 || math.Abs(first[0]-first[1]) > .1 {
				t.Fatalf("picture and audio clocks disagree after bitmap seek: %v", first)
			}
		})
	}
}
