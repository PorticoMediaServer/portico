package playback

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

func TestH264ManifestDoesNotInventConstraintFlags(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	binary := decodertest.QualifiedFFmpeg(t)
	for _, profile := range []string{"main", "baseline"} {
		t.Run(profile, func(t *testing.T) {
			target := decoder.ConversionTarget{VideoCodec: "h264", BitDepth: 8, DynamicRange: "sdr", Width: 160, Height: 90, VideoProfile: profile, VideoLevel: 31}
			p := DeliveryPlan{Profile: DeliveryProfileLegacy, Mode: "hls", VideoAction: "convert", AudioAction: "none", OutputVideoCodec: "h264", ConversionTarget: &target, SoftwarePreset: "veryfast"}
			g, err := p.conversionGraph(nil)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "clip.mp4")
			args := append([]string{"-v", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc2=s=160x90:r=24:d=0.5"}, g.Video...)
			args = append(args, "-an", path)
			cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: binary, Args: args, WriteDirs: []string{filepath.Dir(path)}})
			if err != nil {
				t.Fatal(err)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("encode %v: %s", err, out)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			at := bytes.Index(data, []byte("avcC"))
			if at < 0 || at+8 > len(data) {
				t.Fatal("missing avcC")
			}
			actual := fmt.Sprintf("avc1.%02x%02x%02x", data[at+5], data[at+6], data[at+7])
			if declared := videoCodecString(&p); declared != "" && declared != actual {
				t.Fatalf("manifest codec %s does not describe encoded SPS %s", declared, actual)
			}
		})
	}
}
