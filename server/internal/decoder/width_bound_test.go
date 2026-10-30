package decoder

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
)

func TestConversionFitsBothClientBoundsWithRealFFmpeg(t *testing.T) {
	ffmpeg, probe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	for _, size := range [][2]int{{2560, 1080}, {2560, 720}, {1280, 720}, {1920, 1080}, {1024, 1600}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			g, err := BuildConversion(ConversionRequest{ConvertVideo: true, MaxWidth: 1920, MaxHeight: 1080, SoftwarePreset: "ultrafast"})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "fit.mp4")
			args := []string{"-v", "error", "-nostdin", "-f", "lavfi", "-i", fmt.Sprintf("color=c=blue:s=%dx%d:r=1", size[0], size[1]), "-frames:v", "1"}
			args = append(args, g.Video...)
			args = append(args, "-threads", "1", path)
			if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
				t.Fatalf("encode: %v %s", err, out)
			}
			out, err := exec.Command(probe, "-v", "error", "-show_entries", "stream=width,height", "-of", "json", path).Output()
			if err != nil {
				t.Fatal(err)
			}
			var facts struct{ Streams []struct{ Width, Height int } }
			if err = json.Unmarshal(out, &facts); err != nil || len(facts.Streams) != 1 {
				t.Fatalf("probe: %s %v", out, err)
			}
			got := facts.Streams[0]
			if got.Width > 1920 || got.Height > 1080 || got.Width > size[0] || got.Height > size[1] || got.Width%2 != 0 || got.Height%2 != 0 {
				t.Fatalf("out of bounds: %+v", got)
			}
			if size[0] <= 1920 && size[1] <= 1080 && (got.Width != size[0] || got.Height != size[1]) {
				t.Fatalf("unneeded scale: %+v", got)
			}
		})
	}
}

func TestEveryScaleRoundsInsideBothBounds(t *testing.T) {
	for _, backend := range []HardwareBackend{BackendSoftware, BackendNVENC, BackendVAAPI, BackendQSV} {
		got := boundedScaleFilter(backend, 1920, 1080)
		if !strings.Contains(got, "1920/") || !strings.Contains(got, "1080/ih") || !strings.Contains(got, "/2)*2") {
			t.Fatal(backend, got)
		}
		if backend == BackendSoftware && !strings.HasSuffix(got, "setsar=1") {
			t.Fatal(got)
		}
	}
}
