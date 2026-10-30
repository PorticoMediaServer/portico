package mediaanalysis

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitlevideo"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

func TestSonicActualConfinedCueDecodeAndSingleAllocation(t *testing.T) {
	testtier.Media(t, "actual FLAC/soxr range decoder and original musicnn inference")
	helper, model := os.Getenv("PORTICO_SONIC_HELPER_TEST"), os.Getenv("PORTICO_SONIC_MODEL_TEST")
	if helper == "" || model == "" {
		t.Skip("actual checked Sonic model/helper required")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	dir := canonicalFixtureDir(t)
	if _, err = mediaexec.Configure(mediaexec.Options{StateDir: filepath.Join(dir, "state"), SkipOrphanKill: true}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	path := filepath.Join(dir, "cue.flac")
	command, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: ffmpeg, Args: []string{"-nostdin", "-v", "error", "-threads", "1", "-filter_threads", "1", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:duration=9", "-ac", "2", "-c:a", "flac", path}, WriteDirs: []string{dir}, Background: true, MaxFileBytes: 8 << 20, MaxCPUSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("real FLAC: %v: %s", err, output)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	observed, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	input := &episodeDecodeInput{file: file, observed: observed}
	sup := &storage.Supervisor{Limit: 1}
	runtime, err := sonic.OpenRuntime(sonic.RuntimeOptions{Helper: helper, ModelDirectory: model, Supervisor: sup})
	if err != nil {
		t.Fatal(err)
	}
	locks, err := livechannels.NewPhysicalLocks(filepath.Join(dir, "physical"))
	if err != nil {
		t.Fatal(err)
	}
	physical, err := locks.Lock(livechannels.Allocation{ID: "0123456789abcdef0123456789abcdef0123456789abcdef", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer physical.Close()
	bridge := subtitlevideo.NewMediaBridge(input)
	decoded := 0
	decode := func(ctx context.Context, key string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
		decoded++
		if spec.Kind != "sonic_pcm" || spec.StartUS != 3000000 || spec.RegionUS != 6000000 {
			t.Fatalf("wrong cue region: %+v", spec)
		}
		spec.PhysicalLock = physical
		return bridge.With(ctx, func(url string, reservation *decoder.EndpointReservation) error {
			return decoder.RunAnalysis(ctx, sup, key, ffmpeg, url, reservation, spec, consume)
		})
	}
	s := &Service{options: Options{Sonic: runtime, MaxGeneratedBytes: 1 << 20}}
	out, err := s.produceSonic(ctx, binding{AudioCodec: "flac", DurationUS: 9000000, SonicTracks: []sonicTrackBinding{
		{EntityID: 1, StartUS: 3000000, EndUS: 9000000}, {EntityID: 2, StartUS: 3000000, EndUS: 9000000},
	}}, decode, "cue-fixture", physical)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != 1 || len(out.Sonic) != 2 || out.Sonic[0].Analysis.Patches != 2 || out.Sonic[0].Analysis != out.Sonic[1].Analysis || runtime.Availability().State != "ready" || sup.Active() != 0 {
		t.Fatalf("unretired/duplicated/unbound analysis: decoded=%d results=%+v active=%d", decoded, out.Sonic, sup.Active())
	}
	if out.Sonic[0].Binding.EntityID == out.Sonic[1].Binding.EntityID {
		t.Fatal("shared source lost song identity")
	}
	if err = input.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	input.mu.Lock()
	defer input.mu.Unlock()
	if input.maxRead > 1<<20 || input.bytes < 1 {
		t.Fatal("decode bypassed bounded source bridge", input.maxRead, input.bytes)
	}
}
