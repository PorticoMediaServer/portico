package subtitlevideo

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// A retained byte version supplies the same bridge interface as remote media;
// there is no provider URL or outbound request in this fixture.
type windowProbeInput struct {
	data   []byte
	checks atomic.Int32
	failAt int
}

func (*windowProbeInput) Evidence() string { return "pinned-version" }
func (f *windowProbeInput) Size() int64    { return int64(len(f.data)) }
func (*windowProbeInput) Close() error     { return nil }
func (f *windowProbeInput) Validate(context.Context) error {
	checked := f.checks.Add(1)
	if f.failAt > 0 && checked >= int32(f.failAt) {
		return subtitles.ErrConflict
	}
	return nil
}
func (f *windowProbeInput) ReadExtent(_ context.Context, offset, length int64) ([]byte, error) {
	return f.data[offset : offset+length], nil
}

func TestWindowProbeValidatesRetainedInputBeforeAndAfter(t *testing.T) {
	tool, err := exec.LookPath("true")
	if err != nil {
		t.Skip("true(1) unavailable")
	}
	tool, err = filepath.EvalSymlinks(tool)
	if err != nil {
		t.Fatal(err)
	}
	for _, failAt := range []int{1, 2} {
		input := &windowProbeInput{failAt: failAt}
		supervisor := &storage.Supervisor{Limit: 1}
		window := &playbackWindow{runtime: &Runtime{storage: &storage.Client{Supervisor: supervisor}, ffprobe: tool}, input: input, bridge: NewMediaBridge(input)}
		out, err := window.Probe(context.Background(), []string{"-show_entries", "format=start_time", "-of", "csv=p=0"})
		if !errors.Is(err, subtitles.ErrConflict) || out != nil || input.checks.Load() != int32(failAt) {
			t.Fatalf("fail at %d: output=%q checks=%d error=%v", failAt, out, input.checks.Load(), err)
		}
		if supervisor.Active() != 0 {
			t.Fatal("probe retained a physical process")
		}
	}
}

func TestWindowProbeRealKeyframeTimeline(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "timeline.mp4")
	out, err := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-t", "19", "-c:v", "libx264", "-preset", "ultrafast", "-g", "96", "-keyint_min", "96", "-sc_threshold", "0", "-threads", "1", "-pix_fmt", "yuv420p", "-movflags", "+faststart", path).CombinedOutput()
	if err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := decoder.ResolveLibraries(ffmpeg, ffprobe, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := &windowProbeInput{data: data}
	supervisor := &storage.Supervisor{Limit: 1}
	window := &playbackWindow{runtime: &Runtime{storage: &storage.Client{Supervisor: supervisor}, ffprobe: ffprobe, libraries: libraries}, input: input, bridge: NewMediaBridge(input)}
	defer window.Close()
	out, err = window.Probe(ctx, []string{"-show_entries", "format=start_time", "-of", "csv=p=0"})
	if err != nil {
		t.Fatal(err)
	}
	start, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatal(err, string(out))
	}
	out, err = window.Probe(ctx, []string{"-select_streams", "v:0", "-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", "-read_intervals", "6.000000%+#1,12.000000%+#1,18.000000%+#1"})
	if err != nil {
		t.Fatal(err)
	}
	boundaries := []float64{0}
	for _, row := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		stamp, flags, _ := strings.Cut(row, ",")
		value, err := strconv.ParseFloat(stamp, 64)
		if err != nil || !strings.HasPrefix(flags, "K") {
			t.Fatalf("non-keyframe interval: %q", out)
		}
		normalized := value - start
		rounded := math.Round(normalized)
		if math.Abs(normalized-rounded) > 0.001 {
			t.Fatalf("unexpected keyframe: %q", out)
		}
		boundaries = append(boundaries, rounded)
	}
	boundaries = append(boundaries, 19)
	if !reflect.DeepEqual(boundaries, []float64{0, 4, 12, 16, 19}) {
		t.Fatalf("bridge keyframe timeline: %v", boundaries)
	}
	if supervisor.Active() != 0 {
		t.Fatal("probe did not retire")
	}
	if input.checks.Load() < 4 {
		t.Fatalf("missing input validation: %d", input.checks.Load())
	}
}
